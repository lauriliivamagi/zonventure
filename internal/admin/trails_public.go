package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/klahr/zonventure/internal/auth"
	"github.com/klahr/zonventure/internal/emailtpl"
	"github.com/klahr/zonventure/internal/httpx"
	"github.com/klahr/zonventure/internal/logx"
	"github.com/klahr/zonventure/internal/settings"
	"github.com/klahr/zonventure/internal/tokenx"
	"github.com/klahr/zonventure/internal/trail"
)

// The public quiz trail: anyone makes a trail on a phone, with no
// account, at /o/{slug}/trail of an org that sells them
// (organizations.quiz_trail_enabled), and pays a hosted pass to play it.
// A trail is a hidden adventure of that org (adventures.trail_hidden),
// written in the same creator as the console's (trails.go), plus a
// trails row: the draft's secret, the buyer's email, the hosted event
// once paid.
//
// A draft is opened by its secret, which only the maker's browser holds
// (X-Trail-Secret), or by an email session (X-Trail-Session): a mailed
// link confirms an address and saves the browser's trails to it, or
// signs in to every trail saved there. Paying makes the hosted event
// (createHostedEvent) and mails its host link, from which the trail is
// edited too. A week into play the buyer may keep it: it moves into an
// account of theirs and the event goes. SweepTrails deletes what is
// left. See migration 0112.

const (
	// trailDraftDays is how long an unpaid draft is kept after its last
	// edit.
	trailDraftDays = 30
	// trailKeepAfter is how long into play the buyer is offered to keep
	// the trail.
	trailKeepAfter = 7 * 24 * time.Hour
	// maxTrailDraftsPerIP caps the drafts one address starts a day.
	maxTrailDraftsPerIP = 20
	// maxTrailMailsPerHour caps the confirm and sign-in links mailed to
	// one address, and from one IP, an hour.
	maxTrailMailsPerHour = 5
	// maxTrailSavesPerMinute caps the saves of one draft a minute; the
	// creator saves a second after the last change.
	maxTrailSavesPerMinute = 60
	trailLinkTTL           = time.Hour
	trailSessionTTL        = 30 * 24 * time.Hour
)

// hashTrailSecret is how a draft's secret and a mailed token are stored.
func hashTrailSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// trailIPHash counts what one address does without keeping it.
func trailIPHash(r *http.Request) string {
	return hashTrailSecret("trail-ip:" + auth.ClientIP(r))
}

/* ---------------- the start page ---------------- */

// trailStartDTO is what the start page shows: whether the org sells
// quiz trails, at what price, and the templates.
type trailStartDTO struct {
	OrgName      string             `json:"org_name"`
	OrgSlug      string             `json:"org_slug"`
	Enabled      bool               `json:"enabled"`
	PaymentsOpen bool               `json:"payments_open"`
	Price        hostedPriceDTO     `json:"price"`
	PlayDays     int                `json:"play_days"`
	StartDays    int                `json:"start_days"`
	Templates    []trailTemplateDTO `json:"templates"`
}

// trailOrg is the org in the URL, when it sells quiz trails.
func (h *Handlers) trailOrg(ctx context.Context, slug string) (id int64, name string, enabled bool, err error) {
	err = h.DB.QueryRowContext(ctx, `SELECT id, name, quiz_trail_enabled FROM organizations WHERE slug = $1`, slug).Scan(&id, &name, &enabled)
	return
}

// trailPaymentsOpen is whether a trail can be paid for now.
func (h *Handlers) trailPaymentsOpen() bool { return h.pricing().TeamCents <= 0 || h.paymentsEnabled() }

// GetTrailStart is the start page of an org's quiz trails.
//
// GET /api/o/{slug}/trail?lang=sv
func (h *Handlers) GetTrailStart(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	id, name, enabled, err := h.trailOrg(r.Context(), slug)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "no such organisation")
		return
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	_ = id
	out := trailStartDTO{OrgName: name, OrgSlug: slug, Enabled: enabled, PaymentsOpen: h.trailPaymentsOpen(),
		Price: h.hostedPrice(), PlayDays: int(HostedPlayPeriod.Hours() / 24), StartDays: int(PartyValidity.Hours() / 24),
		Templates: []trailTemplateDTO{}}
	lang := trailLang(r.URL.Query().Get("lang"))
	for _, n := range trail.TemplateNames {
		t, _ := trail.NewFromTemplate(n, lang)
		out.Templates = append(out.Templates, trailTemplateDTO{Name: n, Title: t.Title, Kids: t.Kids, Cards: len(t.Cards), Person: t.Person})
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, out)
}

/* ---------------- drafts ---------------- */

// publicTrail is a trails row with its adventure.
type publicTrail struct {
	ID          int64
	OrgID       int64
	AdvID       int64
	Slug        string
	SecretHash  string
	EventID     sql.NullInt64
	PaidAt      sql.NullTime
	Email       sql.NullString
	Confirmed   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	EventState  sql.NullString
	ValidUntil  sql.NullTime
	FirstGameAt sql.NullTime
}

const publicTrailSQL = `
	SELECT t.id, t.org_id, t.adventure_id, a.slug, t.secret_hash, t.event_id, t.paid_at, t.email,
	       t.email_confirmed_at IS NOT NULL, t.created_at, t.updated_at, e.state, e.valid_until, e.first_game_at
	FROM trails t JOIN adventures a ON a.id = t.adventure_id LEFT JOIN events e ON e.id = t.event_id`

func scanPublicTrail(row interface{ Scan(...any) error }) (publicTrail, error) {
	var p publicTrail
	err := row.Scan(&p.ID, &p.OrgID, &p.AdvID, &p.Slug, &p.SecretHash, &p.EventID, &p.PaidAt, &p.Email,
		&p.Confirmed, &p.CreatedAt, &p.UpdatedAt, &p.EventState, &p.ValidUntil, &p.FirstGameAt)
	return p, err
}

// State is "draft", "paid" (not played yet), "playing" or "ended".
func (p publicTrail) State() string {
	switch {
	case !p.PaidAt.Valid:
		return "draft"
	case !p.EventID.Valid || p.EventState.String == "completed" || p.EventState.String == "cancelled" ||
		(p.ValidUntil.Valid && p.ValidUntil.Time.Before(time.Now())):
		return "ended"
	case p.FirstGameAt.Valid:
		return "playing"
	}
	return "paid"
}

