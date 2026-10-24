// Package icscal builds RFC 5545 iCalendar (.ics) bytes for a
// Zonventure event. Two callers today:
//
//   - internal/admin/calendar.go — serves the .ics directly from
//     /api/admin/events/{id}/invite.ics for download.
//   - internal/player/recover.go — attaches the .ics to magic-link
//     recovery emails so participants get a real calendar entry.
//
// Lives in its own package because both callers need the same
// SQL+adventure-library lookup; duplicating it would risk drift
// between "what the download shows" and "what's in the email
// attachment".
package icscal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/klahr/zonventure/internal/adventure"
	"github.com/klahr/zonventure/internal/sitename"
)

// ErrNoSchedule indicates the event has neither a scheduled_at nor
// a started_at, or is self-guided (public_for_sale) — there's nothing
// meaningful to put on a calendar.
// Callers map this to a 400 (or just skip the attachment).
var ErrNoSchedule = errors.New("icscal: event has no scheduled time")

// Event is the value the wire-format Write consumes. Pure data so
// Write can be unit-tested against fixtures without a database.
type Event struct {
	UID            string
	GeneratedAt    time.Time
	SequenceN      int
	StartAt        time.Time // must be in UTC
	EndAt          time.Time // must be in UTC, > StartAt
	Title          string
	OrgName        string
	OrganizerEmail string
	OrganizerName  string
	LocationLabel  string // free-form; coords or place name
	Description    string // plain text; line breaks become \n in the unfolded value
	JoinURL        string // shown in DESCRIPTION + emitted as URL property
	Status         string // CONFIRMED / TENTATIVE / CANCELLED
	// Method is the iTIP method: PUBLISH when empty; CANCEL takes the
	// entry off the calendars that hold it.
	Method string
	// JoinLabel introduces JoinURL in DESCRIPTION: "Join link:" when
	// empty, else what Labelled set, in the invitee's language.
	JoinLabel string
}

// BuildForEvent loads the event, its adventure, and the org name,
// fills out the Event struct, and returns it ready for Write. Pure
// reads — never mutates state. Returns ErrNoSchedule when the
// event has no schedulable time yet.
//
// publicURL is used for both the UID host-part and the JoinURL.
// Pass an empty string when no operator-supplied URL is configured;
// the helper falls back to "zonventure" for the UID and skips the
// URL property entirely.
func BuildForEvent(ctx context.Context, db *sql.DB, lib *adventure.Library, eventID int64, publicURL string) (Event, error) {
	var (
		out      Event
		slug     string
		name     string
		state    string
		schedRaw sql.NullString
		startRaw sql.NullString
		endRaw   sql.NullString
		creator  sql.NullString
		orgName  string
		forSale  bool
		seq      int
		meetAt   string
	)
	if err := db.QueryRowContext(ctx, `
		SELECT e.adventure_slug, e.name, e.state, e.scheduled_at, e.started_at,
		       e.completed_at,
		       COALESCE(u.email, ''),
		       o.name, e.public_for_sale, e.ics_sequence, e.meeting_place
		FROM events e
		JOIN organizations o ON o.id = e.org_id
		LEFT JOIN users u ON u.id = e.created_by_user_id
		WHERE e.id = $1
	`, eventID).Scan(&slug, &name, &state, &schedRaw, &startRaw, &endRaw, &creator, &orgName, &forSale, &seq, &meetAt); err != nil {
		return out, err
	}
	adv := lib.Get(slug)
	if adv == nil {
		return out, fmt.Errorf("icscal: adventure %q not loaded", slug)
	}
	startTime, hasStart := PickStart(state, schedRaw.String, startRaw.String)
	// A self-guided event is played whenever a team likes: its start
	// is no date to put on a calendar.
	if !hasStart || forSale {
		return out, ErrNoSchedule
	}
	endTime, hasEnd := ParseTimestamp(endRaw.String)
	if !hasEnd || !endTime.After(startTime) {
		// Two-hour default matches a typical corporate team-build
		// window; arrangers running longer events can edit the
		// entry their calendar app creates from this invite.
		endTime = startTime.Add(2 * time.Hour)
	}

	out.UID = fmt.Sprintf("event-%d@%s", eventID, hostFromURL(publicURL))
	out.GeneratedAt = time.Now().UTC()
	out.SequenceN = seq
	out.StartAt = startTime.UTC()
	out.EndAt = endTime.UTC()
	// The event's own name ("Acme team day") is what the invitee knows
	// it by; the adventure's title stands in when it has none.
	out.Title = strings.TrimSpace(name)
	if out.Title == "" {
		out.Title = adv.Meta.Title
	}
	if out.Title == "" {
		out.Title = sitename.Name() + " event"
	}
	out.OrgName = orgName
	out.OrganizerEmail = strings.TrimSpace(creator.String)
	out.OrganizerName = orgName
	// The organiser's meeting place says more than the start's
	// coordinates.
	out.LocationLabel = strings.TrimSpace(meetAt)
	if out.LocationLabel == "" {
		out.LocationLabel = pickLocation(adv)
	}
	out.JoinURL = joinURL(publicURL)
	out.Description = buildDescription(out.Title, adv.Meta.Description, out.JoinURL, orgName)
	// A scheduled event is on: the invitee should see it as confirmed,
	// not "maybe". Only a cancelled one says otherwise.
	out.Status = "CONFIRMED"
	if state == "cancelled" {
		out.Status = "CANCELLED"
	}
	return out, nil
}

