package public

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/klahr/zonventure/internal/auth"
	"github.com/klahr/zonventure/internal/domains"
	"github.com/klahr/zonventure/internal/emailtpl"
	"github.com/klahr/zonventure/internal/eventfile"
	"github.com/klahr/zonventure/internal/graph"
	"github.com/klahr/zonventure/internal/httpx"
	"github.com/klahr/zonventure/internal/imageutil"
	"github.com/klahr/zonventure/internal/play"
	"github.com/klahr/zonventure/internal/printmat"
	"github.com/klahr/zonventure/internal/sitename"
	"github.com/klahr/zonventure/internal/storage"
	"github.com/klahr/zonventure/internal/ws"
	qrcode "github.com/skip2/go-qrcode"
)

// maxTeamAvatarBytes caps uploads from the share page. Matches the
// admin-side limit so both entry points behave the same.
const maxTeamAvatarBytes = 5 * 1024 * 1024

// teamShareView is the data passed to the share-page template.
type teamShareView struct {
	// Lang is the page's language, as the event's emails are written:
	// the adventure's, else its organisation's ("en", "sv").
	Lang           string
	SiteName       string
	TeamName       string
	AdventureTitle string
	AvatarURL      string
	Token          string
	Members        []teamShareSlot
	// State, ScheduledAt and StartedAt feed the countdown card. State
	// is the event's lifecycle code; ScheduledAt is the server-side
	// "YYYY-MM-DD HH:MM:SS" UTC string, StartedAt the same. The inline
	// JS appends 'Z' before parsing so the browser interprets them as
	// UTC, then ticks down against Date.now() in local time.
	State       string
	ScheduledAt string
	StartedAt   string
	// SelfGuided switches the page between two audiences. A self-guided
	// play (a quest bought/gifted, or any running, unscheduled event) is
	// a casual one-time player who just wants to begin — so the page is
	// warm and start-first. A hosted event (scheduled, arranger-run) is a
	// captain setting a team up — so it keeps the share links, countdown
	// and QR. See TeamShare for how it's decided.
	SelfGuided bool
	// Solo is true for a one-seat team: the self-guided view then shows a
	// single "Start" button instead of a name picker.
	Solo bool
	// Files are what the organisers left for the players, linked by the
	// team's token (TeamFile).
	Files []teamShareFile
}

type teamShareFile struct {
	Name, URL, Size string
}

// fileSize is a file's size for the page: "340 kB", "2.4 MB" ("2,4 MB"
// in Swedish).
func fileSize(n int64, lang string) string {
	switch {
	case n >= 10_000_000:
		return fmt.Sprintf("%d MB", (n+500_000)/1_000_000)
	case n >= 1_000_000:
		mb := fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
		if lang == "sv" || lang == "et" {
			mb = strings.Replace(mb, ".", ",", 1)
		}
		return mb
	default:
		return fmt.Sprintf("%d kB", max(1, (n+500)/1000))
	}
}

type teamShareSlot struct {
	Name     string
	MagicURL string
	// Taken says a phone is playing as this player now. Opening the
	// link anyway takes the seat over and signs that phone out.
	Taken bool
}