// maskEmail shows enough of an address to recognise it: "anna@…".
func maskEmail(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i+1] + "…"
	}
	return ""
}

// publicTrailDTO is the creator's view of a public trail: the console's,
// and where it stands.
type publicTrailDTO struct {
	trailDTO
	ID    int64  `json:"id"`
	State string `json:"state"`
	// Email is where the trail is saved, masked, once confirmed.
	Email        string         `json:"email,omitempty"`
	PaymentsOpen bool           `json:"payments_open"`
	Price        hostedPriceDTO `json:"price"`
	ValidUntil   *time.Time     `json:"valid_until,omitempty"`
	FirstGameAt  *time.Time     `json:"first_game_at,omitempty"`
	// RoundOpen is whether a game is being played: changes apply from
	// the next.
	RoundOpen bool `json:"round_open"`
}

func (h *Handlers) describePublicTrail(ctx context.Context, p publicTrail) (publicTrailDTO, error) {
	t, err := loadTrail(ctx, h.DB, p.Slug, false)
	if err != nil {
		return publicTrailDTO{}, err
	}
	if err := t.describe(ctx, h.DB); err != nil {
		return publicTrailDTO{}, err
	}
	out := publicTrailDTO{trailDTO: t.dto, ID: p.ID, State: p.State(), PaymentsOpen: h.trailPaymentsOpen(), Price: h.hostedPrice()}
	if p.Confirmed {
		out.Email = maskEmail(p.Email.String)
	}
	if p.ValidUntil.Valid {
		out.ValidUntil = &p.ValidUntil.Time
	}
	if p.FirstGameAt.Valid {
		out.FirstGameAt = &p.FirstGameAt.Time
	}
	if p.EventID.Valid {
		if err := h.DB.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM event_rounds r JOIN events e ON e.id = r.event_id
			               WHERE r.event_id = $1 AND r.round = e.current_round AND r.finished_at IS NULL)
		`, p.EventID.Int64).Scan(&out.RoundOpen); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (h *Handlers) writePublicTrail(w http.ResponseWriter, ctx context.Context, p publicTrail, status int, extra map[string]any) {
	d, err := h.describePublicTrail(ctx, p)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "trail lookup failed", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if extra == nil {
		httpx.JSON(w, status, d)
		return
	}
	extra["trail"] = d
	httpx.JSON(w, status, extra)
}

type createDraftRequest struct {
	Template string `json:"template"`
	Language string `json:"language"`
	// Who a template about someone is about; empty for its example.
	Person string `json:"person"`
	Age    int    `json:"age"`
}

// maxPersonName bounds the name written into a template's cards.
const maxPersonName = 40

// CreateTrailDraft starts a quiz trail from a template. The answer holds
// the draft's secret, the only key to it: the browser keeps it.
//
// POST /api/o/{slug}/trail/drafts
func (h *Handlers) CreateTrailDraft(w http.ResponseWriter, r *http.Request) {
	var req createDraftRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	orgID, _, enabled, err := h.trailOrg(ctx, chi.URLParam(r, "slug"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !enabled) {
		httpx.JSON(w, http.StatusNotFound, map[string]string{"error": "party adventures are not available here", "code": "not_available"})
		return
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	lang := trailLang(req.Language)
	person := strings.TrimSpace(req.Person)
	if utf8.RuneCountInString(person) > maxPersonName || req.Age < 0 || req.Age > 120 {
		httpx.Error(w, http.StatusBadRequest, "the name or the age is out of range")
		return
	}
	t, ok := trail.NewFromTemplateFor(req.Template, lang, trail.Person{Name: person, Age: req.Age})
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "no such template")
		return
	}
	h.newDraft(w, r, orgID, t, t.Title, lang, 0)
}

// newDraft makes a draft of org's from t and answers with it and its
// secret, when the caller's address has not started too many today.
// A copy is made from adventure from, whose look and edits it keeps.
func (h *Handlers) newDraft(w http.ResponseWriter, r *http.Request, orgID int64, t trail.Template, title, lang string, from int64) {
	ctx := r.Context()
	ip := trailIPHash(r)
	var started int
	if err := h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM trails WHERE ip_hash = $1 AND created_at > now() - interval '1 day'
	`, ip).Scan(&started); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
		return
	}
	if started >= maxTrailDraftsPerIP {
		httpx.JSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many new party adventures today; try again tomorrow", "code": "too_many_drafts"})
		return
	}
	secret, err := tokenx.New(32)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
		return
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	advID, _, err := createTrailAdventure(ctx, tx, orgID, 0, t, title, lang, true)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
		return
	}
	if from > 0 {
		if err := copyTrailLook(ctx, tx, from, advID); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
			return
		}
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO trails (org_id, adventure_id, secret_hash, ip_hash) VALUES ($1, $2, $3, $4) RETURNING id
	`, orgID, advID, hashTrailSecret(secret), ip).Scan(&id); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
		return
	}
	p, err := scanPublicTrail(h.DB.QueryRowContext(ctx, publicTrailSQL+` WHERE t.id = $1`, id))
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "create failed", err)
		return
	}
	h.writePublicTrail(w, ctx, p, http.StatusCreated, map[string]any{"id": id, "secret": secret})
}

// trailSession is the email a session header opens trails of, or "".
func (h *Handlers) trailSession(ctx context.Context, r *http.Request) (string, error) {
	tok := strings.TrimSpace(r.Header.Get("X-Trail-Session"))
	if tok == "" || len(tok) > 128 {
		return "", nil
	}
	var email string
	err := h.DB.QueryRowContext(ctx, `
		SELECT email FROM trail_email_tokens WHERE token_hash = $1 AND purpose = 'session' AND expires_at > now()
	`, hashTrailSecret(tok)).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return email, err
}

// opens is whether secret or an email session opens p.
func (p publicTrail) opens(secret, sessionEmail string) bool {
	if secret != "" && subtle.ConstantTimeCompare([]byte(hashTrailSecret(secret)), []byte(p.SecretHash)) == 1 {
		return true
	}
	return sessionEmail != "" && p.Confirmed && strings.EqualFold(p.Email.String, sessionEmail)
}

// draftFor loads the trail in the URL for whoever holds its secret or a
// session of its email, writing a 404 for anyone else.
func (h *Handlers) draftFor(w http.ResponseWriter, r *http.Request) (publicTrail, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(w, http.StatusNotFound, "no such party adventure")
		return publicTrail{}, false
	}
	ctx := r.Context()
	p, err := scanPublicTrail(h.DB.QueryRowContext(ctx, publicTrailSQL+` WHERE t.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "no such party adventure")
		return p, false
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "trail lookup failed", err)
		return p, false
	}
	email, err := h.trailSession(ctx, r)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "trail lookup failed", err)
		return p, false
	}
	if !p.opens(r.Header.Get("X-Trail-Secret"), email) {
		httpx.Error(w, http.StatusNotFound, "no such party adventure")
		return p, false
	}
	return p, true
}

