package admin

// Emails in the organisation's look (internal/emailtpl), and what
// arrangers do with them here:
//
//	GET  /api/admin/email-themes                       the themes and the email kinds
//	GET  /api/admin/email-preview?kind=&theme=&event_id=   a sample email, rendered
//	POST /api/admin/email-preview                      {subject, body, theme?, event_id?} an arranger's draft, rendered
//	PUT  /api/admin/orgs/me/email-theme                {theme} the organisation's default
//	PUT  /api/admin/events/{eventID}/email-theme       {theme} an event's own, "" for the organisation's
//	GET  /api/admin/events/{eventID}/emails            what was written to its teams
//	POST /api/admin/events/{eventID}/emails            {subject, body, team_ids?} write to its teams
//	GET  /api/admin/me/email-prefs                     which notification emails the caller gets
//	PUT  /api/admin/me/email-prefs                     {kind: on}

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/klahr/zonventure/internal/activity"
	"github.com/klahr/zonventure/internal/auth"
	"github.com/klahr/zonventure/internal/emailtpl"
	"github.com/klahr/zonventure/internal/httpx"
	"github.com/klahr/zonventure/internal/icscal"
	"github.com/klahr/zonventure/internal/logx"
	mailerpkg "github.com/klahr/zonventure/internal/mailer"
	"github.com/klahr/zonventure/internal/ws"
)

// look is how an email goes out: its theme and brand, and what it is
// sent for, for the footer.
type look struct {
	Theme emailtpl.Theme
	Brand emailtpl.Brand
	Event string
	// Lang is the language its emails are written in.
	Lang string
	// Cover is the event's adventure's picture for its players' emails.
	Cover emailtpl.Picture
	// BuyerNote is the adventure author's word to its buyer.
	BuyerNote string
	// Contact is the organisation's contact address, its emails'
	// Reply-To; TimeZone the one their times are written in.
	Contact  string
	TimeZone string
}

// eventLook is an event's: its theme, else its organisation's.
func (h *Handlers) eventLook(ctx context.Context, eventID int64) look {
	lk := emailtpl.Lookup(ctx, h.DB, h.PublicURL, 0, eventID)
	var name, note string
	_ = h.DB.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(e.name, ''), a.title, e.adventure_slug), COALESCE(a.buyer_note, '') FROM events e LEFT JOIN adventures a ON a.slug = e.adventure_slug WHERE e.id = $1
	`, eventID).Scan(&name, &note)
	return look{Theme: lk.Theme, Brand: lk.Brand, Event: name, Lang: lk.Lang, Cover: lk.Cover, BuyerNote: note, Contact: lk.Contact, TimeZone: lk.TimeZone}
}

// withBuyerNote adds the adventure's note to a buyer's email body.
func (l look) withBuyerNote(c emailtpl.Content) emailtpl.Content {
	if strings.TrimSpace(l.BuyerNote) != "" {
		c.Body += "\n\n" + emailtpl.Lines(l.BuyerNote)
	}
	return c
}

// orgLook is an organisation's, for what no event sends.
func (h *Handlers) orgLook(ctx context.Context, orgID int64) look {
	lk := emailtpl.Lookup(ctx, h.DB, h.PublicURL, orgID, 0)
	return look{Theme: lk.Theme, Brand: lk.Brand, Lang: lk.Lang, Contact: lk.Contact, TimeZone: lk.TimeZone}
}

// house is a house picture for an email, none without a public URL.
func (h *Handlers) house(lang, id string) emailtpl.Picture {
	return emailtpl.House(h.PublicURL, lang, id)
}

// emailCover is adventure slug's picture for its players' emails.
func (h *Handlers) emailCover(ctx context.Context, lang, slug string) emailtpl.Picture {
	return emailtpl.CoverFor(ctx, h.DB, h.PublicURL, lang, slug)
}

func (l look) message(to []string, subject string, c emailtpl.Content) mailerpkg.Message {
	msg := emailtpl.Message(to, subject, l.Theme, l.Brand, c)
	msg.ReplyTo = l.Contact
	return msg
}

var bareURL = regexp.MustCompile(`https?://[^\s<>()]+[^\s<>().,;:!?]`)