// WithJoinURL points the entry, and the link in its description, at u:
// one team's own page instead of the generic join page.
func (e Event) WithJoinURL(u string) Event {
	if e.JoinURL != "" {
		e.Description = strings.Replace(e.Description, e.joinLabel()+" "+e.JoinURL, e.joinLabel()+" "+u, 1)
	} else {
		e.Description += "\n" + e.joinLabel() + " " + u
	}
	e.JoinURL = u
	return e
}

// Labelled introduces the entry's link with label, "Join link:" in the
// invitee's language; an empty one leaves it as it was.
func (e Event) Labelled(label string) Event {
	if label = strings.TrimSpace(label); label == "" {
		return e
	}
	if e.JoinURL != "" {
		e.Description = strings.Replace(e.Description, e.joinLabel()+" "+e.JoinURL, label+" "+e.JoinURL, 1)
	}
	e.JoinLabel = label
	return e
}

func (e Event) joinLabel() string {
	if e.JoinLabel != "" {
		return e.JoinLabel
	}
	return "Join link:"
}

// Named is the entry called title and placed at location: one team's,
// in the language it plays in. An empty one leaves either as it was.
func (e Event) Named(title, location string) Event {
	if title = strings.TrimSpace(title); title != "" && title != e.Title {
		e.Description = strings.Replace(e.Description, e.Title, title, 1)
		e.Title = title
	}
	if location = strings.TrimSpace(location); location != "" {
		e.LocationLabel = location
	}
	return e
}

// Filename returns a stable filename for the .ics attachment.
// Stable across re-sends so a recipient's calendar app updates the
// existing entry on import rather than creating a duplicate.
func Filename(eventID int64) string {
	return fmt.Sprintf("event-%d-invite.ics", eventID)
}

// PickStart picks the most authoritative start time available for
// the calendar entry. State-aware so a running event's actual start
// wins over a stale scheduled stamp.
func PickStart(state, schedRaw, startedRaw string) (time.Time, bool) {
	switch state {
	case "running", "paused", "completed":
		if t, ok := ParseTimestamp(startedRaw); ok {
			return t, true
		}
	}
	if t, ok := ParseTimestamp(schedRaw); ok {
		return t, true
	}
	if t, ok := ParseTimestamp(startedRaw); ok {
		return t, true
	}
	return time.Time{}, false
}

// ParseTimestamp tolerates both Postgres' "YYYY-MM-DD HH:MM:SS"
// shape (what now_text() emits) and full RFC3339 strings.
func ParseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// pickLocation surfaces the adventure's starting location as a
// "59.32931, 18.06869" string. iCalendar's LOCATION is free-form, so
// coords work fine; a future iteration could reverse-geocode for a
// human label. Returns an empty string when no start is set.
func pickLocation(adv *adventure.Adventure) string {
	if adv == nil || !adv.Start.HasCoords() {
		return ""
	}
	return fmt.Sprintf("%.5f, %.5f", *adv.Start.Lat, *adv.Start.Lng)
}

// buildDescription assembles the multi-line plain-text description.
// Newlines come out as "\n" in the unfolded property value.
func buildDescription(title, desc, joinURL, orgName string) string {
	var b strings.Builder
	if orgName != "" {
		fmt.Fprintf(&b, "%s — %s\n", orgName, title)
	} else {
		b.WriteString(title)
		b.WriteString("\n")
	}
	if d := strings.TrimSpace(desc); d != "" {
		b.WriteString("\n")
		b.WriteString(d)
		b.WriteString("\n")
	}
	if joinURL != "" {
		b.WriteString("\nJoin link: ")
		b.WriteString(joinURL)
	}
	return b.String()
}

// hostFromURL returns the host portion of the operator's PublicURL
// — used in UID generation. Falls back to "zonventure" when
// PublicURL is empty so the UID is still well-formed.
func hostFromURL(publicURL string) string {
	pu := strings.TrimSpace(publicURL)
	if pu == "" {
		return "zonventure"
	}
	pu = strings.TrimPrefix(pu, "https://")
	pu = strings.TrimPrefix(pu, "http://")
	if i := strings.IndexAny(pu, "/?#"); i >= 0 {
		pu = pu[:i]
	}
	if pu == "" {
		return "zonventure"
	}
	return pu
}

// joinURL builds the public play surface URL from the operator's
// configured PublicURL. Empty when PublicURL is unset — caller
// should drop the URL property in that case.
func joinURL(publicURL string) string {
	pu := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if pu == "" {
		return ""
	}
	return pu + "/play"
}

