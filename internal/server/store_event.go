package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/klahr/zonventure/internal/adventure"
	"github.com/klahr/zonventure/internal/listing"
	"github.com/klahr/zonventure/internal/printmat"
	"github.com/klahr/zonventure/internal/public"
)

// renderStoreEventPage serves the per-game detail page paired with
// /store. Returns 404 for events that aren't currently
// public_for_sale + priced + running, so a stale
// link doesn't expose past prices or events the operator has
// unlisted.
//
// Mounted at /o/{slug}/store/event/{event_id} as the canonical URL;
// the legacy /store/event/{event_id} route 301-redirects there via
// redirectLegacyStoreEvent so old QR codes / emailed links keep
// working. When `slugFromURL` is empty (legacy route fallthrough,
// in tests, or operators without a slug routed yet), the renderer
// derives the slug from the event's owning org and emits canonical
// links to the org-scoped path.
//
// Substitution shape mirrors renderLandingPage / renderTopbarPage:
// the template is plain HTML with {{KEY}} placeholders, swapped via
// strings.ReplaceAll. Per-event values are HTML-escaped where they
// land inside text content (blurb, names) or attribute values
// (cover URL). Operators who want a richer / branded variant drop
// their own store-event.html into ZV_WEB_OVERRIDE_DIR; the embedded
// brand-neutral version is shadowed.
// simulated says the simulated payment provider takes the payment, not Stripe.
func renderStoreEventPage(w http.ResponseWriter, r *http.Request, db *sql.DB, publicURL string, simulated bool) {
	idStr := chi.URLParam(r, "event_id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		render404(w, r)
		return
	}
	slugFromURL := strings.TrimSpace(chi.URLParam(r, "slug"))

	body, err := readWebFileFor(r, "web/store-event.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var (
		eventName  sql.NullString
		advSlug    string
		advTitle   sql.NullString
		orgName    string
		orgSlug    string
		blurb      string
		priceCents sql.NullInt64
		currency   string
		teamSize   int
		startLabel sql.NullString
		// startLat and startLng put the starting point on the page's map.
		startLat, startLng sql.NullFloat64
		hasCover           bool
		hasIcon            bool
		// orgVerified is a verified creator: the page shows a badge.
		orgVerified bool
		// tour is a walk, not a game, and home one played where the
		// buyer is (see public.StoreItemDTO): the page says which.
		tour, home bool
		// facts and accessNote are what the adventure tells a buyer
		// about its playing time, walk, age, difficulty and access.
		facts      []byte
		accessNote string
	)
	var (
		brandAccent          sql.NullString
		brandAccentSecondary sql.NullString
		brandBackground      sql.NullString
		brandText            sql.NullString
		brandFont            string
	)
	var (
		mainLang           string
		advTexts, ownTexts []byte
	)
	err = db.QueryRowContext(r.Context(), `
		SELECT l.name, e.adventure_slug,
		       a.title AS adv_title,
		       o.name AS org_name,
		       o.slug AS org_slug, o.verified_creator,
		       l.blurb,
		       l.price_cents,
		       l.currency,
		       l.team_size,
		       a.start_label, a.start_lat, a.start_lng,
		       COALESCE(a.facts, '{}'), COALESCE(a.access_note, ''),
		       (l.cover_mime_type IS NOT NULL) AS has_cover,
		       COALESCE(a.avatar_mime_type IS NOT NULL, FALSE) AS has_icon,
		       COALESCE((g.graph->>'tour')::boolean, FALSE) AS tour,
		       (a.start_lat IS NULL AND NOT COALESCE(jsonb_typeof(g.graph->'zones') = 'array' AND jsonb_array_length(g.graph->'zones') > 0, FALSE)) AS home,
		       o.accent_color, o.accent_secondary_color, o.background_color, o.text_color, o.brand_font,
		       `+listing.Columns+`
		FROM events e
		JOIN event_listings l ON l.event_id = e.id
		JOIN organizations o ON o.id = e.org_id
		LEFT JOIN adventures a ON a.org_id = e.org_id AND a.slug = e.adventure_slug
		LEFT JOIN adventure_graphs g ON g.id = e.graph_id
		WHERE e.id = $1
		  AND e.public_for_sale
		  AND e.ticket_price_cents IS NOT NULL
		  AND e.state = 'running'
		  AND NOT e.sales_stopped
	`, id).Scan(&eventName, &advSlug, &advTitle, &orgName, &orgSlug, &orgVerified, &blurb, &priceCents,
		&currency, &teamSize, &startLabel, &startLat, &startLng, &facts, &accessNote, &hasCover, &hasIcon, &tour, &home,
		&brandAccent, &brandAccentSecondary, &brandBackground, &brandText, &brandFont,
		&mainLang, &advTexts, &ownTexts)
	if errors.Is(err, sql.ErrNoRows) {
		renderStoreEventGone(w, r, db, id, slugFromURL)
		return
	}
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	texts, err := listing.Scan(mainLang, advTexts, ownTexts)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}

	// The listing in the page's language, where it is translated.
	lang := resolveLang(w, r)
	tr := texts.In(lang, listing.Texts{Name: eventName.String, Blurb: blurb, Title: advTitle.String, StartLabel: startLabel.String, AccessNote: accessNote})
	eventName.String, blurb, advTitle.String, startLabel.String, accessNote = tr.Name, tr.Blurb, tr.Title, tr.StartLabel, strings.TrimSpace(tr.AccessNote)
	var f adventure.Facts
	if err := json.Unmarshal(facts, &f); err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}

	// Cross-org tunnelling guard: if the page was reached via
	// /o/{slug}/store/event/{id} but the event belongs to a
	// different org, 404 rather than render a different company's
	// product page under this company's URL. The legacy /store/event/{id}
	// path leaves slugFromURL empty and skips this check.
	if slugFromURL != "" && slugFromURL != orgSlug {
		render404(w, r)
		return
	}

	// The listing's name heads the page, as it heads the store's card;
	// the adventure's name goes under it when it says something else.
	// Fall through to slug when the adventure row has no human name;
	// the slug is at worst kebab-case which is readable enough for the
	// rare case the row was renamed away.
	advName := strings.TrimSpace(advSlug)
	if advTitle.Valid && strings.TrimSpace(advTitle.String) != "" {
		advName = strings.TrimSpace(advTitle.String)
	}
	title, subtitle := advName, ""
	if eventName.Valid && strings.TrimSpace(eventName.String) != "" {
		title = strings.TrimSpace(eventName.String)
		if !strings.EqualFold(title, advName) {
			subtitle = advName
		}
	}

	priceMajor := ""
	if priceCents.Valid && priceCents.Int64 > 0 {
		// Drop trailing zero cents for whole-currency amounts so
		// "25 €" reads better than "25.00 €" on hero copy. Stripe
		// itself tolerates either side: this is a display choice.
		amount := float64(priceCents.Int64) / 100
		if priceCents.Int64%100 == 0 {
			priceMajor = fmt.Sprintf("%d", int(amount))
		} else {
			priceMajor = fmt.Sprintf("%.2f", amount)
		}
	}
	priceDisplay := formatStorePrice(priceCents.Int64, currency)

	// The adventure's look: its cover stands in for the event's, and
	// its title typeface heads the page.
	look, err := public.LoadPageLook(r.Context(), db, public.LookAssets{ThemeCover: themeCoverURL, Font: lookFontURL}, advSlug)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	coverURL, lookFont := "", ""
	if hasCover {
		coverURL = "/store-cover/event/" + strconv.FormatInt(id, 10)
	} else if look != nil && look.CoverURL != "" {
		coverURL, hasCover = look.CoverURL, true
	}
	if look != nil && look.FontCSS != "" {
		// [data-look-title] lets an override template (ZV_WEB_OVERRIDE_DIR)
		// with its own markup mark the title it wants in the look's font.
		lookFont = `<link rel="stylesheet" href="` + html.EscapeString(look.FontCSS) + `">` +
			`<style>.hero h1, [data-look-title] { font-family: "` + html.EscapeString(look.HeadingFont) + `", var(--font-brand); }</style>`
	}
	iconURL, ogImage := "", coverURL
	if hasIcon {
		iconURL = "/adventure-assets/avatar/" + url.PathEscape(advSlug)
		if ogImage == "" {
			ogImage = iconURL
		}
	}
	if ogImage != "" {
		ogImage = buildAbsoluteURL(publicURL, ogImage)
	}

	teamSizeStr := strconv.Itoa(teamSize)

	// Localised topbar chrome + body copy. Both pull from the
	// shared landingLocales table so a translator updates one place
	// and every store surface follows.
	s := landingLocales[lang]
	free := priceCents.Valid && priceCents.Int64 == 0
	if free {
		priceDisplay, s.StorePerTicket, s.StoreEventBuyCTA = s.StorePriceFree, "", s.StoreEventFreeCTA
	} else if !simulated {
		s.StoreEventTermsHTML = s.StoreEventPaymentByStripe + " " + s.StoreEventTermsHTML
	}
	kindKey, kind := "game", s.StoreKindGame
	switch {
	case tour:
		kindKey, kind = "walk", s.StoreKindWalk
		s.StoreEventMetaFormatValue = s.StoreEventMetaFormatWalk
	case home:
		kindKey, kind = "home", s.StoreKindHome
		s.StoreEventMetaFormatValue = s.StoreEventMetaFormatHome
	}
	subs := topbarSubs(s, lang)
	subs = append(subs, storeSubs(s)...)

	// Team label needs the locale's plural template. Same {n}
	// placeholder convention as the JS-side teamSizeLabel — keep
	// them in lock-step so a translator only edits one phrase.
	teamLabel := s.StoreTeamSizeSolo
	if teamSize > 1 {
		teamLabel = strings.ReplaceAll(s.StoreTeamSizePlural, "{n}", teamSizeStr)
	}
	ft := formatFacts(lang, s, f)
	if ft.Access == "" {
		// A note alone is the tile's value.
		ft.Access, accessNote = accessNote, ""
	}

	// Canonical path always points at the org-scoped URL — the
	// legacy /store/event/{id} surface 301s here, so search engines
	// converge on a single URL per event regardless of how the
	// page was reached.
	canonicalPath := "/o/" + orgSlug + "/store/event/" + strconv.FormatInt(id, 10)
	apiBase := "/api/o/" + orgSlug + "/store"

	subs = append(subs,
		[2]string{"{{TITLE}}", html.EscapeString(title)},
		[2]string{"{{SUBTITLE}}", html.EscapeString(subtitle)},
		[2]string{"{{ORG_NAME}}", html.EscapeString(orgName)},
		[2]string{"{{BLURB}}", html.EscapeString(strings.TrimSpace(blurb))},
		[2]string{"{{PRICE_DISPLAY}}", html.EscapeString(priceDisplay)},
		[2]string{"{{PRICE_MAJOR}}", html.EscapeString(priceMajor)},
		[2]string{"{{CURRENCY}}", html.EscapeString(strings.ToUpper(currency))},
		[2]string{"{{TEAM_SIZE}}", teamSizeStr},
		[2]string{"{{TEAM_LABEL}}", html.EscapeString(teamLabel)},
		[2]string{"{{START_LABEL}}", html.EscapeString(strings.TrimSpace(startLabel.String))},
		[2]string{"{{START_LABEL_HIDDEN}}", hiddenIf(strings.TrimSpace(startLabel.String) == "")},
		// The adventure's facts, each tile hidden when its author
		// hasn't said; access is the access fact, its note or both.
		[2]string{"{{DURATION}}", html.EscapeString(ft.Duration)},
		[2]string{"{{DURATION_HIDDEN}}", hiddenIf(ft.Duration == "")},
		[2]string{"{{DISTANCE}}", html.EscapeString(ft.Distance)},
		[2]string{"{{DISTANCE_HIDDEN}}", hiddenIf(ft.Distance == "")},
		[2]string{"{{AGE}}", html.EscapeString(ft.Age)},
		[2]string{"{{AGE_HIDDEN}}", hiddenIf(ft.Age == "")},
		[2]string{"{{DIFFICULTY}}", html.EscapeString(ft.Difficulty)},
		[2]string{"{{DIFFICULTY_HIDDEN}}", hiddenIf(ft.Difficulty == "")},
		[2]string{"{{ACCESS}}", html.EscapeString(ft.Access)},
		[2]string{"{{ACCESS_NOTE}}", html.EscapeString(accessNote)},
		[2]string{"{{ACCESS_HIDDEN}}", hiddenIf(ft.Access == "")},
		[2]string{"{{LANGUAGES}}", html.EscapeString(strings.Join(texts.Languages(), ", "))},
		[2]string{"{{LANGUAGES_HIDDEN}}", hiddenIf(len(texts.Languages()) == 0)},
		// Where it starts, on a map under the ticket's details; hidden
		// for one played wherever the buyer is.
		[2]string{"{{HAS_START}}", boolAttr(startLat.Valid && startLng.Valid)},
		[2]string{"{{START_LAT}}", coord(startLat)},
		[2]string{"{{START_LNG}}", coord(startLng)},
		[2]string{"{{START_MAP_URL}}", html.EscapeString(directionsURL(startLat, startLng))},
		[2]string{"{{STORE_EVENT_START_MAP}}", html.EscapeString(s.StoreEventStartMap)},
		[2]string{"{{STORE_EVENT_OPEN_MAP}}", html.EscapeString(s.StoreEventOpenMap)},
		[2]string{"{{STORE_MAP_ZOOM_HINT}}", html.EscapeString(s.StoreMapZoomHint)},
		[2]string{"{{STORE_MAP_PAN_HINT}}", html.EscapeString(s.StoreMapPanHint)},
		// COVER_URL lands inside both an HTML attribute AND a CSS
		// url('…') context; html.EscapeString covers the HTML side
		// but a `'` would still break out of the CSS quotes once
		// the HTML parser decodes &#39;. cssURLQuote percent-encodes
		// the chars that matter for CSS so the URL survives the
		// nested-context decode chain.
		[2]string{"{{COVER_URL}}", html.EscapeString(cssURLQuote(coverURL))},
		[2]string{"{{HAS_COVER}}", boolAttr(hasCover)},
		[2]string{"{{ORG_VERIFIED}}", boolAttr(orgVerified)},
		// A walk or a game: a badge on the page, and IS_TOUR for an
		// override template that styles the two apart.
		[2]string{"{{KIND_LABEL}}", html.EscapeString(kind)},
		[2]string{"{{KIND}}", kindKey},
		[2]string{"{{IS_TOUR}}", boolAttr(tour)},
		[2]string{"{{LOOK_FONT}}", lookFont},
		// The adventure's icon, beside the title; also what a link
		// preview shows when the event has no cover of its own.
		[2]string{"{{ICON_URL}}", html.EscapeString(iconURL)},
		[2]string{"{{HAS_ICON}}", boolAttr(hasIcon)},
		[2]string{"{{OG_IMAGE_URL}}", html.EscapeString(ogImage)},
		[2]string{"{{EVENT_ID}}", strconv.FormatInt(id, 10)},
		[2]string{"{{LETTER_LANGS}}", html.EscapeString(letterLangsJSON(r.Context(), db, id))},
		// The organisation's page: where the breadcrumb leads back to.
		[2]string{"{{ORG_PATH}}", "/o/" + url.PathEscape(orgSlug)},
		[2]string{"{{ADVENTURE_SLUG}}", html.EscapeString(advSlug)},
		[2]string{"{{CANONICAL_URL}}", html.EscapeString(buildAbsoluteURL(publicURL, canonicalPath))},
		[2]string{"{{STORE_API_BASE}}", apiBase},
		[2]string{"{{ORG_BRAND_OVERRIDES}}", orgBrandStyleBlock(
			brandAccent.String, brandAccentSecondary.String,
			brandBackground.String, brandText.String,
		) + orgFontBlock(brandFont)},
	)

	out := string(body)
	for _, sub := range subs {
		out = strings.ReplaceAll(out, sub[0], sub[1])
	}
	// Final pass: the locale strings carry inline {n} / {team_label}
	// placeholders for the team-size feature line, FAQ answer, and
	// hero price suffix. Run them after the main {{KEY}} pass so a
	// translator can place them anywhere inside the localised
	// string without coordinating with the renderer.
	out = strings.ReplaceAll(out, "{n}", teamSizeStr)
	out = strings.ReplaceAll(out, "{team_label}", teamLabel)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "Cookie, Accept-Language")
	// Short cache: the price / blurb are mutable in the arrange UI.
	// 60 seconds is plenty for crawler bursts without staleness
	// becoming a customer-visible problem.
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write([]byte(out))
}