// themedText gives a message written as plain text the look: its
// subject as the heading, its lines as they were, its links clickable.
func (l look) themedText(msg mailerpkg.Message) mailerpkg.Message {
	body := bareURL.ReplaceAllStringFunc(emailtpl.Lines(msg.Text), func(u string) string { return "[" + u + "](" + u + ")" })
	out := l.message(msg.To, msg.Subject, emailtpl.Content{Lang: l.Lang, Heading: msg.Subject, Body: body})
	out.Attachments = msg.Attachments
	if msg.ReplyTo != "" {
		out.ReplyTo = msg.ReplyTo
	}
	return out
}

// send queues msg through the outbox when there is one, else sends it.
func (h *Handlers) send(ctx context.Context, kind string, msg mailerpkg.Message) error {
	switch {
	case h.MailOutbox != nil:
		return h.MailOutbox.Enqueue(ctx, kind, msg)
	case h.Mailer != nil:
		return h.Mailer.Send(ctx, msg)
	}
	return errors.New("no mailer configured")
}

// EmailThemes lists the themes and the emails there are to preview.
func (h *Handlers) EmailThemes(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"themes": emailtpl.Themes, "kinds": emailtpl.Kinds})
}

type previewDTO struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
	Theme   string `json:"theme"`
}

// previewLook is the look a preview shows: the event's, or the caller's
// organisation's, in the theme asked for if any.
func (h *Handlers) previewLook(w http.ResponseWriter, r *http.Request, eventID int64, theme string) (look, bool) {
	me, _ := auth.FromContext(r.Context())
	var l look
	if eventID > 0 {
		if _, ok := h.authorizeEvent(w, r, eventID); !ok {
			return l, false
		}
		l = h.eventLook(r.Context(), eventID)
	} else if me.OrgID != nil {
		l = h.orgLook(r.Context(), *me.OrgID)
	} else {
		l = look{Theme: emailtpl.Classic, Brand: emailtpl.Platform(), Lang: "en"}
	}
	if theme != "" {
		if !emailtpl.Valid(emailtpl.Theme(theme)) {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "unknown theme", "code": "theme_invalid"})
			return l, false
		}
		l.Theme = emailtpl.Theme(theme)
	}
	return l, true
}

// EmailPreview renders a sample of one kind of email as the caller's
// organisation — or an event — would send it.
func (h *Handlers) EmailPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	eventID, _ := strconv.ParseInt(q.Get("event_id"), 10, 64)
	if p, ok := h.samplePreview(w, r, q.Get("kind"), eventID, q.Get("theme"), q.Get("lang")); ok {
		httpx.JSON(w, http.StatusOK, p)
	}
}

// samplePreview is a sample of one kind of email as it would be sent.
func (h *Handlers) samplePreview(w http.ResponseWriter, r *http.Request, kind string, eventID int64, theme, lang string) (previewDTO, bool) {
	k, ok := emailtpl.KindByID(kind)
	if !ok {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "unknown email", "code": "kind_invalid"})
		return previewDTO{}, false
	}
	l, ok := h.previewLook(w, r, eventID, theme)
	if !ok {
		return previewDTO{}, false
	}
	if lang != "" {
		l.Lang = emailtpl.Lang(lang)
	}
	subject, c := k.Sample(l.Lang, l.Brand.Name)
	if l.Event != "" {
		subject, c = sampleForEvent(k.ID, l)
	}
	if coverKinds[k.ID] {
		c.Picture = h.previewCover(r, eventID, l.Lang)
	}
	// The console shows the preview, and its pages load pictures and fonts
	// from the address it was opened at only: an organisation's logo and
	// typeface come from there, not its own domain, which its sent
	// emails use.
	if i := strings.Index(l.Brand.LogoURL, "/org-logo/"); i > 0 {
		l.Brand.LogoURL = "//" + r.Host + l.Brand.LogoURL[i:]
	}
	if i := strings.Index(l.Brand.TitleFontCSS, "/assets/"); i > 0 {
		l.Brand.TitleFontCSS = "//" + r.Host + l.Brand.TitleFontCSS[i:]
	}
	html, text := emailtpl.Render(l.Theme, l.Brand, subject, c)
	return previewDTO{Subject: subject, HTML: html, Text: text, Theme: string(l.Theme)}, true
}

