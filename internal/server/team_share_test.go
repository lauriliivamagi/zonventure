package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// getRaw is a sibling of client.do that leaves the response body
// untouched — used when we want to inspect an HTML body, not JSON.
func getRaw(t *testing.T, c *client, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", c.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestTeamShare_PageRendersSlots(t *testing.T) {
	// End-to-end: CreateTeam mints the team a share token; the captain
	// opens /team/{token} with no session and sees a page that names the
	// team, the adventure, and links to each slot's magic-link URL. An
	// unknown token is a 404.
	ts, assignmentID, cleanup := newTestServer(t)
	defer cleanup()

	arr, _, _, eventID := setupRun(t, ts, assignmentID)
	var detail struct {
		Teams []struct {
			Token   string `json:"token"`
			Members []struct {
				Name  string `json:"name"`
				Token string `json:"token"`
			} `json:"members"`
		} `json:"teams"`
	}
	if s := arr.do("GET", "/api/admin/events/"+itoa(eventID), nil, &detail); s != 200 {
		t.Fatalf("run detail = %d", s)
	}
	if len(detail.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(detail.Teams))
	}
	team := detail.Teams[0]
	if team.Token == "" {
		t.Fatalf("team.token is empty — CreateTeam didn't mint one")
	}

	// Unauthenticated fetch of /team/{token} — the captain gets the URL
	// from the arranger and has no session of their own.
	anon := newClient(t, ts)
	status, body := getRaw(t, anon, "/team/"+team.Token)
	if status != 200 {
		t.Fatalf("team page status = %d", status)
	}
	// Adventure title ("Demo: First Steps") must be on the page.
	if !strings.Contains(body, "Demo") {
		t.Errorf("team page missing adventure title")
	}
	// Team name ("Team Alpha") too.
	if !strings.Contains(body, "Team Alpha") {
		t.Errorf("team page missing team name")
	}
	// Each slot's magic-link URL is embedded as an <a href>.
	for _, m := range team.Members {
		want := `/play?t=` + m.Token
		if !strings.Contains(body, want) {
			t.Errorf("team page missing link for %s", m.Name)
		}
	}

	// The page speaks the visitor's language, not the adventure's.
	req, _ := http.NewRequest("GET", ts.URL+"/team/"+team.Token, nil)
	req.Header.Set("Accept-Language", "sv-SE,sv;q=0.9")
	if resp, err := anon.c.Do(req); err != nil {
		t.Fatal(err)
	} else {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(b), `<html lang="sv">`) {
			t.Errorf("a Swedish browser should get the team page in Swedish")
		}
	}

	// This fork's guests are in Estonia: an Estonian phone reads it in
	// Estonian.
	req, _ = http.NewRequest("GET", ts.URL+"/team/"+team.Token, nil)
	req.Header.Set("Accept-Language", "et-EE,et;q=0.9,en;q=0.8")
	if resp, err := anon.c.Do(req); err != nil {
		t.Fatal(err)
	} else {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(b), `<html lang="et">`) || !strings.Contains(string(b), "Mine sisu juurde") {
			t.Errorf("an Estonian browser should get the team page in Estonian")
		}
	}

	if status, _ := getRaw(t, anon, "/team/doesnotexist"); status != 404 {
		t.Errorf("unknown team token = %d, want 404", status)
	}
}