// coord is a coordinate for the page's map, or "" when there is none.
func coord(v sql.NullFloat64) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatFloat(v.Float64, 'f', 6, 64)
}

// directionsURL opens Google Maps' directions to a starting point: in
// the Maps app on a phone that has it, else on the web.
func directionsURL(lat, lng sql.NullFloat64) string {
	if !lat.Valid || !lng.Valid {
		return ""
	}
	return "https://www.google.com/maps/dir/?api=1&destination=" + coord(lat) + "," + coord(lng)
}

// formatStorePrice mirrors the JS helper on the listing page —
// known currencies get a symbol, others fall back to the ISO code.
// Whole-amount totals drop ".00" for cleaner display.
func formatStorePrice(cents int64, currency string) string {
	if cents <= 0 {
		return ""
	}
	c := strings.ToLower(currency)
	amount := float64(cents) / 100
	formatted := ""
	if cents%100 == 0 {
		formatted = strconv.FormatInt(cents/100, 10)
	} else {
		formatted = fmt.Sprintf("%.2f", amount)
	}
	switch c {
	case "eur":
		return "€" + formatted
	case "usd":
		return "$" + formatted
	case "gbp":
		return "£" + formatted
	case "sek", "dkk", "nok":
		return formatted + " kr"
	default:
		return formatted + " " + strings.ToUpper(c)
	}
}