// coverKinds are the previewed emails that show the adventure's cover.
var coverKinds = map[string]bool{"team_invitation": true, "game_email": true, "team_link": true, "play_links": true}

// previewCover is the cover a preview shows: the event's adventure's,
// else none: an organisation's look is its logo and colours, not a
// themed picture it never chose. Without a public URL it points at this
// site, where the console's preview frame finds it.
func (h *Handlers) previewCover(r *http.Request, eventID int64, lang string) emailtpl.Picture {
	base := h.PublicURL
	if strings.TrimSpace(base) == "" {
		base = "//" + r.Host
	}
	if eventID > 0 {
		var slug string
		if h.DB.QueryRowContext(r.Context(), `SELECT adventure_slug FROM events WHERE id = $1`, eventID).Scan(&slug) == nil {
			return emailtpl.CoverFor(r.Context(), h.DB, base, lang, slug)
		}
	}
	return emailtpl.Picture{}
}

// sampleForEvent is a kind's sample with the event's own name.
func sampleForEvent(kind string, l look) (string, emailtpl.Content) {
	org, lang := l.Brand.Name, l.Lang
	team := emailtpl.T(lang, "The Seagulls")
	switch kind {
	case "team_invitation":
		return emailtpl.TeamInvitation(lang, team, l.Event, org, []emailtpl.PlayerLink{
			{Player: "Anna", URL: "https://example.org/play?t=a1b2c3"}, {Player: "Ben", URL: "https://example.org/play?t=d4e5f6"},
		}, true)
	case "team_message":
		return emailtpl.TeamMessage(lang, emailtpl.T(lang, "A message from the organisers"), emailtpl.T(lang, "Hi %s!\n\nThis is how your message will look.", team), l.Event, org)
	case "game_email":
		return emailtpl.GameEmail(lang, emailtpl.T(lang, "A letter has arrived"), emailtpl.T(lang, "Dear %s,\n\nThis is how an email step reads during the game.", team), l.Event, org)
	}
	k, _ := emailtpl.KindByID(kind)
	return k.Sample(lang, org)
}

type draftPreviewRequest struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Theme   string `json:"theme"`
	EventID int64  `json:"event_id"`
	// Kind is team_message (the default), or game_email: what an email
	// step sends, with Step for {{step}}.
	Kind string `json:"kind"`
	Step string `json:"step"`
	// Lang previews it in a language other than the event's or the
	// organisation's.
	Lang string `json:"lang"`
}

// DraftPreview renders what an arranger is writing to teams, filled in
// as the first team would get it.
func (h *Handlers) DraftPreview(w http.ResponseWriter, r *http.Request) {
	var req draftPreviewRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if p, ok := h.draftPreview(w, r, req); ok {
		httpx.JSON(w, http.StatusOK, p)
	}
}

// draftPreview is what an arranger is writing, as the first team gets it.
func (h *Handlers) draftPreview(w http.ResponseWriter, r *http.Request, req draftPreviewRequest) (previewDTO, bool) {
	l, ok := h.previewLook(w, r, req.EventID, req.Theme)
	if !ok {
		return previewDTO{}, false
	}
	if req.Lang != "" {
		l.Lang = emailtpl.Lang(req.Lang)
	}
	team := emailtpl.T(l.Lang, "The Seagulls")
	if req.EventID > 0 {
		_ = h.DB.QueryRowContext(r.Context(), `SELECT name FROM teams WHERE event_id = $1 ORDER BY id LIMIT 1`, req.EventID).Scan(&team)
	}
	event := l.Event
	if event == "" {
		event = emailtpl.T(l.Lang, "Saturday's game")
	}
	var (
		subject string
		c       emailtpl.Content
	)
	switch req.Kind {
	case "game_email":
		step := strings.NewReplacer("{{step}}", req.Step)
		subject, c = emailtpl.GameEmail(l.Lang, step.Replace(fill(req.Subject, team, event)), step.Replace(fill(req.Body, team, event)), event, l.Brand.Name)
		c.Picture = h.previewCover(r, req.EventID, l.Lang)
	case "", "team_message":
		subject, c = emailtpl.TeamMessage(l.Lang, fill(req.Subject, team, event), fill(req.Body, team, event), event, l.Brand.Name)
	default:
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "unknown email", "code": "kind_invalid"})
		return previewDTO{}, false
	}
	html, text := emailtpl.Render(l.Theme, l.Brand, subject, c)
	return previewDTO{Subject: subject, HTML: html, Text: text, Theme: string(l.Theme)}, true
}

