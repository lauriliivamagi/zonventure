package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/klahr/zonventure/internal/admin"
	"github.com/klahr/zonventure/internal/adventure"
	"github.com/klahr/zonventure/internal/auth"
	"github.com/klahr/zonventure/internal/dbtest"
)

// An adventure played in Estonian sends its teams Estonian invitations,
// whatever the organisation's own language: the email, its start time
// and its calendar entry.
func TestSendTeamInvitation_Estonian(t *testing.T) {
	t.Parallel()
	db, cleanup := dbtest.Open(t)
	defer cleanup()

	var orgID, userID, teamID int64
	if err := db.QueryRow(`INSERT INTO organizations(name) VALUES ('Acme') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO users(username, email, email_verified) VALUES ('boss', 'boss@acme.com', 1) RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO org_memberships(user_id, org_id, role) VALUES ($1, $2, 'org_admin')`, userID, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO adventures(slug, title, org_id, language) VALUES ('matk', 'Matk', $1, 'et')`, orgID); err != nil {
		t.Fatal(err)
	}
	var eventID int64
	if err := db.QueryRow(`
		INSERT INTO events(adventure_slug, org_id, created_by_user_id, state, scheduled_at, live_token, name, meeting_place)
		VALUES ('matk', $1, $2, 'scheduled', '2026-05-02 08:30:00', 'lt-' || md5(random()::text), 'Elsa sünnipäev', 'Raekoja plats')
		RETURNING id
	`, orgID, userID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	var teamToken string
	if err := db.QueryRow(`
		INSERT INTO teams(event_id, name, email, token, recap_token) VALUES ($1, 'Kajakad', 'kajakad@acme.com', gen_random_uuid()::text, gen_random_uuid()::text)
		RETURNING id, token
	`, eventID).Scan(&teamID, &teamToken); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO player_slots(team_id, name, token) VALUES ($1, 'Anna', 'tok-anna')`, teamID); err != nil {
		t.Fatal(err)
	}

	mail := &fakeMailer{}
	h := &admin.Handlers{DB: db, Lib: adventure.New(db), Mailer: mail, PublicURL: "https://zv.example.com"}
	r := chi.NewRouter()
	r.Use(injectUser(auth.User{ID: userID, Role: auth.RoleOrgAdmin, OrgID: &orgID}))
	r.Post("/api/admin/teams/{teamID}/invitations/send", h.SendTeamInvitation)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/teams/"+itoa64(teamID)+"/invitations/send", strings.NewReader("{}")))
	if w.Code != http.StatusOK || len(mail.messages) != 1 {
		t.Fatalf("status = %d, %d messages; body = %s", w.Code, len(mail.messages), w.Body.String())
	}
	got := mail.messages[0]
	if got.Subject != "Acme: sinu võistkonna lingid" {
		t.Errorf("subject = %q", got.Subject)
	}
	// The organisation's time zone in tests is Europe/Stockholm.
	for _, want := range []string{
		"Oled oodatud: Elsa sünnipäev",
		"Algus: laupäev, 2. mai 2026 kell 10.30 (CEST).",
		"Kogunemiskoht: Raekoja plats",
		"Sinu võistkond Kajakad on mänguks valmis.",
		"Said selle kirja, sest sinu võistkond osaleb üritusel Elsa sünnipäev, mille korraldaja on Acme.",
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, got.Text)
		}
	}
	if !strings.Contains(got.HTML, `lang="et"`) || !strings.Contains(got.HTML, "Acme · saadetud keskkonnast") {
		t.Errorf("HTML is not Estonian:\n%s", got.HTML)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
	ics := strings.ReplaceAll(string(got.Attachments[0].Bytes), "\r\n ", "")
	if !strings.Contains(ics, `Liitumislink: https://zv.example.com/team/`+teamToken) || strings.Contains(ics, "Join link") {
		t.Errorf("calendar entry's link is not introduced in Estonian:\n%s", ics)
	}
}