// TeamShare serves the one-URL-per-team page. The arranger sends this
// single URL to the team captain; each member taps their own name to
// claim the slot. Unauthenticated (the token IS the auth): anyone with
// the URL can see every slot's magic link, which is exactly what team
// distribution needs.
func (h *Handlers) TeamShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "bad token", http.StatusBadRequest)
		return
	}
	var (
		view  teamShareView
		title sql.NullString
	)
	var (
		teamID    int64
		eventID   int64
		hasAvatar int
	)
	var (
		schedAt sql.NullString
		startAt sql.NullString
		forSale bool
		// teamLang is the language the team plays in, "" for the
		// adventure's own.
		teamLang string
	)
	// The page speaks the visitor's language; the adventure's own words
	// (its title) are in the language the team plays in, or the
	// visitor's for a team that hasn't chosen one.
	view.Lang = emailtpl.LangFromRequest(r)
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT t.id, t.event_id, t.name, COALESCE(NULLIF(g.graph->'translations'->COALESCE(NULLIF(t.lang, ''), $2)->>'meta:title', ''), a.title),
		       (t.avatar_mime_type IS NOT NULL)::int AS has_avatar,
		       ar.state, ar.scheduled_at, ar.started_at, ar.public_for_sale, t.lang
		FROM teams t
		JOIN events ar ON ar.id = t.event_id
		LEFT JOIN adventures a ON a.slug = ar.adventure_slug
		LEFT JOIN adventure_graphs g ON g.id = ar.graph_id
		WHERE t.token = $1
	`, token, view.Lang).Scan(&teamID, &eventID, &view.TeamName, &title, &hasAvatar,
		&view.State, &schedAt, &startAt, &forSale, &teamLang)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "team not found — ask your arranger for a fresh link", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	view.AdventureTitle = title.String
	view.Token = token
	view.ScheduledAt = schedAt.String
	view.StartedAt = startAt.String
	// Self-guided: a quest on sale, or any running event with no
	// scheduled group start — i.e. the player can just begin. Everything
	// else (a scheduled, arranger-run event) is hosted.
	view.SelfGuided = forSale || (view.State == "running" && strings.TrimSpace(schedAt.String) == "")
	if hasAvatar != 0 {
		view.AvatarURL = play.TeamAvatarURL(teamID)
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT ps.name, ps.token,
		       EXISTS (SELECT 1 FROM slot_sessions ss
		               WHERE ss.slot_id = ps.id AND ss.active AND NOT ss.replaced AND NOT ss.ended)
		FROM player_slots ps
		JOIN teams t ON t.id = ps.team_id
		WHERE t.token = $1
		ORDER BY LOWER(ps.name)
	`, token)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var (
			name, slotTok string
			taken         bool
		)
		if err := rows.Scan(&name, &slotTok, &taken); err != nil {
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		view.Members = append(view.Members, teamShareSlot{
			Name: name, MagicURL: "/play?t=" + slotTok, Taken: taken,
		})
	}

	view.Solo = len(view.Members) == 1

	files, err := eventfile.ListForPlayersIn(r.Context(), h.DB, eventID, teamLang)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	for _, f := range files {
		view.Files = append(view.Files, teamShareFile{
			Name: f.Name, Size: fileSize(f.Size, view.Lang),
			URL: "/team/" + url.PathEscape(token) + "/files/" + strconv.FormatInt(f.ID, 10),
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	view.SiteName = sitename.Name()
	if err := teamShareTmpl.Execute(w, view); err != nil {
		// Already wrote some bytes most likely; nothing useful to report.
		_ = err
	}
}

// JoinTeamByToken is the "scan to join" action on the self-guided team
// page: it takes the next free seat on the team, signs this device into
// it, and tells the page where to go. A player who scans the QR (or taps
// Start on the host's phone) never has to pick which "Player N" they are.
//
// POST /api/public/team/{token}/join → {"redirect":"/play"} on success,
// 409 {"code":"team_full"} when every seat is taken. If this device is
// already signed into this team, it just continues rather than burning a
// second seat.
func (h *Handlers) JoinTeamByToken(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	teamID, _, ok := h.teamByToken(w, r, token)
	if !ok {
		return
	}
	// Send the player straight into the app. We've just established the
	// slot session here, so there's no need to bounce through /play (the
	// voucher front door) — doing so risks landing back on that page if
	// the fresh cookie isn't seen on the next hop.
	const dest = "/app/play/"
	// Already in on this device? Continue instead of taking another seat.
	if slot, ok := auth.SlotFromContext(r.Context()); ok && slot.TeamID == teamID {
		httpx.JSON(w, http.StatusOK, map[string]string{"redirect": dest})
		return
	}
	slot, session, err := auth.ClaimFreeSlot(r.Context(), h.DB, teamID)
	if errors.Is(err, auth.ErrTeamFull) {
		httpx.JSON(w, http.StatusConflict, map[string]string{
			"error": "everyone has already joined this team",
			"code":  "team_full",
		})
		return
	}
	if errors.Is(err, auth.ErrOrgCannotPay) {
		httpx.JSON(w, http.StatusPaymentRequired, map[string]string{
			"error": "this game can't start just now: ask the organiser",
			"code":  "org_cannot_pay",
		})
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "join failed")
		return
	}
	_ = slot
	auth.SetSlotCookie(w, session, auth.CookieSecure(r, h.SecureCookie))
	httpx.JSON(w, http.StatusOK, map[string]string{"redirect": dest})
}

// teamByToken resolves a share token to (teamID, eventID). Empty/missing
// tokens return (0, 0, false) with the HTTP error already written.
// Used by the share page upload/delete handlers — the token IS the
// authentication so there's no Role guard around them.
func (h *Handlers) teamByToken(w http.ResponseWriter, r *http.Request, token string) (int64, int64, bool) {
	if token == "" {
		httpx.Error(w, http.StatusBadRequest, "bad token")
		return 0, 0, false
	}
	var (
		teamID  int64
		eventID int64
	)
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT id, event_id FROM teams WHERE token = $1
	`, token).Scan(&teamID, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "team not found")
		return 0, 0, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "lookup failed")
		return 0, 0, false
	}
	return teamID, eventID, true
}

// SetTeamAvatarByToken lets the team itself upload a profile picture
// via its share URL. Multipart POST with a `file` field. Same size and
// MIME constraints as the admin-side endpoint.
func (h *Handlers) SetTeamAvatarByToken(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	teamID, eventID, ok := h.teamByToken(w, r, token)
	if !ok {
		return
	}
	// Like the player app's own upload: the team's to change only until
	// it begins.
	if begun, err := play.TeamBegun(r.Context(), h.DB, teamID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "loading team failed")
		return
	} else if begun {
		httpx.JSON(w, http.StatusConflict, map[string]string{"error": "the team picture is fixed once your team has begun", "code": "team_started"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTeamAvatarBytes+(1<<20))
	if err := r.ParseMultipartForm(maxTeamAvatarBytes); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad multipart form")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "file field required")
		return
	}
	defer f.Close()
	if hdr.Size > maxTeamAvatarBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "image too large")
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTeamAvatarBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "read failed")
		return
	}
	if int64(len(data)) > maxTeamAvatarBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "image too large")
		return
	}
	mime := hdr.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "image/") {
		httpx.Error(w, http.StatusBadRequest, "only image uploads are accepted")
		return
	}
	shrunk, newMIME, err := imageutil.Shrink(data, imageutil.DefaultMaxDim)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "unreadable image: "+err.Error())
		return
	}
	data, mime = shrunk, newMIME
	if err := h.Storage.PutTeamAvatar(r.Context(), teamID, data, mime); err != nil {
		// teamByToken already proved the row exists; an ErrNotFound
		// here means the team was deleted mid-upload — surface as 404.
		if errors.Is(err, storage.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "team not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "store failed: "+err.Error())
		return
	}
	// Tell any listening run detail / live board that this team's
	// picture changed so they re-fetch and re-render.
	h.Hub.Publish(ws.EventTopic(eventID), "changed", nil)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"url":  play.TeamAvatarURL(teamID),
		"mime": mime,
		"size": int64(len(data)),
	})
}

// TeamShareQR renders the team-share URL as a PNG QR code. The token
// IS the auth (anyone with the URL can already see the share page
// itself), so we don't gate access here. Cached for a minute to spare
// the encoder work when the captain's phone re-fetches the image on
// rotation / wake.
func (h *Handlers) TeamShareQR(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "bad token", http.StatusBadRequest)
		return
	}
	// Build the absolute team-share URL the QR encodes. Read only
	// from h.PublicURL — request headers like X-Forwarded-Host
	// could otherwise be poisoned by a hostile reverse proxy and
	// the printed QR would point arrangers + players at a phishing
	// origin. No request-host fallback: a QR pointing at "localhost"
	// or whatever the operator's machine name happens to be is
	// useless to anyone scanning it on a different device, and the
	// 503 here pushes the operator to set ZV_PUBLIC_URL before the
	// QR is printed and handed out. Same posture as buildResetLink.
	// An organisation's own domain is safe: only a registered one
	// gets here.
	base := strings.TrimRight(h.PublicURL, "/")
	if e, ok := domains.FromContext(r.Context()); ok {
		base = domains.Origin(e.Host)
	}
	if base == "" {
		http.Error(w, "operator must set ZV_PUBLIC_URL before generating share QR codes", http.StatusServiceUnavailable)
		return
	}
	target := base + "/team/" + token
	png, err := qrcode.Encode(target, qrcode.Medium, 360)
	if err != nil {
		http.Error(w, "qr encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(png)
}

// DeleteTeamAvatarByToken clears the team's profile picture via the
// share URL.
func (h *Handlers) DeleteTeamAvatarByToken(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	teamID, eventID, ok := h.teamByToken(w, r, token)
	if !ok {
		return
	}
	// Like the player app's own upload: the team's to change only until
	// it begins.
	if begun, err := play.TeamBegun(r.Context(), h.DB, teamID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "loading team failed")
		return
	} else if begun {
		httpx.JSON(w, http.StatusConflict, map[string]string{"error": "the team picture is fixed once your team has begun", "code": "team_started"})
		return
	}
	if err := h.Storage.DeleteTeamAvatar(r.Context(), teamID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "clear failed")
		return
	}
	h.Hub.Publish(ws.EventTopic(eventID), "changed", nil)
	httpx.JSON(w, http.StatusNoContent, nil)
}

// teamSV is the team page in Swedish, by its English words.
var teamSV = map[string]string{
	"Skip to start":      "Hoppa till start",
	"Your adventure":     "Ditt äventyr",
	"Ready when you are": "Redo när du är",
	"Walk, look around, and solve your way through — at your own pace. Tap below when you're ready to begin.": "Gå, titta dig omkring och lös dig fram – i din egen takt. Tryck nedan när du är redo att börja.",
	"Start the adventure →":                            "Starta äventyret →",
	"Playing as a group? Get everyone in, then begin.": "Är du inte ensam? Få med alla först, börja sedan.",
	"Everyone joins":                                   "Alla går med",
	"Each person opens the camera on their phone and points it at this square:": "Var och en öppnar kameran i sin telefon och riktar den mot den här rutan:",
	"QR code to join this team": "QR-kod för att gå med i laget",
	"Tap Start playing":         "Tryck på Börja",
	"On every phone — including yours — tap the button below. That's it, you're playing together.": "Tryck på knappen nedan i varje telefon – även din. Klart, nu är alla med.",
	"Start playing →":                              "Börja →",
	"Playing on your own? Just tap Start playing.": "Är du ensam? Tryck bara på Börja.",
	"This adventure has no player spots yet.":      "Äventyret har inga platser än.",
	"Add a team photo":                             "Lägg till ett lagfoto",
	"change":                                       "ändra",
	"+ photo":                                      "+ foto",
	"Remove photo":                                 "Ta bort foto",
	"Starts in":                                    "Tid till start",
	"day":                                          "dag",
	"days":                                         "dagar",
	"Tap your name to open your link":              "Tryck på ditt namn för att öppna din länk",
	"Each link is for one person. Don't pass yours on — your teammates have their own below.": "Varje länk är till en person. Skicka inte vidare din – dina lagkamrater har sina egna nedan.",
	"Open →":                           "Öppna →",
	"Playing on a phone · take over →": "Används på en mobil · ta över →",
	"No members on this team yet. Ask your arranger to add you.": "Laget har inga medlemmar än. Be arrangören lägga till dig.",
	"Share this team page": "Dela lagsidan",
	"Team page link":       "Länk till lagsidan",
	"Copy":                 "Kopiera",
	"QR code to this page": "QR-kod till den här sidan",
	"Teammates who scan this land here and tap their own name.":                "Lagkamrater som skannar den hamnar här och trycker på sitt eget namn.",
	"If your name isn't listed, ask the arranger to add you.":                  "Står inte ditt namn med? Be arrangören lägga till dig.",
	"Files from the organisers":                                                "Filer från arrangörerna",
	"Download them before you set off. They are also in the menu of the game.": "Ladda ner dem innan du ger dig av. De finns också i menyn.",
	"Everyone's already joined this team. Ask whoever's hosting.":              "Alla har redan gått med i laget. Fråga den som håller i rundan.",
	"This game can't start just now. Ask the organiser, then try again.":       "Rundan kan inte starta just nu. Fråga arrangören och försök sedan igen.",
	"Couldn't join — please try again.":                                        "Det gick inte att gå med – försök igen.",
	"Couldn't reach the server. Check your connection.":                        "Når inte servern. Kontrollera din anslutning.",
	"upload failed":            "uppladdningen misslyckades",
	"remove failed":            "det gick inte att ta bort",
	"Link copied":              "Länken är kopierad",
	"Press Ctrl/Cmd+C to copy": "Tryck Ctrl/Cmd+C för att kopiera",
	"Event":                    "Eventet",
	"Completed":                "Avslutat",
	"Paused":                   "Pausat",
	"Live now":                 "Pågår nu",
	"Started ":                 "Startade ",
	"Starting…":                "Startar…",
	"Awaiting start":           "Väntar på start",
}

// teamET is the team page in Estonian, by its English words. Upstream
// has no Estonian; this fork's guests are in Estonia, and this page is
// the first thing they open.
var teamET = map[string]string{
	"Skip to start":      "Mine sisu juurde",
	"Your adventure":     "Sinu seiklus",
	"Ready when you are": "Alusta, kui oled valmis",
	"Walk, look around, and solve your way through — at your own pace. Tap below when you're ready to begin.": "Kõnni, vaata ringi ja lahenda ülesanded – omas tempos. Kui oled valmis, puuduta allolevat nuppu.",
	"Start the adventure →":                            "Alusta seiklust →",
	"Playing as a group? Get everyone in, then begin.": "Mängid koos teistega? Too kõik mängu ja alusta siis.",
	"Everyone joins":                                   "Kõik liituvad",
	"Each person opens the camera on their phone and points it at this square:": "Igaüks avab oma telefoni kaamera ja suunab selle sellele ruudule:",
	"QR code to join this team": "QR-kood sellesse võistkonda liitumiseks",
	"Tap Start playing":         "Puuduta nuppu „Alusta mängu“",
	"On every phone — including yours — tap the button below. That's it, you're playing together.": "Puudutage igas telefonis – ka sinu omas – allolevat nuppu. Ongi kõik, mängite koos.",
	"Start playing →":                              "Alusta mängu →",
	"Playing on your own? Just tap Start playing.": "Mängid üksi? Puuduta lihtsalt nuppu „Alusta mängu“.",
	"This adventure has no player spots yet.":      "Selles seikluses pole veel mängijakohti.",
	"Add a team photo":                             "Lisa võistkonna foto",
	"change":                                       "muuda",
	"+ photo":                                      "+ foto",
	"Remove photo":                                 "Eemalda foto",
	"Starts in":                                    "Alguseni",
	"day":                                          "päev",
	"days":                                         "päeva",
	"Tap your name to open your link":              "Puuduta oma nime, et oma link avada",
	"Each link is for one person. Don't pass yours on — your teammates have their own below.": "Iga link on ühele inimesele. Ära anna oma linki edasi – võistkonnakaaslaste lingid on allpool.",
	"Open →":                           "Ava →",
	"Playing on a phone · take over →": "Kasutusel ühes telefonis · võta üle →",
	"No members on this team yet. Ask your arranger to add you.": "Selles võistkonnas pole veel liikmeid. Palu korraldajal sind lisada.",
	"Share this team page": "Jaga võistkonna lehte",
	"Team page link":       "Võistkonna lehe link",
	"Copy":                 "Kopeeri",
	"QR code to this page": "Selle lehe QR-kood",
	"Teammates who scan this land here and tap their own name.":                "Võistkonnakaaslased, kes selle skannivad, jõuavad siia ja puudutavad oma nime.",
	"If your name isn't listed, ask the arranger to add you.":                  "Kui sinu nime pole loendis, palu korraldajal sind lisada.",
	"Files from the organisers":                                                "Failid korraldajatelt",
	"Download them before you set off. They are also in the menu of the game.": "Laadi need alla enne teeleasumist. Need on ka mängu menüüs.",
	"Everyone's already joined this team. Ask whoever's hosting.":              "Kõik on sellesse võistkonda juba liitunud. Küsi mängujuhilt.",
	"This game can't start just now. Ask the organiser, then try again.":       "Seda mängu ei saa praegu alustada. Küsi korraldajalt ja proovi siis uuesti.",
	"Couldn't join — please try again.":                                        "Liitumine ebaõnnestus – proovi uuesti.",
	"Couldn't reach the server. Check your connection.":                        "Serveriga ei saanud ühendust. Kontrolli ühendust.",
	"upload failed":            "üleslaadimine ebaõnnestus",
	"remove failed":            "eemaldamine ebaõnnestus",
	"Link copied":              "Link on kopeeritud",
	"Press Ctrl/Cmd+C to copy": "Kopeerimiseks vajuta Ctrl/Cmd+C",
	"Event":                    "Üritus",
	"Completed":                "Lõppenud",
	"Paused":                   "Peatatud",
	"Live now":                 "Käib praegu",
	"Started ":                 "Algas ",
	"Starting…":                "Algab…",
	"Awaiting start":           "Ootab algust",
}

// teamWords are the team page's words by language, English being the keys.
var teamWords = map[string]map[string]string{"sv": teamSV, "et": teamET}

// teamT is the team page's words in lang: Swedish or Estonian where
// there are any, else as written.
func teamT(lang, s string) string {
	if v, ok := teamWords[lang][s]; ok {
		return v
	}
	return s
}

// teamShareTmpl renders a tiny standalone page — no app-wide shell.
// The target audience is the team captain on a phone, so touch
// targets are generous and nothing requires JS. Escaping goes through
// html/template so team + member names can't break out of attributes.
var teamShareTmpl = template.Must(template.New("team").Funcs(template.FuncMap{"t": teamT}).Parse(`<!doctype html>
<html lang="{{.Lang}}"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>{{.TeamName}} · {{.SiteName}}</title>
<meta name="robots" content="noindex">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<meta name="theme-color" content="#0b1020">
<link rel="stylesheet" href="/assets/fontface.css">
<style>
  :root{
    --bg:#0b1020; --ink:#eef2ff; --ink-soft:#aab3d4; --ink-dim:#7e87ab;
    --accent:#ff5c8a; --accent2:#7c5cff; --gold:#ffd36b;
    --card:rgba(20,28,55,.62); --border:rgba(255,255,255,.14); --surface:rgba(8,12,28,.6);
  }
  *{box-sizing:border-box}
  html,body{min-height:100%}
  body{margin:0;color:var(--ink);background:var(--bg);
    font-family:"Open Sans",system-ui,-apple-system,Segoe UI,Roboto,sans-serif;line-height:1.6;
    -webkit-font-smoothing:antialiased;overflow-x:hidden;}

  .sky{position:fixed;inset:0;z-index:-3;overflow:hidden;
    background:radial-gradient(130% 100% at 50% -10%, #1a2550 0%, var(--bg) 60%, #070a16 100%);}
  .aurora{position:absolute;width:70vmax;height:70vmax;border-radius:50%;filter:blur(70px);opacity:.4;mix-blend-mode:screen;will-change:transform;}
  .a1{background:radial-gradient(circle,var(--accent2),transparent 62%);top:-22vmax;left:-18vmax;animation:drift1 26s ease-in-out infinite;}
  .a2{background:radial-gradient(circle,var(--accent),transparent 62%);bottom:-26vmax;right:-20vmax;animation:drift2 32s ease-in-out infinite;}
  .a3{background:radial-gradient(circle,#2bd4ff,transparent 62%);top:30%;right:-24vmax;opacity:.24;animation:drift1 38s ease-in-out infinite reverse;}
  @keyframes drift1{0%,100%{transform:translate3d(0,0,0) scale(1)}50%{transform:translate3d(6vmax,4vmax,0) scale(1.12)}}
  @keyframes drift2{0%,100%{transform:translate3d(0,0,0) scale(1.05)}50%{transform:translate3d(-7vmax,-3vmax,0) scale(.92)}}
  .stars{position:fixed;inset:0;z-index:-2;pointer-events:none;}
  .star{position:absolute;width:2px;height:2px;border-radius:50%;background:#fff;opacity:0;animation:twinkle linear infinite;}
  @keyframes twinkle{0%{opacity:0;transform:translateY(0)}10%{opacity:.9}50%{opacity:.3}90%{opacity:.8}100%{opacity:0;transform:translateY(-16px)}}

  .blackout{position:fixed;inset:0;z-index:999;background:#000;opacity:0;pointer-events:none;transition:opacity .72s ease;}
  .blackout.on{opacity:1;pointer-events:auto;}

  .wrap{position:relative;min-height:100dvh;display:grid;place-items:center;
    padding:max(1.5rem,env(safe-area-inset-top)) 1.15rem max(1.75rem,calc(env(safe-area-inset-bottom) + .5rem));}
  .stage{width:100%;max-width:30rem;text-align:center;animation:rise .9s cubic-bezier(.2,.8,.2,1) both;}
  .stage.leaving{animation:leave .72s ease forwards;}
  @keyframes rise{from{opacity:0;transform:translateY(22px)}to{opacity:1;transform:none}}
  @keyframes leave{to{opacity:0;transform:translateY(-14px) scale(.985)}}

  .mark{display:inline-flex;align-items:center;gap:.55rem;margin-bottom:1.1rem;color:var(--ink-soft);
    font-weight:600;letter-spacing:.14em;text-transform:uppercase;font-size:.72rem;}
  .mark .dot{width:.5rem;height:.5rem;border-radius:50%;background:var(--gold);box-shadow:0 0 12px 2px var(--gold);animation:beat 2.4s ease-in-out infinite;}
  @keyframes beat{0%,100%{transform:scale(1);opacity:.85}50%{transform:scale(1.4);opacity:1}}

  .compass{width:76px;height:76px;margin:0 auto 1rem;animation:float 6s ease-in-out infinite;}
  @keyframes float{0%,100%{transform:translateY(0)}50%{transform:translateY(-8px)}}
  .compass svg{width:100%;height:100%;display:block;filter:drop-shadow(0 6px 18px rgba(124,92,255,.5));}
  .compass .ring{transform-origin:50% 50%;animation:spin 24s linear infinite;}
  @keyframes spin{to{transform:rotate(360deg)}}
  .compass .needle{transform-origin:50% 50%;animation:wobble 5s ease-in-out infinite;}
  @keyframes wobble{0%,100%{transform:rotate(-12deg)}50%{transform:rotate(16deg)}}

  h1{font-family:"Aclonica",system-ui,sans-serif;font-weight:400;font-size:clamp(1.8rem,7vw,2.7rem);line-height:1.08;margin:.1rem 0 .4rem;
    background:linear-gradient(100deg,#fff 10%,var(--gold) 50%,#fff 90%);-webkit-background-clip:text;background-clip:text;color:transparent;background-size:200% auto;animation:sheen 6s linear infinite;}
  @keyframes sheen{to{background-position:200% center}}
  .sub{color:var(--ink-soft);margin:0 auto 1.5rem;max-width:24rem;font-size:1rem;}
  .sub .team{color:var(--ink);font-weight:700;}

  .card{background:var(--card);border:1px solid var(--border);border-radius:20px;padding:1.3rem 1.15rem;margin:0 0 1rem;
    backdrop-filter:blur(14px) saturate(140%);-webkit-backdrop-filter:blur(14px) saturate(140%);
    box-shadow:0 24px 60px -24px rgba(0,0,0,.7),inset 0 1px 0 rgba(255,255,255,.08);}
  .lead{margin:0 0 1.1rem;color:var(--ink-soft);}
  .label{margin:0 0 .25rem;font-weight:700;color:var(--ink);text-align:left;}
  .note{margin:.1rem 0 1rem;color:var(--ink-dim);font-size:.86rem;text-align:left;}

  .go{display:block;width:100%;padding:1.05rem 1.2rem;font-size:1.08rem;font-weight:800;color:#1a0f1e;border:0;border-radius:14px;cursor:pointer;text-decoration:none;position:relative;overflow:hidden;
    background:linear-gradient(100deg,var(--gold),var(--accent));background-size:180% auto;transition:transform .12s ease,box-shadow .2s,background-position .4s;box-shadow:0 12px 30px -10px rgba(255,92,138,.6);}
  .go:hover{background-position:100% center;transform:translateY(-1px);}
  .go:active{transform:translateY(1px) scale(.99);}

  .slot{display:flex;align-items:center;justify-content:space-between;gap:.75rem;text-align:left;
    padding:.95rem 1.1rem;margin:.55rem 0;background:var(--surface);border:1px solid var(--border);border-radius:14px;
    text-decoration:none;color:var(--ink);transition:border-color .18s,transform .12s,box-shadow .18s;}
  .slot:hover{border-color:var(--accent);transform:translateY(-1px);box-shadow:0 10px 26px -14px rgba(255,92,138,.6);}
  .slot:active{transform:translateY(1px);}
  .slot .name{font-weight:700;font-size:1.05rem;}
  .slot .go-sm{color:var(--gold);font-weight:700;font-size:.9rem;white-space:nowrap;}
  .slot.is-taken{opacity:.6;}
  .slot.is-taken .go-sm{color:var(--ink-dim);white-space:normal;text-align:right;}
  .file .name{overflow-wrap:anywhere;min-width:0;}

  details.share{margin:.2rem 0 0;border:1px solid var(--border);border-radius:14px;background:var(--surface);overflow:hidden;}
  details.share summary{cursor:pointer;list-style:none;padding:.85rem 1.1rem;font-weight:600;color:var(--ink-soft);font-size:.9rem;}
  details.share summary::-webkit-details-marker{display:none;}
  details.share summary::before{content:'▸';display:inline-block;margin-right:.45rem;transition:transform .15s ease;color:var(--ink-dim);}
  details.share[open] summary::before{transform:rotate(90deg);}
  .share-body{padding:0 1.1rem 1.1rem;}
  .copy-row{display:flex;gap:.5rem;margin:0 0 .9rem;}
  .copy-row input{flex:1;min-width:0;padding:.6rem .7rem;border:1px solid var(--border);border-radius:10px;background:rgba(8,12,28,.6);color:var(--ink);font:inherit;font-size:.82rem;}
  .copy-row button{padding:.6rem .9rem;border-radius:10px;border:0;background:linear-gradient(100deg,var(--gold),var(--accent));color:#1a0f1e;font:inherit;font-weight:700;cursor:pointer;}
  .qr-body{text-align:center;}
  .qr-body img{width:220px;height:220px;max-width:100%;image-rendering:pixelated;background:#fff;padding:.4rem;border-radius:12px;}
  .qr-body p{color:var(--ink-dim);font-size:.82rem;margin:.6rem 0 0;}

  .cd{margin:0 0 1rem;padding:.85rem 1rem;background:var(--card);border:1px solid var(--border);border-radius:14px;text-align:center;
    backdrop-filter:blur(14px);-webkit-backdrop-filter:blur(14px);}
  .cd .cd-label{color:var(--ink-dim);text-transform:uppercase;letter-spacing:.12em;font-size:.7rem;font-weight:700;margin:0 0 .3rem;}
  .cd .cd-value{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-variant-numeric:tabular-nums;font-size:1.6rem;font-weight:800;color:var(--ink);margin:0;}
  .cd.live .cd-value{color:var(--gold);}
  .cd .cd-when{color:var(--ink-dim);font-size:.8rem;margin:.25rem 0 0;}

  .avatar-wrap{display:flex;flex-direction:column;align-items:center;margin:0 0 1.1rem;}
  .avatar-btn{position:relative;width:84px;height:84px;border-radius:50%;background:linear-gradient(135deg,var(--accent2),var(--accent));color:#fff;border:0;cursor:pointer;display:grid;place-items:center;
    font-family:"Open Sans",system-ui,sans-serif;font-size:1.8rem;font-weight:800;letter-spacing:.04em;text-transform:uppercase;box-shadow:0 10px 26px -10px rgba(124,92,255,.7);transition:transform .12s;padding:0;}
  .avatar-btn:hover{transform:translateY(-1px);} .avatar-btn:active{transform:translateY(1px);}
  .avatar-btn img{width:100%;height:100%;object-fit:cover;display:block;border-radius:50%;}
  .avatar-btn .hint-pill{position:absolute;bottom:-2px;right:-2px;background:#141c37;color:var(--ink-soft);border:2px solid var(--accent);border-radius:999px;padding:.12rem .45rem;font-size:.66rem;font-weight:700;white-space:nowrap;}
  .avatar-remove{display:block;margin:.4rem auto 0;background:none;border:none;color:var(--ink-dim);font:inherit;font-size:.78rem;cursor:pointer;padding:.2rem .5rem;}
  .avatar-remove:hover{color:var(--accent);} .avatar-remove[hidden]{display:none;}
  .avatar-err{color:#ff6b7a;font-size:.82rem;text-align:center;margin:.25rem 0 0;min-height:1em;}

  .empty{color:var(--ink-dim);padding:1.5rem 0;}
  .footer{color:var(--ink-dim);font-size:.78rem;margin-top:1.3rem;}
  .copy-toast{position:fixed;left:50%;bottom:4rem;transform:translateX(-50%);background:#000;color:#fff;padding:.55rem .9rem;border-radius:999px;font-size:.85rem;opacity:0;transition:opacity .2s ease;pointer-events:none;}
  .copy-toast.show{opacity:1;}

  /* Self-guided "scan to join" steps — big, numbered, inexperienced-friendly. */
  .steps{list-style:none;margin:0 0 1.2rem;padding:0;text-align:left;}
  .steps li{display:flex;gap:.85rem;align-items:flex-start;margin:0 0 1.3rem;}
  .steps li:last-child{margin-bottom:0;}
  .snum{flex:0 0 auto;width:2rem;height:2rem;border-radius:50%;display:grid;place-items:center;font-weight:800;font-size:1rem;color:#1a0f1e;background:linear-gradient(135deg,var(--gold),var(--accent));box-shadow:0 6px 16px -6px rgba(255,92,138,.7);}
  .sbody{flex:1;min-width:0;}
  .shead{margin:.2rem 0 .15rem;font-weight:800;font-size:1.1rem;color:var(--ink);}
  .stext{margin:0 0 .7rem;color:var(--ink-soft);font-size:.96rem;}
  .qrbox{background:#fff;border-radius:16px;padding:.9rem;display:inline-block;box-shadow:0 12px 34px -12px rgba(0,0,0,.65);}
  .qr-big{display:block;width:210px;height:210px;max-width:62vw;image-rendering:pixelated;}
  .join-btn{margin-top:.1rem;}
  .joinmsg{min-height:1.2rem;margin:.7rem 0 0;font-size:.92rem;font-weight:600;color:#ff6b7a;text-align:center;}

  .visually-hidden{position:absolute !important;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0;}
  .skip-link{position:fixed;top:0;left:.5rem;transform:translateY(-150%);z-index:10000;padding:.5rem 1rem;background:var(--accent);color:#fff;border-radius:8px;font-weight:700;text-decoration:none;transition:transform 120ms ease;}
  .skip-link:focus,.skip-link:focus-visible{transform:translateY(.5rem);}
  :focus-visible{outline:3px solid color-mix(in srgb,var(--accent) 55%,transparent);outline-offset:2px;}

  @media (prefers-reduced-motion:reduce){
    .aurora,.star,.compass,.compass .ring,.compass .needle,h1,.mark .dot,.stage{animation:none !important;}
    .stage.leaving{animation:none;opacity:0;}
    .blackout{transition-duration:.001s;}
    h1{background-position:0 center;}
  }
</style></head>
<body>
<a class="skip-link" href="#main">{{t $.Lang "Skip to start"}}</a>
<div class="sky" aria-hidden="true"><div class="aurora a1"></div><div class="aurora a2"></div><div class="aurora a3"></div></div>
<div class="stars" id="stars" aria-hidden="true"></div>
<div class="blackout" id="blackout" aria-hidden="true"></div>

<main id="main" class="wrap" tabindex="-1">
  <div class="stage">
    <div class="mark"><span class="dot"></span> {{.SiteName}}</div>

    <div class="compass" aria-hidden="true">
      <svg viewBox="0 0 100 100" fill="none">
        <circle class="ring" cx="50" cy="50" r="42" stroke="url(#g1)" stroke-width="3" stroke-dasharray="4 10" stroke-linecap="round"/>
        <circle cx="50" cy="50" r="34" stroke="rgba(255,255,255,.18)" stroke-width="1.5"/>
        <g class="needle"><path d="M50 20 L58 52 L50 46 L42 52 Z" fill="url(#g2)"/><path d="M50 80 L42 48 L50 54 L58 48 Z" fill="rgba(255,255,255,.35)"/></g>
        <circle cx="50" cy="50" r="4" fill="#fff"/>
        <defs>
          <linearGradient id="g1" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#7c5cff"/><stop offset="1" stop-color="#ff5c8a"/></linearGradient>
          <linearGradient id="g2" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#ffd36b"/><stop offset="1" stop-color="#ff5c8a"/></linearGradient>
        </defs>
      </svg>
    </div>

{{if .SelfGuided}}
    {{/* ---------- Self-guided: casual, start-first ---------- */}}
    <h1>{{if .AdventureTitle}}{{.AdventureTitle}}{{else}}{{t $.Lang "Your adventure"}}{{end}}</h1>

    {{if .Solo}}
    <p class="sub">{{t $.Lang "Ready when you are"}}</p>
    <div class="card">
      <p class="lead">{{t $.Lang "Walk, look around, and solve your way through — at your own pace. Tap below when you're ready to begin."}}</p>
      <a class="go slot-go" href="{{(index .Members 0).MagicURL}}">{{t $.Lang "Start the adventure →"}}</a>
    </div>
    {{template "files" .}}
    {{else if .Members}}
    <p class="sub">{{t $.Lang "Playing as a group? Get everyone in, then begin."}}</p>
    <div class="card">
      <ol class="steps">
        <li>
          <span class="snum" aria-hidden="true">1</span>
          <div class="sbody">
            <p class="shead">{{t $.Lang "Everyone joins"}}</p>
            <p class="stext">{{t $.Lang "Each person opens the camera on their phone and points it at this square:"}}</p>
            <div class="qrbox"><img class="qr-big" src="/qr/team/{{.Token}}" alt="{{t $.Lang "QR code to join this team"}}"></div>
          </div>
        </li>
        <li>
          <span class="snum" aria-hidden="true">2</span>
          <div class="sbody">
            <p class="shead">{{t $.Lang "Tap Start playing"}}</p>
            <p class="stext">{{t $.Lang "On every phone — including yours — tap the button below. That's it, you're playing together."}}</p>
          </div>
        </li>
      </ol>
      <button type="button" class="go join-btn" id="join-btn" data-token="{{.Token}}">{{t $.Lang "Start playing →"}}</button>
      <p class="joinmsg" id="join-msg" role="status" aria-live="polite"></p>
    </div>
    <p class="hint">{{t $.Lang "Playing on your own? Just tap Start playing."}}</p>
    {{template "files" .}}
    {{else}}
    <p class="sub">{{t $.Lang "Ready when you are"}}</p>
    <div class="card"><p class="empty">{{t $.Lang "This adventure has no player spots yet."}}</p></div>
    {{end}}

{{else}}
    {{/* ---------- Hosted: team captain, scheduled event ---------- */}}
    <h1>{{.TeamName}}</h1>
    {{if .AdventureTitle}}<p class="sub">{{.AdventureTitle}}</p>{{end}}

    <div class="avatar-wrap">
      <button type="button" id="avatar-btn" class="avatar-btn" aria-label="{{t $.Lang "Add a team photo"}}">
        {{if .AvatarURL}}<img id="avatar-img" src="{{.AvatarURL}}" alt="">{{else}}<span id="avatar-ini"></span>{{end}}
        <span class="hint-pill">{{if .AvatarURL}}{{t $.Lang "change"}}{{else}}{{t $.Lang "+ photo"}}{{end}}</span>
      </button>
      <button type="button" id="avatar-remove" class="avatar-remove"{{if not .AvatarURL}} hidden{{end}}>{{t $.Lang "Remove photo"}}</button>
      <p id="avatar-err" class="avatar-err"></p>
      <input type="file" id="avatar-file" accept="image/*" style="display:none">
    </div>

    <div id="cd" class="cd" hidden>
      <p class="cd-label" id="cd-label">{{t $.Lang "Starts in"}}</p>
      <p class="cd-value" id="cd-value">—</p>
      <p class="cd-when" id="cd-when"></p>
    </div>

    <div class="card">
      <p class="label">{{t $.Lang "Tap your name to open your link"}}</p>
      <p class="note">{{t $.Lang "Each link is for one person. Don't pass yours on — your teammates have their own below."}}</p>
      {{if .Members}}
        {{range .Members}}
        <a class="slot slot-go{{if .Taken}} is-taken{{end}}" href="{{.MagicURL}}"><span class="name">{{.Name}}</span><span class="go-sm">{{if .Taken}}{{t $.Lang "Playing on a phone · take over →"}}{{else}}{{t $.Lang "Open →"}}{{end}}</span></a>
        {{end}}
      {{else}}
        <p class="empty">{{t $.Lang "No members on this team yet. Ask your arranger to add you."}}</p>
      {{end}}
    </div>
    {{template "files" .}}

    <details class="share">
      <summary>{{t $.Lang "Share this team page"}}</summary>
      <div class="share-body">
        <div class="copy-row">
          <input id="team-url" type="text" readonly aria-label="{{t $.Lang "Team page link"}}">
          <button type="button" id="copy-team-url">{{t $.Lang "Copy"}}</button>
        </div>
        <div class="qr-body">
          <img src="/qr/team/{{.Token}}" alt="{{t $.Lang "QR code to this page"}}" loading="lazy">
          <p>{{t $.Lang "Teammates who scan this land here and tap their own name."}}</p>
        </div>
      </div>
    </details>

    <p class="footer">{{t $.Lang "If your name isn't listed, ask the arranger to add you."}}</p>
{{end}}
  </div>
</main>

<div id="copy-toast" class="copy-toast" role="status" aria-live="polite"></div>

<script>
(function () {
  var token = {{.Token}};
  var teamName = {{.TeamName}};
  var eventState = {{.State}};
  var scheduledAt = {{.ScheduledAt}};
  var startedAt = {{.StartedAt}};

  // Drifting stars (skipped under reduced motion).
  try{
    if(!matchMedia("(prefers-reduced-motion: reduce)").matches){
      var box=document.getElementById("stars"), f=document.createDocumentFragment();
      for(var i=0;i<30;i++){var s=document.createElement("div");s.className="star";
        s.style.left=(Math.random()*100)+"%";s.style.top=(Math.random()*100)+"%";
        s.style.animationDuration=(3+Math.random()*5)+"s";s.style.animationDelay=(Math.random()*6)+"s";
        s.style.opacity=String(.3+Math.random()*.6);f.appendChild(s);}
      box.appendChild(f);
    }
  }catch(e){}

  // Fade-to-black handoff, matching /play.
  var reduce=false; try{ reduce=matchMedia("(prefers-reduced-motion: reduce)").matches; }catch(e){}
  function handoff(href){
    document.querySelector(".stage").classList.add("leaving");
    document.getElementById("blackout").classList.add("on");
    setTimeout(function(){ location.href=href; }, reduce?40:620);
  }
  // Solo "Start" (and any direct seat link) opens a magic link.
  Array.prototype.forEach.call(document.querySelectorAll("a.slot-go"), function(a){
    a.addEventListener("click", function(ev){
      if(ev.metaKey||ev.ctrlKey||ev.shiftKey||ev.button) return;
      ev.preventDefault();
      handoff(a.getAttribute("href"));
    });
  });
  // Group "Start playing" — take a free seat (scan-to-join), then go.
  var joinBtn=document.getElementById("join-btn");
  var joinMsg=document.getElementById("join-msg");
  if(joinBtn){
    var joining=false;
    joinBtn.addEventListener("click", function(){
      if(joining) return; joining=true;
      if(joinMsg) joinMsg.textContent="";
      joinBtn.disabled=true;
      fetch("/api/public/team/"+encodeURIComponent(joinBtn.getAttribute("data-token"))+"/join",
        {method:"POST",credentials:"same-origin"})
      .then(function(r){ return r.json().then(function(b){ return {s:r.status,b:b}; }); })
      .then(function(res){
        if(res.s===200 && res.b && res.b.redirect){ handoff(res.b.redirect); return; }
        joining=false; joinBtn.disabled=false;
        if(res.s===409){ if(joinMsg) joinMsg.textContent={{t $.Lang "Everyone's already joined this team. Ask whoever's hosting."}}; }
        else if(res.s===402){ if(joinMsg) joinMsg.textContent={{t $.Lang "This game can't start just now. Ask the organiser, then try again."}}; }
        else { if(joinMsg) joinMsg.textContent=(res.b&&res.b.error)||{{t $.Lang "Couldn't join — please try again."}}; }
      })
      .catch(function(){ joining=false; joinBtn.disabled=false; if(joinMsg) joinMsg.textContent={{t $.Lang "Couldn't reach the server. Check your connection."}}; });
    });
  }

  // Team photo upload / remove (token is the auth).
  var btn=document.getElementById("avatar-btn");
  var file=document.getElementById("avatar-file");
  var removeBtn=document.getElementById("avatar-remove");
  var errEl=document.getElementById("avatar-err");
  var iniEl=document.getElementById("avatar-ini");
  if(iniEl) iniEl.textContent=initials(teamName);
  if(btn && file){
    btn.addEventListener("click",function(){file.click();});
    file.addEventListener("change",function(){
      var fobj=file.files&&file.files[0]; if(!fobj) return; errEl.textContent="";
      var fd=new FormData(); fd.append("file",fobj);
      fetch("/api/public/team/"+encodeURIComponent(token)+"/avatar",{method:"POST",body:fd,credentials:"same-origin"})
      .then(function(res){if(!res.ok)return res.text().then(function(t){throw new Error(t||("error "+res.status));});return res.json();})
      .then(function(body){var url=(body.url||("/adventure-assets/team-avatar/"+Date.now()))+"?v="+Date.now();paintPhoto(url);if(removeBtn)removeBtn.hidden=false;})
      .catch(function(e){errEl.textContent=e.message||{{t $.Lang "upload failed"}};})
      .then(function(){file.value="";});
    });
  }
  if(removeBtn){
    removeBtn.addEventListener("click",function(){
      errEl.textContent="";
      fetch("/api/public/team/"+encodeURIComponent(token)+"/avatar",{method:"DELETE",credentials:"same-origin"})
      .then(function(res){if(res.status!==204&&!res.ok)throw new Error("error "+res.status);paintInitials();removeBtn.hidden=true;})
      .catch(function(e){errEl.textContent=e.message||{{t $.Lang "remove failed"}};});
    });
  }
  function paintPhoto(url){if(!btn)return;btn.innerHTML="";var img=new Image();img.id="avatar-img";img.alt="";img.src=url;btn.appendChild(img);var pill=document.createElement("span");pill.className="hint-pill";pill.textContent={{t $.Lang "change"}};btn.appendChild(pill);}
  function paintInitials(){if(!btn)return;btn.innerHTML="";var span=document.createElement("span");span.id="avatar-ini";span.textContent=initials(teamName);btn.appendChild(span);var pill=document.createElement("span");pill.className="hint-pill";pill.textContent={{t $.Lang "+ photo"}};btn.appendChild(pill);}
  function initials(name){var p=(name||"").trim().split(/\s+/).filter(Boolean);if(p.length===0)return "?";if(p.length===1)return p[0].slice(0,2).toUpperCase();return (p[0][0]+p[p.length-1][0]).toUpperCase();}

  // Copy team link.
  var urlEl=document.getElementById("team-url");
  var copyBtn=document.getElementById("copy-team-url");
  var toast=document.getElementById("copy-toast");
  if(urlEl) urlEl.value=location.href;
  function flashToast(m){if(!toast)return;toast.textContent=m;toast.classList.add("show");setTimeout(function(){toast.classList.remove("show");},1800);}
  if(copyBtn&&urlEl){copyBtn.addEventListener("click",function(){var u=urlEl.value;var ok=function(){flashToast({{t $.Lang "Link copied"}});};var fail=function(){urlEl.select();flashToast({{t $.Lang "Press Ctrl/Cmd+C to copy"}});};if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(u).then(ok,fail);}else{fail();}});}

  // Countdown (hosted view only; the element isn't rendered otherwise).
  var cd=document.getElementById("cd");
  if(cd){
    var cdLabel=document.getElementById("cd-label"),cdValue=document.getElementById("cd-value"),cdWhen=document.getElementById("cd-when");
    function parseUTC(s){if(!s)return null;var iso=s.indexOf("T")>=0?s:s.replace(" ","T")+"Z";var d=new Date(iso);return isNaN(d.getTime())?null:d;}
    function pad(n){return n<10?"0"+n:""+n;}
    function fmtCountdown(sec){if(sec<=0)return "00:00";var d=Math.floor(sec/86400),h=Math.floor((sec%86400)/3600),m=Math.floor((sec%3600)/60),s=sec%60;if(d>0)return d+" "+(d===1?{{t $.Lang "day"}}:{{t $.Lang "days"}})+" "+pad(h)+":"+pad(m)+":"+pad(s);if(h>0)return pad(h)+":"+pad(m)+":"+pad(s);return pad(m)+":"+pad(s);}
    function fmtClock(d){return d.toLocaleString(document.documentElement.lang||undefined,{dateStyle:"medium",timeStyle:"short",hour12:false});}
    if(eventState==="completed"){cd.hidden=false;cdLabel.textContent={{t $.Lang "Event"}};cdValue.textContent={{t $.Lang "Completed"}};}
    else if(eventState==="running"||eventState==="paused"){cd.hidden=false;cd.classList.add("live");cdLabel.textContent={{t $.Lang "Event"}};cdValue.textContent=eventState==="paused"?{{t $.Lang "Paused"}}:{{t $.Lang "Live now"}};var sd=parseUTC(startedAt);cdWhen.textContent=sd?{{t $.Lang "Started "}}+fmtClock(sd):"";}
    else{var when=parseUTC(scheduledAt);if(when){cd.hidden=false;cdLabel.textContent={{t $.Lang "Starts in"}};cdWhen.textContent=fmtClock(when);var tick=function(){var sec=Math.round((when.getTime()-Date.now())/1000);if(sec<=0){cd.classList.add("live");cdLabel.textContent={{t $.Lang "Event"}};cdValue.textContent={{t $.Lang "Starting…"}};cdWhen.textContent="";return false;}cdValue.textContent=fmtCountdown(sec);return true;};if(tick()){var iv=setInterval(function(){if(!tick())clearInterval(iv);},1000);}}else{cd.hidden=false;cdLabel.textContent={{t $.Lang "Event"}};cdValue.textContent={{t $.Lang "Awaiting start"}};}}
  }
})();
</script>
</body></html>
{{define "files"}}{{if .Files}}
    <div class="card">
      <p class="label">{{t .Lang "Files from the organisers"}}</p>
      <p class="note">{{t .Lang "Download them before you set off. They are also in the menu of the game."}}</p>
      {{range .Files}}
      <a class="slot file" href="{{.URL}}" download="{{.Name}}"><span class="name">{{.Name}}</span><span class="go-sm">{{.Size}} ↓</span></a>
      {{end}}
    </div>
{{end}}{{end}}`))

// TeamFile downloads one of the files the organisers left for the
// team's event, by the team's share token: the links in the ticket and
// invitation emails, for a player not yet signed in to the game.
//
// GET /team/{token}/files/{fileID}
func (h *Handlers) TeamFile(w http.ResponseWriter, r *http.Request) {
	_, eventID, ok := h.teamByToken(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "fileID"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(w, http.StatusNotFound, "no such file")
		return
	}
	f, content, err := eventfile.GetForPlayers(r.Context(), h.DB, eventID, id)
	switch {
	case errors.Is(err, eventfile.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "no such file")
		return
	case err != nil:
		httpx.ServerErr(w, http.StatusInternalServerError, "reading file failed", err)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	eventfile.Serve(w, f, content)
}

// TeamPrint downloads a print material the event emails to its buyers,
// by the team's token: the link in the team's email. It is in the team's
// language when there is one, and stops working once the team is
// withdrawn. A material the event posts or keeps for organisers is not
// the team's to fetch.
func (h *Handlers) TeamPrint(w http.ResponseWriter, r *http.Request) {
	var (
		eventID   int64
		lang      string
		withdrawn bool
	)
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT event_id, lang, withdrawn_at IS NOT NULL FROM teams WHERE token = $1
	`, chi.URLParam(r, "token")).Scan(&eventID, &lang, &withdrawn)
	if errors.Is(err, sql.ErrNoRows) || err == nil && withdrawn {
		httpx.Error(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	g, o, err := printmat.ForEvent(r.Context(), h.DB, eventID)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	for _, pm := range printmat.Effective(g, o) {
		if pm.ID != chi.URLParam(r, "material") || pm.Delivery != graph.DeliveryEmailed {
			continue
		}
		_, id := printmat.File(pm, lang, g.Language)
		var content []byte
		if err := h.DB.QueryRowContext(r.Context(),
			`SELECT content FROM adventure_media WHERE id = $1 AND kind = 'pdf'`, id).Scan(&content); err != nil {
			break
		}
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		eventfile.Serve(w, eventfile.File{Name: pm.Name + ".pdf", Mime: "application/pdf"}, content)
		return
	}
	httpx.Error(w, http.StatusNotFound, "no such file")
}