type testEmailRequest struct {
	draftPreviewRequest
	// Sample sends the sample of a kind of email instead of a draft.
	Sample string `json:"sample"`
}

// SendMeTest sends the caller, at their own address, exactly what a
// preview shows — a sample of one kind of email, or a draft — marked as
// a test in its subject. Limited as the SMTP test is, so it cannot be
// used to flood an inbox.
func (h *Handlers) SendMeTest(w http.ResponseWriter, r *http.Request) {
	me, _ := auth.FromContext(r.Context())
	if strings.TrimSpace(me.Email) == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "your account has no email address to send the test to", "code": "no_email"})
		return
	}
	var req testEmailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ip := auth.ClientIP(r)
	if ok, wait := testEmailLimit.allow(time.Now(), me.ID, ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		httpx.JSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many test emails — try again in a little while", "code": "rate_limited"})
		return
	}
	var (
		p  previewDTO
		ok bool
	)
	if req.Sample != "" {
		p, ok = h.samplePreview(w, r, req.Sample, req.EventID, req.Theme, req.Lang)
	} else {
		if strings.TrimSpace(req.Subject) == "" && strings.TrimSpace(req.Body) == "" {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "write something to test first", "code": "email_empty"})
			return
		}
		p, ok = h.draftPreview(w, r, req.draftPreviewRequest)
	}
	if !ok {
		return
	}
	msg := mailerpkg.Message{To: []string{me.Email}, Subject: "[Test] " + p.Subject, HTML: p.HTML, Text: p.Text}
	if err := h.send(r.Context(), "email_test", msg); err != nil {
		httpx.ServerErr(w, http.StatusBadGateway, "sending failed", err)
		return
	}
	testEmailLimit.record(time.Now(), me.ID, ip)
	httpx.JSON(w, http.StatusOK, map[string]string{"to": me.Email})
}

// fill puts a team's and event's names in for {{team}} and {{event}}.
func fill(s, team, event string) string {
	return strings.NewReplacer("{{team}}", team, "{{event}}", event).Replace(s)
}

type themeRequest struct {
	Theme string `json:"theme"`
}

type orgEmailRequest struct {
	Theme    *string `json:"theme"`
	Language *string `json:"language"`
}

// SetOrgEmailTheme sets the theme the caller's organisation sends in,
// and the language of what it sends outside any event. Either may be
// left out.
func (h *Handlers) SetOrgEmailTheme(w http.ResponseWriter, r *http.Request) {
	me, _ := auth.FromContext(r.Context())
	if me.OrgID == nil || me.Role != auth.RoleOrgAdmin {
		httpx.Error(w, http.StatusForbidden, "only an organisation's admins choose its emails' look")
		return
	}
	var req orgEmailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Theme != nil {
		if !emailtpl.Valid(emailtpl.Theme(*req.Theme)) {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "unknown theme", "code": "theme_invalid"})
			return
		}
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE organizations SET email_theme = $2 WHERE id = $1`, *me.OrgID, *req.Theme); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "saving failed", err)
			return
		}
	}
	if req.Language != nil {
		if emailtpl.Lang(*req.Language) != *req.Language {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "emails are written in " + strings.Join(emailtpl.Languages, ", "), "code": "language_invalid"})
			return
		}
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE organizations SET language = $2 WHERE id = $1`, *me.OrgID, *req.Language); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "saving failed", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetOrgEmailTheme is the caller's organisation's theme and language.
func (h *Handlers) GetOrgEmailTheme(w http.ResponseWriter, r *http.Request) {
	me, _ := auth.FromContext(r.Context())
	l := look{Theme: emailtpl.Classic, Lang: "en"}
	if me.OrgID != nil {
		l = h.orgLook(r.Context(), *me.OrgID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"theme": string(l.Theme), "language": l.Lang, "languages": emailtpl.Languages})
}

