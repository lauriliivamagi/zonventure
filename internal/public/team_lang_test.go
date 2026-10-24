package public

import "testing"

// The team page is the first thing a guest opens, so this fork has it
// in Estonian wherever upstream has it in Swedish.
func TestTeamPageEstonianCoversSwedish(t *testing.T) {
	for k := range teamSV {
		if _, ok := teamET[k]; !ok {
			t.Errorf("no Estonian for %q", k)
		}
	}
	if got := teamT("et", "Copy"); got != "Kopeeri" {
		t.Errorf(`teamT("et", "Copy") = %q`, got)
	}
}