// GetTrailDraft is the creator's view of a public trail.
//
// GET /api/trail/drafts/{id}
func (h *Handlers) GetTrailDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := h.draftFor(w, r)
	if !ok {
		return
	}
	h.writePublicTrail(w, r.Context(), p, http.StatusOK, nil)
}

// trailSaves counts each draft's saves in the current minute.
var trailSaves = struct {
	sync.Mutex
	window time.Time
	n      map[int64]int
}{n: map[int64]int{}}

func allowTrailSave(id int64) bool {
	trailSaves.Lock()
	defer trailSaves.Unlock()
	now := time.Now().Truncate(time.Minute)
	if !now.Equal(trailSaves.window) {
		trailSaves.window, trailSaves.n = now, map[int64]int{}
	}
	trailSaves.n[id]++
	return trailSaves.n[id] <= maxTrailSavesPerMinute
}

// PutTrailDraft saves a public trail's cards, as PutTrail does: paid or
// not, until it has ended.
//
// PUT /api/trail/drafts/{id}
func (h *Handlers) PutTrailDraft(w http.ResponseWriter, r *http.Request) {
	var req putTrailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, ok := h.draftFor(w, r)
	if !ok {
		return
	}
	h.savePublicTrail(w, r, p, req)
}

func (h *Handlers) savePublicTrail(w http.ResponseWriter, r *http.Request, p publicTrail, req putTrailRequest) {
	if p.State() == "ended" {
		httpx.JSON(w, http.StatusConflict, map[string]string{"error": "this party adventure has ended", "code": "ended"})
		return
	}
	if !allowTrailSave(p.ID) {
		w.Header().Set("Retry-After", "60")
		httpx.Error(w, http.StatusTooManyRequests, "saving too often; try again in a minute")
		return
	}
	ctx := r.Context()
	if _, ok := h.saveTrail(w, ctx, p.Slug, 0, req); !ok {
		return
	}
	if _, err := h.DB.ExecContext(ctx, `UPDATE trails SET updated_at = now() WHERE id = $1`, p.ID); err != nil {
		logx.Warn("admin.trail.touch", "trail_id", p.ID, "err", err.Error())
	}
	h.writePublicTrail(w, ctx, p, http.StatusOK, nil)
}

// DeleteTrailDraft deletes an unpaid trail.
//
// DELETE /api/trail/drafts/{id}
func (h *Handlers) DeleteTrailDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := h.draftFor(w, r)
	if !ok {
		return
	}
	if p.PaidAt.Valid {
		httpx.Error(w, http.StatusConflict, "a party adventure that is ready to play can't be deleted")
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM adventures WHERE id = $1 AND trail_hidden`, p.AdvID); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "delete failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DuplicateTrailDraft starts a new draft with a trail's cards.
//
// POST /api/trail/drafts/{id}/duplicate
func (h *Handlers) DuplicateTrailDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := h.draftFor(w, r)
	if !ok {
		return
	}
	t, err := loadTrail(r.Context(), h.DB, p.Slug, false)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "duplicate failed", err)
		return
	}
	lang := trailLang(t.dto.Language)
	tp := trail.Template{Name: t.dto.Template, Kids: t.dto.Kids, Title: t.dto.Title, Cards: t.dto.Cards}
	title := t.dto.Title
	if lang == "sv" {
		title += " (kopia)"
	} else {
		title += " (copy)"
	}
	if title, err = validateName(title, MaxNameLen); err != nil {
		title = t.dto.Title
	}
	h.newDraft(w, r, p.OrgID, tp, title, lang, p.AdvID)
}

// copyTrailLook gives adventure to the look and theme of adventure from,
// and its revision: an edited trail's copy has no example cards.
func copyTrailLook(ctx context.Context, tx *sql.Tx, from, to int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE adventures n SET accent_color = s.accent_color, accent_secondary_color = s.accent_secondary_color,
		       background_color = s.background_color, text_color = s.text_color, submit_bg_color = s.submit_bg_color,
		       font = s.font, heading_font = s.heading_font, corners = s.corners, cards = s.cards,
		       card_outline = s.card_outline, backdrop = s.backdrop, look_style = s.look_style,
		       trail_theme = s.trail_theme, trail_revision = s.trail_revision
		FROM adventures s WHERE s.id = $1 AND n.id = $2
	`, from, to); err != nil {
		return err
	}
	return clonePicture(ctx, tx, from, to, 0)
}

/* ---------------- checkout ---------------- */

type trailCheckoutRequest struct {
	Email string `json:"email"`
	// Teams is how many teams a game holds; up to the free ones, nothing
	// is paid.
	Teams   int  `json:"teams"`
	Consent bool `json:"consent"`
}

