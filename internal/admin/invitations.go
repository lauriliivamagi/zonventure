package admin

import (
	"context"
	"errors"
	"github.com/klahr/zonventure/internal/emailtpl"
	"net/http"
	"net/url"
	"strings"

	"github.com/klahr/zonventure/internal/activity"
	"github.com/klahr/zonventure/internal/auth"
	"github.com/klahr/zonventure/internal/httpx"
	"github.com/klahr/zonventure/internal/icscal"
	"github.com/klahr/zonventure/internal/logx"
	"github.com/klahr/zonventure/internal/mailer"
	"github.com/klahr/zonventure/internal/ws"
)

// SendEventInvitations triggers an outbound email to every team in
// the event that has a non-empty contact email. Each team gets one
// personalised message with that team's player slot links + the
// event's .ics attached, so participants can both join and add the
// event to their calendar in one go.
//
// Authorisation: authorizeEvent — org_admin and org_member of the
// owning org both pass. Sending invites is a "running the game"
// action (Race directors do this on the day), not authoring, so the
// canAuthor split deliberately does not apply.
//
// Response shape:
//
//	{ "sent": <n>, "skipped_no_email": <n>, "failed": <n> }
//
// Mailer failures count toward `failed` but don't abort the loop —
// one bad SMTP attempt shouldn't lose the rest of the team. Per-
// team errors are logged via logx.Warn so an operator can correlate.
func (h *Handlers) SendEventInvitations(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFrom(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad event id")
		return
	}
	if _, ok := h.authorizeEvent(w, r, eventID); !ok {
		return
	}
	if h.Mailer == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "no mailer configured")
		return
	}

	// Build the .ics once; each team's copy then links to its own team
	// page. A missing schedule (event not yet planned) just drops the
	// attachment; the magic links still go out so an arranger can
	// pre-distribute before scheduling.
	evt, icsErr := icscal.BuildForEvent(r.Context(), h.DB, h.Lib, eventID, h.eventBase(r.Context(), eventID))
	if icsErr != nil && !errors.Is(icsErr, icscal.ErrNoSchedule) {
		logx.Warn("admin.invitations.ics", "event", eventID, "err", icsErr.Error())
	}

	// Pull every team's contact + slot tokens. One row per slot;
	// teams without slots are filtered out by the inner join, and
	// teams without an email are surfaced so we can count them as
	// `skipped_no_email` rather than silently dropping.
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT t.id, t.name, COALESCE(t.email, ''), t.token, COALESCE(t.invited_start, ''), t.lang, ps.name, ps.token
		FROM teams t
		LEFT JOIN player_slots ps ON ps.team_id = t.id
		WHERE t.event_id = $1
		ORDER BY t.id, ps.id
	`, eventID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "team load failed")
		return
	}
	defer rows.Close()

	type teamInfo struct {
		ID      int64
		Name    string
		Email   string
		Token   string
		Invited string
		Lang    string
		Slots   []invitationSlot
	}
	var (
		teams []*teamInfo
		byID  = map[int64]*teamInfo{}
	)
	for rows.Next() {
		var (
			tid    int64
			tname  string
			temail string
			ttoken string
			tinv   string
			tlang  string
			pname  *string
			ptoken *string
		)
		if err := rows.Scan(&tid, &tname, &temail, &ttoken, &tinv, &tlang, &pname, &ptoken); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "team scan failed")
			return
		}
		t, ok := byID[tid]
		if !ok {
			t = &teamInfo{ID: tid, Name: tname, Email: strings.TrimSpace(temail), Token: ttoken, Invited: tinv, Lang: tlang}
			byID[tid] = t
			teams = append(teams, t)
		}
		if pname != nil && ptoken != nil {
			t.Slots = append(t.Slots, invitationSlot{Player: *pname, Token: *ptoken})
		}
	}

	var (
		sent           int
		skippedNoEmail int
		failed         int
	)
	publicURL := h.eventBase(r.Context(), eventID)
	l := h.eventLook(r.Context(), eventID)
	words := h.eventWords(r.Context(), eventID)
	start, place := h.inviteWhen(r.Context(), eventID)
	for _, t := range teams {
		if t.Email == "" {
			skippedNoEmail++
			continue
		}
		if len(t.Slots) == 0 {
			// Teams without slots have nothing to send. Lump under
			// skipped_no_email to keep the response shape simple;
			// in practice it never happens (CreateTeam requires at
			// least one member).
			skippedNoEmail++
			continue
		}
		// In the language the team plays in.
		if err := sendOneInvitation(r.Context(), h.Mailer, words.lookIn(l, t.Lang), t.Name, t.Email, publicURL, t.Slots, h.fileLinks(r.Context(), eventID, publicURL, t.Token, t.Lang), teamICS(words.calendarIn(evt, l, t.Lang), icsErr, eventID, publicURL, t.Token), start, words.placeIn(place, t.Lang), t.Invited); err != nil {
			logx.Warn("admin.invitations.send", "event", eventID, "team", t.ID, "err", err.Error())
			failed++
			continue
		}
		h.markInvited(r.Context(), t.ID, start)
		sent++
	}
	h.recordInvitations(r.Context(), eventID, nil, l, sent, skippedNoEmail)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"sent":             sent,
		"skipped_no_email": skippedNoEmail,
		"failed":           failed,
	})
}

// SendTeamInvitation is the per-team variant: resend exactly one
// team's email. The action a Race director reaches for when one
// team lost their original message and pings the arranger mid-event.
//
// Existing tokens are sent verbatim — no re-mint. If a player has
// already claimed their slot and lost their browser session,
// they should hit the public "I lost my magic link" recovery flow
// instead, which intentionally re-mints. The split keeps "arranger
// helps a team" cheap (no surprise session invalidations) while
// still giving the player a self-serve fresh-token path.
//
// Same authorize-event gate as the bulk send.
func (h *Handlers) SendTeamInvitation(w http.ResponseWriter, r *http.Request) {
	teamID, ok := teamIDFrom(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad team id")
		return
	}
	h.sendTeamInvitation(w, r, teamID, 0)
}

// sendTeamInvitation mails one team its links. operatorOrg is 0 for an
// org user (the authorize-event gate); for the platform operator it is
// the org in the URL, which the team's event must belong to.
func (h *Handlers) sendTeamInvitation(w http.ResponseWriter, r *http.Request, teamID, operatorOrg int64) {
	if h.Mailer == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "no mailer configured")
		return
	}
	// Resolve the team's event_id so the standard event-auth gate
	// applies. Avoids a per-team auth helper duplicating sameOrg.
	var (
		eventID  int64
		teamName string
		teamMail string
		teamTok  string
		invited  string
		teamLang string
	)
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT event_id, name, COALESCE(email, ''), token, COALESCE(invited_start, ''), lang FROM teams WHERE id = $1`, teamID).
		Scan(&eventID, &teamName, &teamMail, &teamTok, &invited, &teamLang); err != nil {
		httpx.Error(w, http.StatusNotFound, "team not found")
		return
	}
	if operatorOrg == 0 {
		if _, ok := h.authorizeEvent(w, r, eventID); !ok {
			return
		}
	} else if org, err := h.eventOrg(r.Context(), eventID); err != nil || org != operatorOrg {
		httpx.Error(w, http.StatusNotFound, "team not found")
		return
	}
	teamMail = strings.TrimSpace(teamMail)
	if teamMail == "" {
		httpx.Error(w, http.StatusBadRequest, "team has no contact email — set one first")
		return
	}

	// Slot tokens for this team. ORDER BY so the email body lists
	// players in the same order every send → cleaner re-receive.
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT name, token FROM player_slots WHERE team_id = $1 ORDER BY id`, teamID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "slot load failed")
		return
	}
	var slots []invitationSlot
	for rows.Next() {
		var s invitationSlot
		if err := rows.Scan(&s.Player, &s.Token); err != nil {
			rows.Close()
			httpx.Error(w, http.StatusInternalServerError, "slot scan failed")
			return
		}
		slots = append(slots, s)
	}
	rows.Close()
	if len(slots) == 0 {
		httpx.Error(w, http.StatusBadRequest, "team has no players to invite")
		return
	}

	// Same .ics-once approach as the bulk handler — built per
	// resend rather than cached, since arrangers may be re-sending
	// after a reschedule and the file should reflect the latest
	// scheduled_at.
	evt, icsErr := icscal.BuildForEvent(r.Context(), h.DB, h.Lib, eventID, h.eventBase(r.Context(), eventID))
	if icsErr != nil && !errors.Is(icsErr, icscal.ErrNoSchedule) {
		logx.Warn("admin.invitations.team.ics", "event", eventID, "team", teamID, "err", icsErr.Error())
	}

	publicURL := h.eventBase(r.Context(), eventID)
	l := h.eventLook(r.Context(), eventID)
	words := h.eventWords(r.Context(), eventID)
	start, place := h.inviteWhen(r.Context(), eventID)
	if err := sendOneInvitation(r.Context(), h.Mailer, words.lookIn(l, teamLang), teamName, teamMail, publicURL, slots, h.fileLinks(r.Context(), eventID, publicURL, teamTok, teamLang), teamICS(words.calendarIn(evt, l, teamLang), icsErr, eventID, publicURL, teamTok), start, words.placeIn(place, teamLang), invited); err != nil {
		logx.Warn("admin.invitations.team.send", "event", eventID, "team", teamID, "err", err.Error())
		httpx.ServerErr(w, http.StatusBadGateway, "send failed", err)
		return
	}
	h.markInvited(r.Context(), teamID, start)
	h.recordInvitations(r.Context(), eventID, &teamID, l, 1, 0)
	httpx.JSON(w, http.StatusOK, map[string]any{"sent": 1})
}

// recordInvitations puts invitations that went out on the event's
// record, as EmailTeams does an arranger's email: in the emails sent to
// its teams, and in its activity (for one team, under that team).
func (h *Handlers) recordInvitations(ctx context.Context, eventID int64, teamID *int64, l look, sent, skipped int) {
	if sent == 0 {
		return
	}
	me, _ := auth.FromContext(ctx)
	subject, _ := emailtpl.TeamInvitation(l.Lang, "", l.Event, l.Brand.Name, nil, false)
	if _, err := h.DB.ExecContext(ctx, `
		INSERT INTO event_emails (event_id, subject, body, theme, sent_by, sent, skipped) VALUES ($1, $2, '', $3, $4, $5, $6)
	`, eventID, subject, string(l.Theme), me.ID, sent, skipped); err != nil {
		logx.Warn("admin.invitations.record", "event", eventID, "err", err.Error())
	}
	activity.Record(ctx, h.DB, eventID, teamID, activity.UserActor(me.ID), "invitations.sent", map[string]any{"sent": sent, "skipped": skipped})
	h.Hub.Publish(ws.EventTopic(eventID), "changed", nil)
}

// inviteWhen is what an event's invitations say of when and where: its
// scheduled start (UTC, as stored; "" without one, or for a self-guided
// event, played whenever a team likes) and its meeting place.
func (h *Handlers) inviteWhen(ctx context.Context, eventID int64) (start, place string) {
	var forSale bool
	if err := h.DB.QueryRowContext(ctx, `SELECT COALESCE(scheduled_at, ''), meeting_place, public_for_sale FROM events WHERE id = $1`, eventID).Scan(&start, &place, &forSale); err != nil || forSale {
		return "", strings.TrimSpace(place)
	}
	return start, strings.TrimSpace(place)
}

// markInvited remembers the start a team's invitation stated, so one sent
// after the event moves says the time changed.
func (h *Handlers) markInvited(ctx context.Context, teamID int64, start string) {
	if _, err := h.DB.ExecContext(ctx, `UPDATE teams SET invited_start = NULLIF($2, '') WHERE id = $1`, teamID, start); err != nil {
		logx.Warn("admin.invitations.mark", "team", teamID, "err", err.Error())
	}
}

// invitationSlot is the (player, token) pair the email body
// renders one line for. Hoisted out of the handler so the
// helper signature can name the type cleanly.
type invitationSlot struct {
	Player string
	Token  string
}

// sendOneInvitation builds and sends a single team's email. Pulled
// out so the loop in SendEventInvitations stays readable and the
// per-team body assembly can be unit-tested in isolation.
func sendOneInvitation(
	ctx context.Context,
	m mailer.Mailer,
	l look,
	teamName, teamEmail, publicURL string,
	slots []invitationSlot,
	files []emailtpl.FileLink,
	atts []mailer.Attachment,
	start, place, invited string,
) error {
	players := make([]emailtpl.PlayerLink, len(slots))
	for i, s := range slots {
		players[i] = emailtpl.PlayerLink{Team: teamName, Player: s.Player, URL: publicURL + "/play?t=" + s.Token}
	}
	subject, c := emailtpl.TeamInvitation(l.Lang, teamName, l.Event, l.Brand.Name, players, len(atts) > 0)
	when := ""
	if t, ok := icscal.ParseTimestamp(start); ok {
		when = emailtpl.When(l.Lang, t, l.TimeZone)
	}
	subject, c = emailtpl.WithSchedule(subject, c, l.Event, when, place, invited != "" && start != "" && invited != start)
	c = emailtpl.WithFiles(c, files)
	c.Picture = l.Cover
	msg := l.message([]string{teamEmail}, subject, c)
	msg.Attachments = atts
	return m.Send(ctx, msg)
}

// teamICS is the calendar attachment for one team's invitation: the
// event's entry, linking to that team's own page rather than the
// generic /play. Nil when the event has no schedule to put on a
// calendar (buildErr) or the entry can't be rendered.
func teamICS(evt icscal.Event, buildErr error, eventID int64, publicURL, teamToken string) []mailer.Attachment {
	if buildErr != nil {
		return nil
	}
	if publicURL != "" {
		evt = evt.WithJoinURL(publicURL + "/team/" + url.PathEscape(teamToken))
	}
	b, err := icscal.Bytes(evt)
	if err != nil {
		return nil
	}
	return []mailer.Attachment{{
		Filename:    icscal.Filename(eventID),
		ContentType: "text/calendar; method=PUBLISH; charset=UTF-8",
		Bytes:       b,
	}}
}