// Write emits the VCALENDAR/VEVENT to w. Pure rendering; safe to
// call from a request handler or an email-build path.
//
// Wire format follows RFC 5545:
//   - CRLF line endings
//   - lines >75 octets folded onto continuation lines starting with a space
//   - property values escape backslash, semicolon, comma, and newline
func Write(w io.Writer, e Event) error {
	var b strings.Builder
	line(&b, "BEGIN:VCALENDAR")
	line(&b, "VERSION:2.0")
	line(&b, "PRODID:-//Zonventure//Event Invite//EN")
	line(&b, "CALSCALE:GREGORIAN")
	method := "PUBLISH"
	if m := strings.TrimSpace(e.Method); m != "" {
		method = m
	}
	line(&b, "METHOD:"+method)
	line(&b, "BEGIN:VEVENT")
	// UID is server-built ("event-N@host") in current callers — but
	// run it through Escape regardless so a future hostFromURL change
	// (or any later caller passing a user-derived value) can't break
	// out of the property line via a stray comma / semicolon / CRLF.
	line(&b, "UID:"+Escape(e.UID))
	line(&b, "DTSTAMP:"+formatTime(e.GeneratedAt))
	line(&b, "DTSTART:"+formatTime(e.StartAt))
	line(&b, "DTEND:"+formatTime(e.EndAt))
	line(&b, "SEQUENCE:"+fmt.Sprintf("%d", e.SequenceN))
	if status := strings.TrimSpace(e.Status); status != "" {
		line(&b, "STATUS:"+status)
	}
	line(&b, "SUMMARY:"+Escape(e.Title))
	if loc := strings.TrimSpace(e.LocationLabel); loc != "" {
		line(&b, "LOCATION:"+Escape(loc))
	}
	if desc := strings.TrimSpace(e.Description); desc != "" {
		line(&b, "DESCRIPTION:"+Escape(desc))
	}
	if e.JoinURL != "" {
		line(&b, "URL:"+Escape(e.JoinURL))
	}
	if e.OrganizerEmail != "" {
		// Belt-and-braces escape on the email too — signup validation
		// rejects CRLF in addresses today, but a regression there
		// would otherwise let a crafted address inject extra ICS
		// properties past the ORGANIZER line.
		email := Escape(e.OrganizerEmail)
		cn := Escape(strings.TrimSpace(e.OrganizerName))
		if cn != "" {
			line(&b, "ORGANIZER;CN="+paramQuote(cn)+":mailto:"+email)
		} else {
			line(&b, "ORGANIZER:mailto:"+email)
		}
	}
	line(&b, "END:VEVENT")
	line(&b, "END:VCALENDAR")
	_, err := io.WriteString(w, b.String())
	return err
}

// Bytes is a convenience wrapper that returns the rendered .ics as
// a byte slice — useful for callers that want to attach the file
// to an email rather than write it to an HTTP response.
func Bytes(e Event) ([]byte, error) {
	var b strings.Builder
	if err := Write(&b, e); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// line appends one logical line to b, applying RFC 5545's 75-octet
// folding rule.
func line(b *strings.Builder, s string) {
	const maxOctets = 75
	const crlf = "\r\n"
	if len(s) <= maxOctets {
		b.WriteString(s)
		b.WriteString(crlf)
		return
	}
	b.WriteString(s[:maxOctets])
	b.WriteString(crlf)
	rest := s[maxOctets:]
	for len(rest) > maxOctets-1 {
		b.WriteString(" ")
		b.WriteString(rest[:maxOctets-1])
		b.WriteString(crlf)
		rest = rest[maxOctets-1:]
	}
	if len(rest) > 0 {
		b.WriteString(" ")
		b.WriteString(rest)
		b.WriteString(crlf)
	}
}

// formatTime returns the "YYYYMMDDTHHMMSSZ" UTC form RFC 5545 uses
// for DTSTAMP / DTSTART / DTEND.
func formatTime(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// Escape applies RFC 5545 §3.3.11 text escaping. Values may not
// contain raw backslash, semicolon, comma, or newline; each must
// be replaced with the listed sequence. Other C0 control bytes
// (NUL, BEL, etc.) are stripped — RFC 5545 limits property values
// to printable + folding-whitespace, and at least Outlook chokes
// on a NUL inside SUMMARY.
func Escape(s string) string {
	s = stripControl(s)
	r := strings.NewReplacer(
		`\`, `\\`,
		";", `\;`,
		",", `\,`,
		"\r\n", `\n`,
		"\n", `\n`,
		"\r", `\n`,
	)
	return r.Replace(s)
}

// stripControl drops C0 control bytes other than CR/LF/TAB. CR/LF are
// handled by the escape replacer above; TAB is legal as folding ws.
func stripControl(s string) string {
	if !strings.ContainsAny(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0B\x0C\x0E\x0F\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1A\x1B\x1C\x1D\x1E\x1F\x7F") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 0x20 && c != '\t' && c != '\r' && c != '\n') || c == 0x7F {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// paramQuote wraps a parameter value in double quotes when it
// contains characters (`:`, `;`, `,`, or whitespace) that would
// otherwise need escaping.
func paramQuote(s string) string {
	if strings.ContainsAny(s, `:;,`) || strings.ContainsAny(s, " \t") {
		return `"` + strings.ReplaceAll(s, `"`, "") + `"`
	}
	return s
}
