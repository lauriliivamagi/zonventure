package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/klahr/zonventure/internal/adventure"
	"github.com/klahr/zonventure/internal/dbtest"
	"github.com/klahr/zonventure/internal/public"
)

// TestStorePages renders the store's event pages from one database
// shared by the subtests: one org, "Acme", and its "demo" adventure;
// each subtest adds its own events (and adventures).
func TestStorePages(t *testing.T) {
	db, cleanup := dbtest.Open(t)
	defer cleanup()
	var orgID int64
	if err := db.QueryRow(`INSERT INTO organizations(name) VALUES ('Acme') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO adventures(slug, title, org_id, start_label) VALUES ('demo', 'Demo Adv', $1, 'By the fountain')
	`, orgID); err != nil {
		t.Fatal(err)
	}

	// Drives the per-event detail renderer against a real DB row and
	// asserts the embedded template has no leftover placeholders.
	// Mirrors landing_test's posture so a missing substitution becomes a
	// test failure rather than a silent "{{TITLE}} on the live page"
	// defect.
	t.Run("render event page", func(t *testing.T) {
		var eventID int64
		if err := db.QueryRow(`
			INSERT INTO events(adventure_slug, org_id, state, live_token, name,
			                   public_for_sale, ticket_price_cents, ticket_currency, ticket_team_size, public_blurb)
			VALUES ('demo', $1, 'running', 'lt-' || md5(random()::text), 'Always-on demo',
			        TRUE, 2500, 'eur', 5, 'A two-hour outdoor adventure.')
			RETURNING id
		`, orgID).Scan(&eventID); err != nil {
			t.Fatal(err)
		}

		dbtest.ApproveListings(t, db)
		r := chi.NewRouter()
		r.Get("/store/event/{event_id}", func(w http.ResponseWriter, req *http.Request) {
			renderStoreEventPage(w, req, db, "https://zv.example.com", false)
		})

		req := httptest.NewRequest("GET", "/store/event/"+itoa64(eventID), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "{{") {
			idx := strings.Index(body, "{{")
			end := idx + 50
			if end > len(body) {
				end = len(body)
			}
			t.Fatalf("leftover placeholder at %d: %q", idx, body[idx:end])
		}
		// The canonical URL moved under /o/{slug}/ when per-org store
		// pages landed (migration 0051). The trigger auto-derives slug
		// "acme" from the seeded org name, so the absolute URL is
		// `https://zv.example.com/o/acme/store/event/{id}` rather than
		// the legacy `/store/event/` path.
		for _, want := range []string{
			"Demo Adv",                      // adventure title
			"Acme",                          // org name
			"A two-hour outdoor adventure.", // blurb
			"https://zv.example.com/o/acme/store/event/", // canonical URL
			"€25", // formatted price
			"Payment by Stripe.",
			"By the fountain", // meeting point
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		// A meeting point is set, so its tile shows.
		hiddenMeetingPoint := `<div class="meta-tile" hidden>
          <div class="k">Meeting point</div>`
		if strings.Contains(body, hiddenMeetingPoint) {
			t.Error("meeting point tile hidden though the adventure has one")
		}
		// Without one the tile goes, rather than showing empty.
		if _, err := db.Exec(`UPDATE adventures SET start_label = '' WHERE slug = 'demo'`); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`UPDATE adventures SET start_label = 'By the fountain' WHERE slug = 'demo'`) //nolint:errcheck
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/store/event/"+itoa64(eventID), nil))
		if !strings.Contains(rec.Body.String(), hiddenMeetingPoint) {
			t.Error("empty meeting point tile not hidden")
		}
		// Cache headers + content type are part of the contract.
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("Content-Type = %q, want text/html…", got)
		}
	})

	// Asserts that a 404 is returned for events that exist but aren't
	// currently for sale — stale link from a previously-listed event
	// must not leak the row's data.
	t.Run("404 on unlisted", func(t *testing.T) {
		var eventID int64
		if err := db.QueryRow(`
			INSERT INTO events(adventure_slug, org_id, state, live_token, name,
			                   public_for_sale, ticket_price_cents, ticket_currency, ticket_team_size)
			VALUES ('demo', $1, 'running', 'lt-' || md5(random()::text), 'Hidden',
			        FALSE, 2500, 'eur', 5)
			RETURNING id
		`, orgID).Scan(&eventID); err != nil {
			t.Fatal(err)
		}

		r := chi.NewRouter()
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404"))
		})
		r.Get("/store/event/{event_id}", func(w http.ResponseWriter, req *http.Request) {
			renderStoreEventPage(w, req, db, "", false)
		})

		req := httptest.NewRequest("GET", "/store/event/"+itoa64(eventID), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	// A listed event that is paused says it isn't on sale right now,
	// not that the page never existed.
	t.Run("off sale while paused", func(t *testing.T) {
		var eventID int64
		if err := db.QueryRow(`
			INSERT INTO events(adventure_slug, org_id, state, live_token, name,
			                   public_for_sale, ticket_price_cents, ticket_currency, ticket_team_size)
			VALUES ('demo', $1, 'running', 'lt-' || md5(random()::text), 'Paused one',
			        TRUE, 2500, 'eur', 5)
			RETURNING id
		`, orgID).Scan(&eventID); err != nil {
			t.Fatal(err)
		}
		dbtest.ApproveListings(t, db)
		if _, err := db.Exec(`UPDATE events SET state = 'paused' WHERE id = $1`, eventID); err != nil {
			t.Fatal(err)
		}
		r := chi.NewRouter()
		r.Get("/store/event/{event_id}", func(w http.ResponseWriter, req *http.Request) {
			renderStoreEventPage(w, req, db, "", false)
		})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/store/event/"+itoa64(eventID), nil))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), landingLocales["en"].StoreEventNotForSale) {
			t.Fatalf("status = %d, want 404 saying it isn't on sale; body = %s", rec.Code, rec.Body.String())
		}
	})

	// An adventure's icon shows on its store card and beside the title
	// on its event page, and is the link preview when the event has no
	// cover.
	t.Run("shows adventure icon", func(t *testing.T) {
		if _, err := db.Exec(`
			INSERT INTO adventures(slug, title, org_id, avatar_mime_type, avatar_size_bytes, avatar_content, area, facts, access_note)
			VALUES ('iconic', 'Iconic', $1, 'image/png', 1, '\x89'::bytea, 'Göteborg · Slottsskogen',
			        '{"minutes": 90, "distance_m": 2400, "min_age": 10, "difficulty": "easy", "access": "some_steps"}', 'Stairs at the fourth stop'),
			       ('plain', 'Plain', $1, NULL, NULL, NULL, '', '{}', '')
		`, orgID); err != nil {
			t.Fatal(err)
		}
		ids := map[string]int64{}
		for _, slug := range []string{"iconic", "plain"} {
			var id int64
			if err := db.QueryRow(`
				INSERT INTO events(adventure_slug, org_id, state, live_token, public_for_sale, ticket_price_cents, ticket_currency, ticket_team_size)
				VALUES ($1, $2, 'running', 'lt-' || md5(random()::text), TRUE, 2500, 'eur', 4) RETURNING id
			`, slug, orgID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids[slug] = id
		}

		dbtest.ApproveListings(t, db)
		r := chi.NewRouter()
		r.Get("/store/event/{event_id}", func(w http.ResponseWriter, req *http.Request) {
			renderStoreEventPage(w, req, db, "https://zv.example.com", false)
		})
		page := func(slug string) string {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", "/store/event/"+itoa64(ids[slug]), nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d", slug, rec.Code)
			}
			return rec.Body.String()
		}
		if body := page("iconic"); !strings.Contains(body, `src="/adventure-assets/avatar/iconic"`) || !strings.Contains(body, `data-has="true"`) ||
			!strings.Contains(body, `content="https://zv.example.com/adventure-assets/avatar/iconic"`) {
			t.Errorf("the icon is missing from the event page or its preview")
		}
		en := landingLocales["en"]
		if body := page("iconic"); !strings.Contains(body, ">1.5 h<") || !strings.Contains(body, ">2.4 km<") ||
			!strings.Contains(body, ">"+en.StoreFactEasy+"<") || !strings.Contains(body, ">"+en.StoreFactSomeSteps+"<") ||
			!strings.Contains(body, ">Stairs at the fourth stop<") {
			t.Errorf("the event page is missing the adventure's facts")
		}
		if body := page("plain"); !strings.Contains(body, `data-has="false"`) || strings.Contains(body, "/adventure-assets/avatar/") {
			t.Errorf("an adventure without an icon shows none")
		}
		if body := page("plain"); strings.Contains(body, "{{") || strings.Count(body, `class="meta-tile" hidden`) < 5 {
			t.Errorf("an adventure without facts shows their tiles")
		}

		rec := httptest.NewRecorder()
		lr := chi.NewRouter()
		lr.Get("/api/o/{slug}/store/items", (&public.Handlers{DB: db, Lib: adventure.New(db), Payments: true}).StoreListByOrg)
		lr.ServeHTTP(rec, httptest.NewRequest("GET", "/api/o/acme/store/items", nil))
		var list struct {
			Items []public.StoreItemDTO `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		icons, areas := map[string]string{}, map[string]string{}
		facts := map[string]adventure.Facts{}
		for _, it := range list.Items {
			icons[it.AdventureSlug] = it.AdventureIconURL
			areas[it.AdventureSlug] = it.Area
			facts[it.AdventureSlug] = it.Facts
		}
		if facts["iconic"].Minutes != 90 || facts["iconic"].Access != "some_steps" || facts["plain"] != (adventure.Facts{}) {
			t.Errorf("store items' facts = %v", facts)
		}
		if icons["iconic"] != "/adventure-assets/avatar/iconic" || icons["plain"] != "" {
			t.Errorf("store items' icons = %v", icons)
		}
		if areas["iconic"] != "Göteborg · Slottsskogen" || areas["plain"] != "" {
			t.Errorf("store items' areas = %v", areas)
		}
	})
}

// TestFormatStorePrice covers the currency-aware formatter so a
// future tweak to symbol placement or zero-cents stripping can't
// silently break the hero copy.
func TestFormatStorePrice(t *testing.T) {
	cases := []struct {
		cents    int64
		currency string
		want     string
	}{
		{2500, "eur", "€25"},
		{2599, "eur", "€25.99"},
		{2500, "usd", "$25"},
		{1000, "gbp", "£10"},
		{50000, "sek", "500 kr"},
		{2500, "xyz", "25 XYZ"},
		{0, "eur", ""},
	}
	for _, c := range cases {
		t.Run(c.currency, func(t *testing.T) {
			got := formatStorePrice(c.cents, c.currency)
			if got != c.want {
				t.Errorf("formatStorePrice(%d, %q) = %q, want %q",
					c.cents, c.currency, got, c.want)
			}
		})
	}
}

// TestBuildAbsoluteURL covers the publicURL-or-relative split. An
// empty publicURL must return the path verbatim so the page still
// works in dev / before the operator has set ZV_PUBLIC_URL.
func TestBuildAbsoluteURL(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"https://zv.example.com", "/store/event/1", "https://zv.example.com/store/event/1"},
		{"https://zv.example.com/", "/store/event/1", "https://zv.example.com/store/event/1"},
		{"", "/store/event/1", "/store/event/1"},
	}
	for _, c := range cases {
		got := buildAbsoluteURL(c.base, c.path)
		if got != c.want {
			t.Errorf("buildAbsoluteURL(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

// TestDirectionsURL: the ticket page's starting point opens Google
// Maps' directions to it, and an adventure without one has no link.
func TestDirectionsURL(t *testing.T) {
	at := func(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
	if got, want := directionsURL(at(58.3797831), at(26.722095)), "https://www.google.com/maps/dir/?api=1&destination=58.379783,26.722095"; got != want {
		t.Errorf("directionsURL = %q, want %q", got, want)
	}
	if got := directionsURL(sql.NullFloat64{}, at(26.722095)); got != "" {
		t.Errorf("directionsURL without a latitude = %q, want none", got)
	}
}

func itoa64(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
