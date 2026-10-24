package admin

import (
	"testing"
	"time"

	"github.com/klahr/zonventure/internal/icscal"
	"github.com/klahr/zonventure/internal/listing"
)

// A team's calendar entry introduces its link in the language of the
// team's invitation: the adventure's, or the team's own where the
// adventure is played in it.
func TestCalendarInLabelsTheLink(t *testing.T) {
	evt := icscal.Event{Title: "Matk", JoinURL: "https://x.test/play", Description: "Org — Matk\n\nJoin link: https://x.test/play"}
	if got := (eventWords{}).calendarIn(evt, look{Lang: "et"}, ""); got.Description != "Org — Matk\n\nLiitumislink: https://x.test/play" {
		t.Errorf("in the adventure's Estonian = %q", got.Description)
	}
	texts, err := listing.Scan("et", []byte(`{"en": {"title": "Hike"}}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	w := eventWords{main: listing.Texts{Title: "Matk"}, lang: "et", texts: texts}
	if got := w.calendarIn(evt, look{Lang: "et"}, "en"); got.Description != "Org — Hike\n\nJoin link: https://x.test/play" || got.Title != "Hike" {
		t.Errorf("for a team playing in English = %q / %q", got.Title, got.Description)
	}
	if got := w.calendarIn(evt, look{Lang: "et"}, "sv"); got.Description != "Org — Matk\n\nLiitumislink: https://x.test/play" {
		t.Errorf("for a team in a language the adventure isn't played in = %q", got.Description)
	}
}

// A party's host reads its dates in Estonian when the party is.
func TestTrailDateEstonian(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if got := trailDate("et", at); got != "1. november 2026" {
		t.Errorf("trailDate = %q", got)
	}
}