// CheckoutTrailDraft buys a draft its hosted event: Teams teams a game
// for PartyGames games, the first PartyFreeTeams free and each one after
// at the team price, charged on the platform's own account. Free, the
// event is made at once, and the answer holds its host link.
//
// POST /api/trail/drafts/{id}/checkout
func (h *Handlers) CheckoutTrailDraft(w http.ResponseWriter, r *http.Request) {
	var req trailCheckoutRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, ok := h.draftFor(w, r)
	if !ok {
		return
	}
	if p.PaidAt.Valid {
		httpx.JSON(w, http.StatusConflict, map[string]string{"error": "this party adventure is paid for already", "code": "paid"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		httpx.Error(w, http.StatusBadRequest, "enter a valid email address")
		return
	}
	if req.Teams == 0 {
		req.Teams = int(h.pricing().PartyFreeTeams)
	}
	if req.Teams < 1 || req.Teams > maxPartyTeams {
		httpx.Error(w, http.StatusBadRequest, fmt.Sprintf("a game holds 1–%d teams", maxPartyTeams))
		return
	}
	// A party holds at least its free teams.
	if free := int(h.pricing().PartyFreeTeams); req.Teams < free {
		req.Teams = free
	}
	lim := hostedLimits{Teams: req.Teams, Games: int(h.pricing().PartyGames), Billing: "prepaid"}
	amount := h.partyPrice(req.Teams)
	ctx := r.Context()
	t, err := loadTrail(ctx, h.DB, p.Slug, false)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "trail lookup failed", err)
		return
	}
	if pr := trail.Problems(t.dto.Cards); len(pr) > 0 {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "the trail " + pr[0].Message, "code": "problems", "problems": pr})
		return
	}
	if t.dto.Kids && !req.Consent {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "confirm that you have the children's guardians' consent", "code": "consent"})
		return
	}
	free := amount <= 0
	if !free && !h.trailPaymentsOpen() {
		httpx.JSON(w, http.StatusConflict, map[string]string{"error": "booking isn't open right now", "code": "payments_closed"})
		return
	}
	if _, err := publishTrail(ctx, h.DB, p.AdvID, 0); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "publish failed", err)
		return
	}

	if free {
		// The admin link is mailed to the address given, so the host can
		// find the page again; drafts are limited per caller and day, which
		// bounds what a stranger can have us send. Nothing paid confirms
		// the address, though: it counts as confirmed (My trails) when
		// this browser signed in with it, or once the mailed link is
		// opened (sendTrailPaid). The answer opens the admin page.
		session, _ := h.trailSession(ctx, r)
		vouched := h.pricing().TeamCents <= 0 || strings.EqualFold(session, email)
		tx, err := h.DB.BeginTx(ctx, nil)
		if err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "checkout failed", err)
			return
		}
		defer tx.Rollback() //nolint:errcheck
		eventID, hostToken, err := createHostedEvent(ctx, tx, p.OrgID, p.Slug, t.dto.Title, 0, lim, "")
		if err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "checkout failed", err)
			return
		}
		if err := h.keepHostToken(ctx, tx, eventID, hostToken); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "checkout failed", err)
			return
		}
		if _, err := attachTrailEvent(ctx, tx, p.Slug, eventID, email, vouched); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "checkout failed", err)
			return
		}
		if err := tx.Commit(); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "checkout failed", err)
			return
		}
		if err := h.sendPartyHostLink(ctx, eventID, email, hostToken, false); err != nil {
			logx.Warn("admin.trail.paid_mail", "event_id", eventID, "err", err.Error())
		}
		httpx.JSON(w, http.StatusCreated, map[string]any{
			"event_id": eventID,
			"host_url": strings.TrimRight(h.PublicURL, "/") + "/party/" + hostToken,
		})
		return
	}
	// The admin page's secret is minted now, for this browser to open it
	// when it is back from paying; the webhook makes the event with it.
	hostToken, err := tokenx.New(24)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "checkout failed", err)
		return
	}
	var purchaseID int64
	if err := h.DB.QueryRowContext(ctx, `
		INSERT INTO ticket_purchases (kind, buyer_org_id, adventure_slug, party_name, buyer_email,
		                              team_size, amount_cents, currency, teams, host_token)
		VALUES ('hosted', $1, $2, $3, $4, 0, $5, $6, $7, $8) RETURNING id
	`, p.OrgID, p.Slug, t.dto.Title, email, amount, strings.ToUpper(settings.Currency), req.Teams, hostToken).Scan(&purchaseID); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "checkout failed", err)
		return
	}
	product := emailtpl.T(emailtpl.Lang(t.dto.Language), "%[1]s — party adventure, up to %[2]d teams per game", t.dto.Title, req.Teams)
	h.startHostedCheckout(w, r, purchaseID, product, amount, email, "/app/party/trail/paid",
		strings.TrimRight(h.PublicURL, "/")+"/party/"+hostToken)
}