type languageRequest struct {
	Language string `json:"language"`
}

// SetMyLanguage records the language the caller uses the console in:
// the notifications they get are written in it.
func (h *Handlers) SetMyLanguage(w http.ResponseWriter, r *http.Request) {
	me, _ := auth.FromContext(r.Context())
	var req languageRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE users SET language = $2 WHERE id = $1`, me.ID, emailtpl.Lang(req.Language)); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "saving failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetEventEmailTheme sets an event's own theme; "" sends in the
// organisation's.
func (h *Handlers) SetEventEmailTheme(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFrom(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad event id")
		return
	}
	if _, ok := h.authorizeEvent(w, r, eventID); !ok {
		return
	}
	var req themeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	var v any
	if req.Theme != "" {
		if !emailtpl.Valid(emailtpl.Theme(req.Theme)) {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "unknown theme", "code": "theme_invalid"})
			return
		}
		v = req.Theme
	}
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE events SET email_theme = $2 WHERE id = $1`, eventID, v); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "saving failed", err)
		return
	}
	h.Hub.Publish(ws.EventTopic(eventID), "changed", nil)
	w.WriteHeader(http.StatusNoContent)
}

type sentEmailDTO struct {
	ID      int64     `json:"id"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	Theme   string    `json:"theme"`
	SentBy  string    `json:"sent_by,omitempty"`
	SentAt  time.Time `json:"sent_at"`
	Sent    int       `json:"sent"`
	Skipped int       `json:"skipped"`
}

// ListEventEmails is what was written to an event's teams, newest first,
// with the event's own theme.
func (h *Handlers) ListEventEmails(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFrom(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad event id")
		return
	}
	if _, ok := h.authorizeEvent(w, r, eventID); !ok {
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT m.id, m.subject, m.body, m.theme, COALESCE(u.username, ''), m.sent_at, m.sent, m.skipped
		FROM event_emails m LEFT JOIN users u ON u.id = m.sent_by
		WHERE m.event_id = $1 ORDER BY m.sent_at DESC, m.id DESC LIMIT 100
	`, eventID)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "loading failed", err)
		return
	}
	defer rows.Close()
	out := []sentEmailDTO{}
	for rows.Next() {
		var m sentEmailDTO
		if err := rows.Scan(&m.ID, &m.Subject, &m.Body, &m.Theme, &m.SentBy, &m.SentAt, &m.Sent, &m.Skipped); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "loading failed", err)
			return
		}
		out = append(out, m)
	}
	var own sql.NullString
	_ = h.DB.QueryRowContext(r.Context(), `SELECT email_theme FROM events WHERE id = $1`, eventID).Scan(&own)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"emails":      out,
		"theme":       own.String,
		"theme_used":  string(h.eventLook(r.Context(), eventID).Theme),
		"with_email":  h.teamsWithEmail(r.Context(), eventID),
		"teams_total": h.teamCount(r.Context(), eventID),
	})
}

func (h *Handlers) teamsWithEmail(ctx context.Context, eventID int64) int {
	var n int
	_ = h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE event_id = $1 AND COALESCE(TRIM(email), '') <> ''`, eventID).Scan(&n)
	return n
}

func (h *Handlers) teamCount(ctx context.Context, eventID int64) int {
	var n int
	_ = h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE event_id = $1`, eventID).Scan(&n)
	return n
}

type teamEmailRequest struct {
	Subject string  `json:"subject"`
	Body    string  `json:"body"`
	TeamIDs []int64 `json:"team_ids"`
	// Translations are the email in the adventure's other languages, by
	// language tag: a team playing in one gets it in that language,
	// every other one as written.
	Translations map[string]teamEmailText `json:"translations,omitempty"`
}

// teamEmailText is an arranger's email to teams in one language.
type teamEmailText struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// maxTeamEmail bounds what an arranger writes to teams.
const maxTeamEmail = 20000

