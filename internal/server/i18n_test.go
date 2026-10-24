package server

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestI18nCompleteness checks that every English string passed to the
// `t(...)` helper in the embedded HTML/JS has a Swedish translation
// in the JSON locale source-of-truth (web/locales/web/sv.json).
// Missing keys still render in English at runtime (the t() helper
// falls through), but a translator scanning the dictionary should
// see the scope of work, and a forgotten key during refactors is
// exactly the thing this test catches.
//
// Limitations: only finds string-literal arguments to t(); a dynamic
// argument like t(state.flag) escapes the lexer. Those are rare and
// listed in the audit comment below.
//
// These three pages are what players see, so this fork checks Estonian
// (et.json, which holds only their strings) the same way.
func TestI18nCompleteness(t *testing.T) {
	for _, lang := range []string{"sv", "et"} {
		t.Run(lang, func(t *testing.T) { testI18nCompleteness(t, lang) })
	}
}

func testI18nCompleteness(t *testing.T, lang string) {
	pages := []string{"web/live.html", "web/recap.html", "web/play.html"}
	raw, err := webFS.ReadFile("web/locales/web/" + lang + ".json")
	if err != nil {
		t.Fatalf("read %s.json: %v", lang, err)
	}
	var catalog map[string]string
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("parse %s.json: %v", lang, err)
	}

	// Regex matches the first string-literal argument to t(...). Both
	// single and double quoted; backslash escapes recognised so an
	// embedded \" or \' doesn't terminate the match early. The \b
	// word boundary keeps us from matching `parseInt(`,
	// `getElementById(`, etc — the previous character must be a
	// non-identifier character (or start-of-string), so `t` has to
	// be its own token.
	rxSingle := regexp.MustCompile(`\bt\(\s*'((?:[^'\\]|\\.)*)'`)
	rxDouble := regexp.MustCompile(`\bt\(\s*"((?:[^"\\]|\\.)*)"`)

	seen := make(map[string]struct{})
	for _, name := range pages {
		body, err := webFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range rxSingle.FindAllSubmatch(body, -1) {
			seen[unescapeSingle(string(m[1]))] = struct{}{}
		}
		for _, m := range rxDouble.FindAllSubmatch(body, -1) {
			seen[unescapeDouble(string(m[1]))] = struct{}{}
		}
	}

	// Strings the grep can't see legitimately (built from runtime data,
	// or referenced as state values like t(state.role)). Adding one
	// here is fine; the cost is just maintaining this allowlist.
	skip := map[string]bool{
		// e.g. t(r.state) where state is one of "scheduled"/"running"/...
		// — the literals are translated elsewhere, the lexer just
		// can't trace through the variable.
	}

	var missing []string
	for k := range seen {
		if skip[k] {
			continue
		}
		if _, ok := catalog[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		// Sort for stable output; helps when a CI run shows a long
		// list and you want to compare against last week.
		for i := 0; i < len(missing); i++ {
			for j := i + 1; j < len(missing); j++ {
				if missing[i] > missing[j] {
					missing[i], missing[j] = missing[j], missing[i]
				}
			}
		}
		t.Logf("missing %d %s translation(s):", len(missing), lang)
		for _, k := range missing {
			t.Logf("  %q", k)
		}
		t.Errorf("i18n.js is missing %d English string(s) — see log above", len(missing))
	}
}

func unescapeSingle(s string) string { return jsUnescape(s) }
func unescapeDouble(s string) string { return jsUnescape(s) }

// jsUnescape decodes the limited subset of JS string escapes we care
// about: \', \", \\, \n, \t. Everything else is passed through as-is
// — this is a translation-table check, not a JS interpreter.
func jsUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\'':
				b.WriteByte('\'')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