// attachTrailEvent records, in tx, that the public trail of adventure
// slug was paid for (or taken free) and plays as eventID, saved to
// email — confirmed when a payment or a signed-in session vouches for
// it. It reports whether slug is a public trail.
func attachTrailEvent(ctx context.Context, tx *sql.Tx, slug string, eventID int64, email string, confirmed bool) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE trails SET event_id = $2, paid_at = now(), email = $3,
		       email_confirmed_at = CASE WHEN $4 THEN now() ELSE email_confirmed_at END, updated_at = now()
		WHERE adventure_id = (SELECT id FROM adventures WHERE slug = $1) AND paid_at IS NULL
	`, slug, eventID, strings.ToLower(email), confirmed)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// trailOfEvent is the public trail eventID plays, if it is one.
func (h *Handlers) trailOfEvent(ctx context.Context, eventID int64) (publicTrail, bool, error) {
	p, err := scanPublicTrail(h.DB.QueryRowContext(ctx, publicTrailSQL+` WHERE t.event_id = $1`, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	return p, err == nil, err
}

// stripeReceipts is whether the payment provider mails the buyer a
// receipt of its own: only Stripe does.
func (h *Handlers) stripeReceipts() bool { return h.Provider != nil && h.Provider.Name() == "stripe" }

// sendTrailPaid mails the buyer of a public trail its host link; sent
// again, or where no provider mails a receipt, it leaves out that one
// follows.
func (h *Handlers) sendTrailPaid(ctx context.Context, eventID int64, to, hostToken string, again bool) error {
	base := strings.TrimRight(strings.TrimSpace(h.PublicURL), "/")
	var (
		title, player string
		validUntil    time.Time
		orgID         int64
		lang, slug    string
		round         int
		paid          bool
	)
	if err := h.DB.QueryRowContext(ctx, `
		SELECT e.party_name, COALESCE(e.player_token, ''), e.valid_until, e.org_id, a.language, a.slug, e.current_round,
		       EXISTS (SELECT 1 FROM ticket_purchases p WHERE p.event_id = e.id AND p.kind = 'hosted' AND p.amount_cents > 0 AND p.state = 'paid')
		FROM events e JOIN adventures a ON a.slug = e.adventure_slug WHERE e.id = $1
	`, eventID).Scan(&title, &player, &validUntil, &orgID, &lang, &slug, &round, &paid); err != nil {
		return err
	}
	hostURL := base + "/party/" + hostToken
	// An address nothing has confirmed yet is confirmed by opening this
	// mail's link (the host page uses the token in its fragment), so
	// "Get a link by email" finds the trail afterwards.
	if p, ok, err := h.trailOfEvent(ctx, eventID); err != nil {
		return err
	} else if ok && !p.Confirmed && p.Email.Valid && strings.EqualFold(p.Email.String, to) {
		tok, err := newTrailToken(ctx, h.DB, to, "confirm", []int64{p.ID}, "", time.Now().Add(trailSessionTTL))
		if err != nil {
			return err
		}
		hostURL += "#confirm=" + tok
	}
	l := h.orgLook(ctx, orgID)
	l.Lang = emailtpl.Lang(lang)
	subject, c := emailtpl.TrailPaid(l.Lang, hostURL, base+"/j/"+player, title,
		trailDate(l.Lang, validUntil), l.Brand.Name, round > 0, !again && paid && h.stripeReceipts())
	c.Picture = h.emailCover(ctx, l.Lang, slug)
	if h.SellerIdentity != nil {
		if seller := h.SellerIdentity(l.Lang); seller != "" {
			c.Footer = strings.TrimSpace(c.Footer + " " + seller)
		}
	}
	return h.send(ctx, "trail_paid", l.message([]string{to}, subject, c))
}

// trailDate is a day as a trail's emails write it: "5 December 2026".
func trailDate(lang string, t time.Time) string {
	t = t.In(emailtpl.Location(""))
	if lang == "sv" {
		months := []string{"januari", "februari", "mars", "april", "maj", "juni", "juli", "augusti", "september", "oktober", "november", "december"}
		return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
	}
	if lang == "et" {
		return emailtpl.EstonianDate(t)
	}
	return t.Format("2 January 2006")
}

// SendTrailHostLink mails a paid trail's host link again, to the email
// it was paid with. It is the link the host has: one open elsewhere
// keeps working.
//
// POST /api/trail/drafts/{id}/host-link
func (h *Handlers) SendTrailHostLink(w http.ResponseWriter, r *http.Request) {
	p, ok := h.draftFor(w, r)
	if !ok {
		return
	}
	if !p.EventID.Valid || p.State() == "ended" || !p.Email.Valid {
		httpx.Error(w, http.StatusConflict, "this party adventure has no host page")
		return
	}
	ctx := r.Context()
	token, err := h.currentHostToken(ctx, p.EventID.Int64)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "send failed", err)
		return
	}
	if err := h.sendTrailPaid(ctx, p.EventID.Int64, p.Email.String, token, true); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "send failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

/* ---------------- saving to an email ---------------- */

// trailKey is a draft the browser holds: its id and secret.
type trailKey struct {
	ID     int64  `json:"id"`
	Secret string `json:"secret"`
}

// heldTrails are the trails among keys whose secret is right.
func (h *Handlers) heldTrails(ctx context.Context, keys []trailKey) ([]publicTrail, []int64, error) {
	var (
		held []publicTrail
		gone []int64
	)
	if len(keys) > 100 {
		keys = keys[:100]
	}
	for _, k := range keys {
		p, err := scanPublicTrail(h.DB.QueryRowContext(ctx, publicTrailSQL+` WHERE t.id = $1`, k.ID))
		if errors.Is(err, sql.ErrNoRows) {
			gone = append(gone, k.ID)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if !p.opens(k.Secret, "") {
			gone = append(gone, k.ID)
			continue
		}
		held = append(held, p)
	}
	return held, gone, nil
}

// trailMailAllowed is whether another link may be mailed to email, and
// from the caller's address, this hour.
func (h *Handlers) trailMailAllowed(ctx context.Context, email, ip string) (bool, error) {
	var byEmail, byIP int
	if err := h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE LOWER(email) = $1), COUNT(*) FILTER (WHERE ip_hash = $2)
		FROM trail_email_tokens
		WHERE purpose IN ('confirm', 'signin') AND created_at > now() - interval '1 hour'
		  AND (LOWER(email) = $1 OR ip_hash = $2)
	`, email, ip).Scan(&byEmail, &byIP); err != nil {
		return false, err
	}
	return byEmail < maxTrailMailsPerHour && byIP < maxTrailMailsPerHour*2, nil
}

// joinIDs and splitIDs carry a token's trail ids as text: "1,2,3".
func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func splitIDs(s string) []int64 {
	var ids []int64
	for _, part := range strings.Split(s, ",") {
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// newTrailToken stores a token for email and returns it.
func newTrailToken(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, email, purpose string, trailIDs []int64, ip string, expires time.Time) (string, error) {
	tok, err := tokenx.New(32)
	if err != nil {
		return "", err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO trail_email_tokens (token_hash, email, purpose, trail_ids, ip_hash, expires_at)
		VALUES ($1, $2, $3, string_to_array($4, ',')::bigint[], $5, $6)
	`, hashTrailSecret(tok), strings.ToLower(email), purpose, joinIDs(trailIDs), ip, expires)
	return tok, err
}

type trailEmailRequest struct {
	Email  string     `json:"email"`
	Trails []trailKey `json:"trails"`
}

func parseTrailEmail(w http.ResponseWriter, raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		httpx.Error(w, http.StatusBadRequest, "enter a valid email address")
		return "", false
	}
	return email, true
}

// SaveTrailsToEmail mails a link that saves the trails this browser
// holds to an address, once its owner opens it. Nothing is saved until
// then.
//
// POST /api/trail/email/save
func (h *Handlers) SaveTrailsToEmail(w http.ResponseWriter, r *http.Request) {
	var req trailEmailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	email, ok := parseTrailEmail(w, req.Email)
	if !ok {
		return
	}
	ctx := r.Context()
	held, _, err := h.heldTrails(ctx, req.Trails)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "save failed", err)
		return
	}
	if len(held) == 0 {
		httpx.Error(w, http.StatusBadRequest, "there is no party adventure to save")
		return
	}
	ids := make([]int64, 0, len(held))
	for _, p := range held {
		ids = append(ids, p.ID)
	}
	h.mailTrailLink(w, r, email, "confirm", ids, held[0].OrgID)
}

// SignInToTrails mails a link to the trails saved to an address, if it
// has any. The answer is the same either way.
//
// POST /api/trail/email/signin
func (h *Handlers) SignInToTrails(w http.ResponseWriter, r *http.Request) {
	var req trailEmailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	email, ok := parseTrailEmail(w, req.Email)
	if !ok {
		return
	}
	var orgID int64
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT org_id FROM trails WHERE LOWER(email) = $1 AND email_confirmed_at IS NOT NULL LIMIT 1
	`, email).Scan(&orgID)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "sign-in failed", err)
		return
	}
	h.mailTrailLink(w, r, email, "signin", nil, orgID)
}