// EmailTeams writes to an event's teams — all of them, or those chosen —
// at each team's contact address, in the event's look. {{team}} and
// {{event}} are filled in for each. A team without an address is left
// out and counted.
func (h *Handlers) EmailTeams(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFrom(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad event id")
		return
	}
	if _, ok := h.authorizeEvent(w, r, eventID); !ok {
		return
	}
	var req teamEmailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	req.Subject, req.Body = strings.TrimSpace(req.Subject), strings.TrimSpace(req.Body)
	if req.Subject == "" || req.Body == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "write a subject and a message", "code": "email_empty"})
		return
	}
	if len(req.Subject) > 200 || len(req.Body) > maxTeamEmail {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "the message is too long", "code": "email_too_long"})
		return
	}
	for lang, t := range req.Translations {
		t.Subject, t.Body = strings.TrimSpace(t.Subject), strings.TrimSpace(t.Body)
		switch {
		case !validLanguageTag(lang):
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "a translation needs a language tag", "code": "email_lang"})
			return
		case len(t.Subject) > 200 || len(t.Body) > maxTeamEmail:
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "the message is too long", "code": "email_too_long"})
			return
		case t.Subject == "" || t.Body == "":
			// Half a translation is none: the team gets it as written.
			delete(req.Translations, lang)
		default:
			req.Translations[lang] = t
		}
	}
	if h.MailOutbox == nil && h.Mailer == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "no mailer configured")
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, name, COALESCE(TRIM(email), ''), lang FROM teams WHERE event_id = $1 ORDER BY id`, eventID)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "loading teams failed", err)
		return
	}
	type team struct {
		id                int64
		name, email, lang string
	}
	var teams []team
	chosen := map[int64]bool{}
	for _, id := range req.TeamIDs {
		chosen[id] = true
	}
	for rows.Next() {
		var t team
		if err := rows.Scan(&t.id, &t.name, &t.email, &t.lang); err != nil {
			rows.Close()
			httpx.ServerErr(w, http.StatusInternalServerError, "loading teams failed", err)
			return
		}
		if len(chosen) == 0 || chosen[t.id] {
			teams = append(teams, t)
		}
	}
	rows.Close()
	if len(teams) == 0 {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "no such teams in this event", "code": "teams_none"})
		return
	}

	l := h.eventLook(r.Context(), eventID)
	words := h.eventWords(r.Context(), eventID)
	sent, skipped := 0, 0
	for _, t := range teams {
		if t.email == "" {
			skipped++
			continue
		}
		// In the team's language where the arranger wrote it in that,
		// the email around it too; as written otherwise.
		lt, text := l, teamEmailText{Subject: req.Subject, Body: req.Body}
		if tr, ok := req.Translations[t.lang]; ok && words.speaks(t.lang) {
			lt, text = words.lookIn(l, t.lang), tr
		}
		subject, c := emailtpl.TeamMessage(lt.Lang, fill(text.Subject, t.name, lt.Event), fill(text.Body, t.name, lt.Event), lt.Event, lt.Brand.Name)
		if err := h.send(r.Context(), "team_message", lt.message([]string{t.email}, subject, c)); err != nil {
			logx.Warn("admin.emails.team", "event", eventID, "team", t.id, "err", err.Error())
			skipped++
			continue
		}
		sent++
	}
	me, _ := auth.FromContext(r.Context())
	var id int64
	if err := h.DB.QueryRowContext(r.Context(), `
		INSERT INTO event_emails (event_id, subject, body, theme, sent_by, sent, skipped) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id
	`, eventID, req.Subject, req.Body, string(l.Theme), me.ID, sent, skipped).Scan(&id); err != nil {
		logx.Warn("admin.emails.record", "event", eventID, "err", err.Error())
	}
	activity.Record(r.Context(), h.DB, eventID, nil, activity.UserActor(me.ID), "email.sent", map[string]any{"subject": req.Subject, "sent": sent, "skipped": skipped})
	h.Hub.Publish(ws.EventTopic(eventID), "changed", nil)
	httpx.JSON(w, http.StatusOK, map[string]any{"id": id, "sent": sent, "skipped": skipped})
}

// emailTeamsCancelled tells every team of an event with a contact email
// that the event is cancelled, and returns how many were sent.
// Best effort: a failure is logged, not returned.
func (h *Handlers) emailTeamsCancelled(ctx context.Context, eventID int64) int {
	if h.MailOutbox == nil && h.Mailer == nil {
		return 0
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT id, name, TRIM(email), lang FROM teams WHERE event_id = $1 AND TRIM(COALESCE(email, '')) <> '' ORDER BY id`, eventID)
	if err != nil {
		logx.Warn("admin.emails.cancelled", "event", eventID, "err", err.Error())
		return 0
	}
	type team struct {
		id                int64
		name, email, lang string
	}
	var teams []team
	for rows.Next() {
		var t team
		if err := rows.Scan(&t.id, &t.name, &t.email, &t.lang); err != nil {
			break
		}
		teams = append(teams, t)
	}
	rows.Close()
	l := h.eventLook(ctx, eventID)
	words := h.eventWords(ctx, eventID)
	// The calendar entry the invitations put in calendars, cancelled, in
	// each team's language. The outbox holds no attachments, so with one
	// the mail goes straight out.
	evt, evtErr := icscal.BuildForEvent(ctx, h.DB, h.Lib, eventID, h.eventBase(ctx, eventID))
	cancelFor := func(lang string) []mailerpkg.Attachment {
		if evtErr != nil || h.Mailer == nil {
			return nil
		}
		e := words.calendarIn(evt, l, lang)
		e.Method, e.Status = "CANCEL", "CANCELLED"
		b, err := icscal.Bytes(e)
		if err != nil {
			return nil
		}
		return []mailerpkg.Attachment{{Filename: icscal.Filename(eventID), ContentType: "text/calendar; method=CANCEL; charset=UTF-8", Bytes: b}}
	}
	sent := 0
	for _, t := range teams {
		lt := words.lookIn(l, t.lang)
		subject, c := emailtpl.EventCancelled(lt.Lang, t.name, lt.Event, lt.Brand.Name, lt.Contact)
		msg := lt.message([]string{t.email}, subject, c)
		cancel := cancelFor(t.lang)
		var err error
		if cancel != nil {
			msg.Attachments = cancel
			err = h.Mailer.Send(ctx, msg)
		} else {
			err = h.send(ctx, "event_cancelled", msg)
		}
		if err != nil {
			logx.Warn("admin.emails.cancelled", "event", eventID, "team", t.id, "err", err.Error())
			continue
		}
		sent++
	}
	return sent
}