// buildAbsoluteURL joins the operator-configured public origin with
// a path. Used for canonical URLs and OG tags where a relative path
// would render as "javascript:" or get prefixed with the crawler's
// own host. Empty publicURL falls back to the path alone — operator
// hasn't set ZV_PUBLIC_URL yet, OG previews will be incomplete but
// the page still works.
func buildAbsoluteURL(publicURL, path string) string {
	base := strings.TrimRight(publicURL, "/")
	if base == "" {
		return path
	}
	return base + path
}

// boolAttr returns "true" / "false" for {{KEY}} placeholders that
// drive CSS data attributes or simple JS boolean checks in inlined
// scripts.
// cssURLQuote percent-encodes characters that would let a URL break
// out of a CSS `url('…')` literal: quotes, parens, backslash, and
// whitespace. The browser's URL fetch un-encodes them back, so the
// underlying request still resolves to the original bytes. Only used
// for URLs that flow into a `style=` attribute via template
// substitution; the HTML attribute encode is layered on top.
func cssURLQuote(u string) string {
	out := make([]byte, 0, len(u)+8)
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch c {
		case '"', '\'', '(', ')', '\\', ' ', '\t', '\r', '\n':
			out = append(out, '%')
			const hex = "0123456789ABCDEF"
			out = append(out, hex[c>>4], hex[c&0x0F])
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

// hiddenIf is a " hidden" attribute when b, else nothing.
func hiddenIf(b bool) string {
	if b {
		return " hidden"
	}
	return ""
}

func boolAttr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// letterLangsJSON lists, as a JSON array, the languages a buyer chooses
// the event's posted letter in: [] when it posts none, and then checkout
// asks for no address either.
func letterLangsJSON(ctx context.Context, db *sql.DB, eventID int64) string {
	g, o, err := printmat.ForEvent(ctx, db, eventID)
	langs := []string{}
	if err == nil && printmat.Posts(g, o) {
		if langs = g.Languages(); langs == nil {
			langs = []string{g.Language}
		}
	}
	b, _ := json.Marshal(langs)
	return string(b)
}

// renderStoreEventGone answers a store event that isn't for sale: one
// with an approved listing that is paused or has stopped selling says
// so, as its buyers will be back; anything else is not found.
func renderStoreEventGone(w http.ResponseWriter, r *http.Request, db *sql.DB, id int64, slug string) {
	var offSale bool
	_ = db.QueryRowContext(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM events e
			JOIN event_listings l ON l.event_id = e.id
			JOIN organizations o ON o.id = e.org_id
			WHERE e.id = $1 AND e.public_for_sale AND e.state IN ('running', 'paused')
			  AND ($2 = '' OR o.slug = $2))
	`, id, slug).Scan(&offSale)
	if !offSale {
		render404(w, r)
		return
	}
	s := landingLocales[resolveLang(w, r)]
	renderErrorPage(w, r, http.StatusNotFound, s.StoreEventOffSaleHeading, s.StoreEventOffSaleHeading, s.StoreEventNotForSale)
}