// mailTrailLink mails a confirm or sign-in link and answers 204, or 429
// when too many confirm links went out. Too many sign-in links still
// answer 204, unsent, so the answer never tells whether an address has
// trails.
func (h *Handlers) mailTrailLink(w http.ResponseWriter, r *http.Request, email, purpose string, ids []int64, orgID int64) {
	ctx := r.Context()
	ip := trailIPHash(r)
	ok, err := h.trailMailAllowed(ctx, email, ip)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "send failed", err)
		return
	}
	if !ok && purpose == "signin" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !ok {
		httpx.JSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many emails; try again in an hour", "code": "too_many_emails"})
		return
	}
	tok, err := newTrailToken(ctx, h.DB, email, purpose, ids, ip, time.Now().Add(trailLinkTTL))
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "send failed", err)
		return
	}
	link := strings.TrimRight(h.PublicURL, "/") + "/app/party/trail/verify#" + tok
	l := h.orgLook(ctx, orgID)
	l.Lang = emailtpl.LangFromRequest(r)
	var (
		subject string
		c       emailtpl.Content
	)
	if purpose == "confirm" {
		subject, c = emailtpl.TrailConfirm(l.Lang, link)
	} else {
		subject, c = emailtpl.TrailSignin(l.Lang, link)
	}
	c.Picture = h.house(l.Lang, emailtpl.PictureVerify)
	if err := h.send(ctx, "trail_"+purpose, l.message([]string{email}, subject, c)); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "send failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// VerifyTrailEmail uses a mailed confirm or sign-in link: a confirm link
// saves its trails to the address. Either answers with a session that
// opens every trail saved there.
//
// POST /api/trail/email/verify
func (h *Handlers) VerifyTrailEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "verify failed", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	var email, purpose, ids string
	err = tx.QueryRowContext(ctx, `
		UPDATE trail_email_tokens SET used_at = now()
		WHERE token_hash = $1 AND purpose IN ('confirm', 'signin') AND used_at IS NULL AND expires_at > now()
		RETURNING email, purpose, array_to_string(trail_ids, ',')
	`, hashTrailSecret(strings.TrimSpace(req.Token))).Scan(&email, &purpose, &ids)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.JSON(w, http.StatusGone, map[string]string{"error": "this link has expired or was used already", "code": "expired"})
		return
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "verify failed", err)
		return
	}
	if purpose == "confirm" {
		// A paid trail keeps the address it was paid with; its host
		// link's mail confirms that address.
		if _, err := tx.ExecContext(ctx, `
			UPDATE trails SET email = $1, email_confirmed_at = now()
			WHERE id = ANY(string_to_array($2, ',')::bigint[]) AND (paid_at IS NULL OR LOWER(email) = $1)
		`, email, ids); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "verify failed", err)
			return
		}
	}
	session, err := newTrailToken(ctx, tx, email, "session", nil, trailIPHash(r), time.Now().Add(trailSessionTTL))
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "verify failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "verify failed", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"session": session, "email": email})
}

// trailSummaryDTO is one trail on My trails.
type trailSummaryDTO struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	Cards       int        `json:"cards"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ValidUntil  *time.Time `json:"valid_until,omitempty"`
	FirstGameAt *time.Time `json:"first_game_at,omitempty"`
	// Saved is whether the trail is saved to an email (else only this
	// browser has it).
	Saved bool `json:"saved"`
	// Theme is its starter look, for a swatch beside it.
	Theme string `json:"theme,omitempty"`
}

// ListMyTrails lists the trails this browser holds and, with a session,
// every trail saved to its email. Gone lists the held ones that no
// longer exist, for the browser to forget.
//
// POST /api/trail/mine
func (h *Handlers) ListMyTrails(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Trails []trailKey `json:"trails"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	held, gone, err := h.heldTrails(ctx, req.Trails)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	email, err := h.trailSession(ctx, r)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	if email != "" {
		rows, err := h.DB.QueryContext(ctx, publicTrailSQL+` WHERE LOWER(t.email) = $1 AND t.email_confirmed_at IS NOT NULL`, email)
		if err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
			return
		}
		for rows.Next() {
			p, err := scanPublicTrail(rows)
			if err != nil {
				rows.Close()
				httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
				return
			}
			held = append(held, p)
		}
		rows.Close()
	}
	seen := map[int64]bool{}
	out := []trailSummaryDTO{}
	for _, p := range held {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		t, err := loadTrail(ctx, h.DB, p.Slug, false)
		if err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
			return
		}
		d := trailSummaryDTO{ID: p.ID, Title: t.dto.Title, State: p.State(), Cards: len(t.dto.Cards),
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Saved: p.Confirmed, Theme: t.dto.Theme}
		if p.ValidUntil.Valid {
			d.ValidUntil = &p.ValidUntil.Time
		}
		if p.FirstGameAt.Valid {
			d.FirstGameAt = &p.FirstGameAt.Time
		}
		out = append(out, d)
	}
	if gone == nil {
		gone = []int64{}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"trails": out, "gone": gone, "email": email})
}

/* ---------------- the host page ---------------- */

