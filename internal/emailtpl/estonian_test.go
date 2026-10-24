package emailtpl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This fork writes to players in Estonian: what a player, a ticket or
// box buyer or a party's host gets, and the frame around every email.
// What organisers and designers get stays English.

// estonianFuncs are the functions whose every word has its Estonian.
var estonianFuncs = []string{
	// kinds.go
	"playerFooter", "TeamInvitation", "WithSchedule", "WithContact", "TeamLink", "AsGift", "lostLink", "WithFiles",
	"WithPrints", "PartyHost", "HostedPlayerLink", "TrailConfirm", "TrailSignin", "TrailPaid", "TrailKeep", "TrailEnded",
	"RoundSummary", "BoxConfirmed", "BoxReceived", "WithEnvelope", "BoxRejected", "BoxCancelled", "PlayLinks",
	"GameEmail", "TeamMessage", "EventCancelled",
	// emailtpl.go, pictures.go
	"signature", "ThemeCover", "coverOf",
}

// estonianElsewhere are players' words said through T in other packages.
var estonianElsewhere = []string{
	// admin/store.go: a ticket's team and its players.
	"Team %d", "Player %d",
	// admin/event_files.go: files added during the game.
	"New file: %s. Open the menu and look under “Files from the organisers”.",
	"%d new files: %s. Open the menu and look under “Files from the organisers”.",
	// admin/emails.go: the previews of what organisers write to teams.
	"A message from the organisers", "Hi %s!\n\nThis is how your message will look.",
	"Dear %s,\n\nThis is how an email step reads during the game.", "Saturday's game",
	// admin/trails_public.go, admin/hosted_pricing.go: what a party's host pays for.
	"%[1]s — party adventure, up to %[2]d teams per game", "%[1]s — more teams, up to %[2]d teams per game",
	// admin/event_words.go, player/recover.go: the calendar entry.
	"Join link:",
}

// estonianKeys are the English of every string estonianFuncs, the
// players' preview samples and the house pictures say, and
// estonianElsewhere.
func estonianKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	say := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "T" {
				return true
			}
			if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %v", lit.Value, err)
				}
				keys = append(keys, s)
			}
			return true
		})
	}
	want := map[string]bool{}
	for _, f := range estonianFuncs {
		want[f] = true
	}
	found, samples := map[string]bool{}, 0
	for _, name := range []string{"kinds.go", "emailtpl.go", "pictures.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && want[d.Name.Name] {
					found[d.Name.Name] = true
					say(d.Body)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					v, ok := s.(*ast.ValueSpec)
					if !ok || len(v.Names) != 1 || v.Names[0].Name != "Kinds" {
						continue
					}
					for _, e := range v.Values[0].(*ast.CompositeLit).Elts {
						k := e.(*ast.CompositeLit)
						if a, ok := k.Elts[2].(*ast.BasicLit); ok && a.Value == `"players"` {
							samples++
							say(k)
						}
					}
				}
			}
		}
	}
	for f := range want {
		if !found[f] {
			t.Errorf("no func %s: the list no longer matches the code", f)
		}
	}
	if samples < 6 {
		t.Errorf("found %d players' samples in Kinds, want at least 6", samples)
	}
	for _, p := range housePictures {
		keys = append(keys, p.alt)
	}
	return append(keys, estonianElsewhere...)
}

func TestEveryEstonianStringIsThere(t *testing.T) {
	keys := estonianKeys(t)
	if len(keys) < 100 {
		t.Fatalf("found only %d strings: the scan no longer matches the code", len(keys))
	}
	missing := map[string]bool{}
	for _, k := range keys {
		if _, ok := catalog["et"][k]; !ok {
			missing[k] = true
		}
	}
	for k := range missing {
		t.Errorf("no Estonian for %q", k)
	}
}

// fills are what a string puts in: each argument's verb by position,
// %% and {{name}}.
var fills = regexp.MustCompile(`%(?:\[(\d+)\])?[-+# 0]*\d*(?:\.\d+)?([a-zA-Z%])|\{\{\w+\}\}`)

func fillsOf(s string) map[string]bool {
	out := map[string]bool{}
	next := 1
	for _, m := range fills.FindAllStringSubmatch(s, -1) {
		switch {
		case strings.HasPrefix(m[0], "{{"), m[2] == "%":
			out[m[0]] = true
		default:
			n := next
			if m[1] != "" {
				n, _ = strconv.Atoi(m[1])
			}
			out[strconv.Itoa(n)+m[2]] = true
			next = n + 1
		}
	}
	return out
}

// A translation puts in what its English does, if in another order: a
// verb lost or changed garbles the email.
func TestTranslationsKeepTheirFills(t *testing.T) {
	if got := fillsOf("%[2]s %s, %[1]d%% {{team}}"); len(got) != 5 || !got["2s"] || !got["3s"] || !got["1d"] || !got["%%"] || !got["{{team}}"] {
		t.Fatalf("fillsOf = %v", got)
	}
	for lang, words := range catalog {
		for k, v := range words {
			want, got := fillsOf(k), fillsOf(v)
			same := len(want) == len(got)
			for f := range want {
				same = same && got[f]
			}
			if !same {
				t.Errorf("%s: %q puts in %v, its English %q %v", lang, v, got, k, want)
			}
		}
	}
}

func TestEstonianWhen(t *testing.T) {
	at := time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC)
	if got := When("et", at, "Europe/Tallinn"); got != "laupäev, 24. oktoober 2026 kell 14.00 (EEST)" {
		t.Errorf("When = %q", got)
	}
	if got := When("et", at.AddDate(0, 0, 61), "Europe/Tallinn"); got != "neljapäev, 24. detsember 2026 kell 13.00 (EET)" {
		t.Errorf("When in winter = %q", got)
	}
}

func TestEstonianEmail(t *testing.T) {
	if Lang("et-EE") != "et" || Lang("ET") != "et" {
		t.Error("Lang does not map Estonian tags to et")
	}
	subject, c := TeamInvitation("et", "Kajakad", "Sadamamüsteerium", "Seiklus OÜ", []PlayerLink{{Player: "Anna", URL: "https://e.org/p"}}, true)
	h, text := Render(Classic, Brand{Name: "Seiklus OÜ"}, subject, c)
	for _, want := range []string{`lang="et"`, "Seiklus OÜ: sinu võistkonna lingid", "Oled oodatud: Sadamamüsteerium", "kalendrikutse", "Seiklus OÜ · saadetud keskkonnast Zonventure", "Said selle kirja, sest sinu võistkond osaleb üritusel Sadamamüsteerium, mille korraldaja on Seiklus OÜ."} {
		if !strings.Contains(h+subject, want) {
			t.Errorf("Estonian invitation lacks %q", want)
		}
	}
	if !strings.Contains(text, "Sinu võistkond Kajakad on mänguks valmis.") || !strings.Contains(text, "- Anna: https://e.org/p\r\n") {
		t.Errorf("text = %q", text)
	}
	subject, c = WithSchedule(subject, c, "Sadamamüsteerium", When("et", time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC), "Europe/Tallinn"), "Raekoja platsil", true)
	if !strings.HasPrefix(subject, "Uus aeg – Seiklus OÜ: ") || !strings.Contains(c.Body, "Aeg on muutunud. Uus algus: **laupäev, 24. oktoober 2026 kell 14.00 (EEST)**.") || !strings.Contains(c.Body, "Kogunemiskoht: **Raekoja platsil**") {
		t.Errorf("changed invitation = %q\n%s", subject, c.Body)
	}
}
