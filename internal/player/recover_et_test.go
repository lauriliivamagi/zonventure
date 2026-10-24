package player

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klahr/zonventure/internal/adventure"
	"github.com/klahr/zonventure/internal/dbtest"
)

// Someone asking for their links from an Estonian phone gets them in
// Estonian, calendar entries too.
func TestRecoverLink_Estonian(t *testing.T) {
	db, cleanup := dbtest.Open(t)
	defer cleanup()
	var orgID, eventID, teamID int64
	if err := db.QueryRow(`INSERT INTO organizations(name) VALUES ('Acme') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO adventures(slug, title, org_id, language) VALUES ('matk', 'Matk', $1, 'et')`, orgID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		INSERT INTO events(adventure_slug, org_id, state, scheduled_at, live_token, name)
		VALUES ('matk', $1, 'scheduled', '2026-05-02 08:30:00', 'lt-recover-et', 'Elsa sünnipäev')
		RETURNING id
	`, orgID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		INSERT INTO teams(event_id, name, email, token, recap_token)
		VALUES ($1, 'Kajakad', 'kajakad@acme.test', 'team-tok-et', 'recap-tok-et') RETURNING id
	`, eventID).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO player_slots(team_id, name, token) VALUES ($1, 'Anna', 'slot-tok-et')`, teamID); err != nil {
		t.Fatal(err)
	}

	mail := &recoverMailer{}
	h := &Handlers{DB: db, Lib: adventure.New(db), Mailer: mail, PublicURL: "https://zv.test"}
	req := httptest.NewRequest("POST", "/api/player/recover-link", strings.NewReader(`{"email":"kajakad@acme.test"}`))
	req.Header.Set("Accept-Language", "et-EE,et;q=0.9,en;q=0.8")
	w := httptest.NewRecorder()
	h.RecoverLink(w, req)
	if w.Code != http.StatusNoContent || len(mail.sent) != 1 {
		t.Fatalf("status = %d, %d mails", w.Code, len(mail.sent))
	}
	got := mail.sent[0]
	if got.Subject != "Acme: sinu mängulingid" || !strings.Contains(got.Text, "- Anna, üritus Elsa sünnipäev, võistkond Kajakad: https://zv.test/play?t=slot-tok-et") {
		t.Errorf("mail = %q\n%s", got.Subject, got.Text)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
	if ics := strings.ReplaceAll(string(got.Attachments[0].Bytes), "\r\n ", ""); !strings.Contains(ics, "Liitumislink: https://zv.test/play") {
		t.Errorf("calendar entry's link is not introduced in Estonian:\n%s", ics)
	}
}