// hostTrail is the public trail the host link in the URL runs, writing a
// 404 when it runs none.
func (h *Handlers) hostTrail(w http.ResponseWriter, r *http.Request) (publicTrail, bool) {
	hp, ok := h.hostedParty(w, r)
	if !ok {
		return publicTrail{}, false
	}
	p, ok, err := h.trailOfEvent(r.Context(), hp.EventID)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "trail lookup failed", err)
		return p, false
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "this is not a party adventure")
		return p, false
	}
	return p, true
}

// GetHostTrail is the creator's view of the trail a host link runs.
//
// GET /api/party/{token}/cards
func (h *Handlers) GetHostTrail(w http.ResponseWriter, r *http.Request) {
	p, ok := h.hostTrail(w, r)
	if !ok {
		return
	}
	h.writePublicTrail(w, r.Context(), p, http.StatusOK, nil)
}

// PutHostTrail saves the cards of the trail a host link runs; they play
// from the next game.
//
// PUT /api/party/{token}/cards
func (h *Handlers) PutHostTrail(w http.ResponseWriter, r *http.Request) {
	var req putTrailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, ok := h.hostTrail(w, r)
	if !ok {
		return
	}
	h.savePublicTrail(w, r, p, req)
}

// keepFrom is when the buyer of a trail first playing at first may keep it.
func keepFrom(first time.Time) time.Time { return first.Add(trailKeepAfter) }

// HostTrailKeep is the Keep it link of the trail a host link runs, from
// a week into play.
//
// POST /api/party/{token}/keep
func (h *Handlers) HostTrailKeep(w http.ResponseWriter, r *http.Request) {
	p, ok := h.hostTrail(w, r)
	if !ok {
		return
	}
	if p.State() != "playing" || time.Now().Before(keepFrom(p.FirstGameAt.Time)) {
		httpx.Error(w, http.StatusConflict, "this party adventure can be kept from a week into play")
		return
	}
	url, err := h.keepLink(r.Context(), p)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"url": url})
}

// keepLink makes a Keep it link for p, good until its event lapses.
func (h *Handlers) keepLink(ctx context.Context, p publicTrail) (string, error) {
	until := time.Now().Add(HostedPlayPeriod)
	if p.ValidUntil.Valid {
		until = p.ValidUntil.Time
	}
	tok, err := newTrailToken(ctx, h.DB, p.Email.String, "keep", []int64{p.ID}, "", until)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(h.PublicURL, "/") + "/app/arrange/#/keep/" + tok, nil
}

/* ---------------- keeping a trail ---------------- */

// keptTrail is the trail a Keep it token names, while it is good.
func (h *Handlers) keptTrail(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, token string, lock bool) (publicTrail, error) {
	var raw string
	l := ""
	if lock {
		l = " FOR UPDATE"
	}
	if err := q.QueryRowContext(ctx, `
		SELECT array_to_string(trail_ids, ',') FROM trail_email_tokens
		WHERE token_hash = $1 AND purpose = 'keep' AND used_at IS NULL AND expires_at > now()`+l,
		hashTrailSecret(strings.TrimSpace(token))).Scan(&raw); err != nil {
		return publicTrail{}, err
	}
	ids := splitIDs(raw)
	if len(ids) != 1 {
		return publicTrail{}, sql.ErrNoRows
	}
	p, err := scanPublicTrail(q.QueryRowContext(ctx, publicTrailSQL+` WHERE t.id = $1`, ids[0]))
	if err != nil {
		return p, err
	}
	if p.State() != "playing" && p.State() != "paid" {
		return p, sql.ErrNoRows
	}
	return p, nil
}

// GetKeepTrail is what the Keep it page shows of the trail.
//
// GET /api/trail/keep/{token}
func (h *Handlers) GetKeepTrail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.keptTrail(ctx, h.DB, chi.URLParam(r, "token"), false)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.JSON(w, http.StatusGone, map[string]string{"error": "this link has expired or was used already", "code": "expired"})
		return
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	t, err := loadTrail(ctx, h.DB, p.Slug, false)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "lookup failed", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"title": t.dto.Title, "cards": len(t.dto.Cards), "email": p.Email.String})
}

// KeepTrail moves the trail a Keep it token names into the caller's org,
// for good: it is an ordinary quiz trail of theirs from now on, and its
// hosted event, with every game's results, goes.
//
// POST /api/admin/trail-keep
func (h *Handlers) KeepTrail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	me, _ := auth.FromContext(r.Context())
	org := callerOrgID(me)
	if !org.Valid || !canAuthor(me, org) {
		httpx.Error(w, http.StatusForbidden, "forbidden")
		return
	}
	ctx := r.Context()
	p, err := h.keptTrail(ctx, h.DB, req.Token, false)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.JSON(w, http.StatusGone, map[string]string{"error": "this link has expired or was used already", "code": "expired"})
		return
	}
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	// The game being played ends first, so its summary goes out.
	if p.EventID.Valid {
		if _, err := h.finishRound(ctx, p.EventID.Int64, "kept"); err != nil {
			httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
			return
		}
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	if p, err = h.keptTrail(ctx, tx, req.Token, true); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.JSON(w, http.StatusGone, map[string]string{"error": "this link has expired or was used already", "code": "expired"})
			return
		}
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE trail_email_tokens SET used_at = now() WHERE token_hash = $1`,
		hashTrailSecret(strings.TrimSpace(req.Token))); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	title, email, orgID, err := endTrail(ctx, tx, p)
	if err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE adventures SET org_id = $2, created_by_user_id = $3, trail_hidden = FALSE WHERE id = $1
	`, p.AdvID, org.Int64, me.ID); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.ServerErr(w, http.StatusInternalServerError, "keep failed", err)
		return
	}
	h.mailTrailEnded(ctx, orgID, email, title, p.Slug, true)
	logx.Info("admin.trail.kept", "trail_id", p.ID, "adventure", p.Slug, "org_id", org.Int64)
	httpx.JSON(w, http.StatusOK, map[string]string{"slug": p.Slug})
}