// The notification emails a designer can turn off.
var notificationKinds = []string{"mention", "step_assigned", "suggestion", "review_requested"}

// wantsEmail reports whether userID gets emails of kind: yes unless
// they turned it off.
func (h *Handlers) wantsEmail(ctx context.Context, userID int64, kind string) bool {
	var on bool
	err := h.DB.QueryRowContext(ctx, `SELECT enabled FROM user_email_prefs WHERE user_id = $1 AND kind = $2`, userID, kind).Scan(&on)
	return err != nil || on
}

// GetEmailPrefs is which notification emails the caller gets.
func (h *Handlers) GetEmailPrefs(w http.ResponseWriter, r *http.Request) {
	me, _ := auth.FromContext(r.Context())
	out := map[string]bool{}
	for _, k := range notificationKinds {
		out[k] = h.wantsEmail(r.Context(), me.ID, k)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"prefs": out})
}

// SetEmailPrefs switches notification emails on or off.
func (h *Handlers) SetEmailPrefs(w http.ResponseWriter, r *http.Request) {
	me, _ := auth.FromContext(r.Context())
	var req map[string]bool
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	for k, on := range req {
		known := false
		for _, x := range notificationKinds {
			known = known || x == k
		}
		if !known {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "unknown kind of email: " + k, "code": "kind_invalid"})
			return
		}
		if _, err := h.DB.ExecContext(r.Context(), `
			INSERT INTO user_email_prefs (user_id, kind, enabled) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, kind) DO UPDATE SET enabled = EXCLUDED.enabled
		`, me.ID, k, on); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "saving failed", err)
			return
		}
	}
	h.GetEmailPrefs(w, r)
}