// endTrail deletes, in tx, a public trail's row and its hosted event —
// with the graphs its games played — but not its adventure. It returns
// what the goodbye mail needs.
func endTrail(ctx context.Context, tx *sql.Tx, p publicTrail) (title, email string, orgID int64, err error) {
	if err = tx.QueryRowContext(ctx, `SELECT title FROM adventures WHERE id = $1`, p.AdvID).Scan(&title); err != nil {
		return
	}
	if p.EventID.Valid {
		if _, err = tx.ExecContext(ctx, `
			WITH gone AS (DELETE FROM events WHERE id = $1 RETURNING graph_id)
			DELETE FROM adventure_graphs g USING gone WHERE g.id = gone.graph_id AND g.version < 0
		`, p.EventID.Int64); err != nil {
			return
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM trails WHERE id = $1`, p.ID); err != nil {
		return
	}
	return title, p.Email.String, p.OrgID, nil
}

// mailTrailEnded says goodbye to a trail's buyer: it was deleted, or
// moved into their account (kept).
func (h *Handlers) mailTrailEnded(ctx context.Context, orgID int64, to, title, slug string, kept bool) {
	if to == "" {
		return
	}
	l := h.orgLook(ctx, orgID)
	var lang string
	if err := h.DB.QueryRowContext(ctx, `SELECT language FROM adventures WHERE slug = $1`, slug).Scan(&lang); err == nil {
		l.Lang = emailtpl.Lang(lang)
	}
	subject, c := emailtpl.TrailEnded(l.Lang, title, kept, l.Brand.Name)
	c.Picture = h.emailCover(ctx, l.Lang, slug)
	if err := h.send(ctx, "trail_ended", l.message([]string{to}, subject, c)); err != nil {
		logx.Warn("admin.trail.ended_mail", "adventure", slug, "err", err.Error())
	}
}

/* ---------------- the sweeper ---------------- */

// SweepTrails deletes public trails whose time is up — unpaid drafts a
// month after their last edit, paid ones when their event has ended or
// lapsed unplayed — mails the day-7 Keep it offer, and drops expired
// links. Run hourly or so.
func (h *Handlers) SweepTrails(ctx context.Context) {
	// Drafts, unless a payment for one is under way.
	if _, err := h.DB.ExecContext(ctx, `
		DELETE FROM adventures a USING trails t
		WHERE t.adventure_id = a.id AND a.trail_hidden AND t.paid_at IS NULL
		  AND t.updated_at < now() - make_interval(days => $1)
		  AND NOT EXISTS (SELECT 1 FROM ticket_purchases p WHERE p.adventure_slug = a.slug AND p.state = 'pending'
		                  AND p.created_at::timestamp AT TIME ZONE 'UTC' > now() - interval '1 day')
	`, trailDraftDays); err != nil {
		logx.Error("trail.sweep.drafts", "err", err.Error())
	}

	rows, err := h.DB.QueryContext(ctx, publicTrailSQL+`
		WHERE t.paid_at IS NOT NULL AND (e.id IS NULL OR e.state IN ('completed', 'cancelled')
		      OR (e.state = 'scheduled' AND e.valid_until < now()))
		LIMIT 50`)
	if err != nil {
		logx.Error("trail.sweep.ended", "err", err.Error())
		return
	}
	var ended []publicTrail
	for rows.Next() {
		p, err := scanPublicTrail(rows)
		if err != nil {
			rows.Close()
			logx.Error("trail.sweep.ended", "err", err.Error())
			return
		}
		ended = append(ended, p)
	}
	rows.Close()
	for _, p := range ended {
		if err := h.deleteTrail(ctx, p); err != nil {
			logx.Error("trail.sweep.delete", "trail_id", p.ID, "err", err.Error())
		}
	}

	h.offerKeep(ctx)

	if _, err := h.DB.ExecContext(ctx, `DELETE FROM trail_email_tokens WHERE expires_at < now()`); err != nil {
		logx.Error("trail.sweep.tokens", "err", err.Error())
	}
}

// deleteTrail deletes a public trail whole and says goodbye.
func (h *Handlers) deleteTrail(ctx context.Context, p publicTrail) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	title, email, orgID, err := endTrail(ctx, tx, p)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM adventures WHERE id = $1 AND trail_hidden`, p.AdvID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	h.mailTrailEnded(ctx, orgID, email, title, p.Slug, false)
	logx.Info("trail.deleted", "trail_id", p.ID, "adventure", p.Slug)
	return nil
}

// offerKeep mails each trail a week into play its Keep it link, once.
func (h *Handlers) offerKeep(ctx context.Context) {
	rows, err := h.DB.QueryContext(ctx, publicTrailSQL+`
		WHERE t.keep_offer_sent_at IS NULL AND t.email IS NOT NULL AND e.state IN ('running', 'paused')
		  AND e.first_game_at < now() - make_interval(secs => $1) AND e.valid_until > now()
		LIMIT 50`, trailKeepAfter.Seconds())
	if err != nil {
		logx.Error("trail.sweep.keep", "err", err.Error())
		return
	}
	var todo []publicTrail
	for rows.Next() {
		p, err := scanPublicTrail(rows)
		if err != nil {
			rows.Close()
			logx.Error("trail.sweep.keep", "err", err.Error())
			return
		}
		todo = append(todo, p)
	}
	rows.Close()
	for _, p := range todo {
		// Marked first: a mail that fails is not sent again and again.
		if _, err := h.DB.ExecContext(ctx, `UPDATE trails SET keep_offer_sent_at = now() WHERE id = $1`, p.ID); err != nil {
			logx.Error("trail.sweep.keep", "trail_id", p.ID, "err", err.Error())
			continue
		}
		link, err := h.keepLink(ctx, p)
		if err != nil {
			logx.Error("trail.sweep.keep", "trail_id", p.ID, "err", err.Error())
			continue
		}
		var title, lang, slug string
		_ = h.DB.QueryRowContext(ctx, `SELECT title, language, slug FROM adventures WHERE id = $1`, p.AdvID).Scan(&title, &lang, &slug)
		l := h.orgLook(ctx, p.OrgID)
		l.Lang = emailtpl.Lang(lang)
		subject, c := emailtpl.TrailKeep(l.Lang, link, title, trailDate(l.Lang, p.ValidUntil.Time))
		c.Picture = h.emailCover(ctx, l.Lang, slug)
		if err := h.send(ctx, "trail_keep", l.message([]string{p.Email.String}, subject, c)); err != nil {
			logx.Warn("trail.sweep.keep_mail", "trail_id", p.ID, "err", err.Error())
		}
	}
}
