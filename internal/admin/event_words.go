package admin

// An event's words in the language each of its teams plays in
// (internal/listing). What the platform writes to a team — its
// invitation, a cancellation, its ticket — and its calendar entry are in
// the team's language when the adventure is played in it, with the
// event named, its meeting place and the adventure's note to buyers said
// in it where they are translated. What an organiser writes to teams
// goes out as written, or in a team's language where they wrote it in
// that too, and the email around it is in the same language as the
// words.

import (
	"context"
	"slices"
	"strings"

	"github.com/klahr/zonventure/internal/emailtpl"
	"github.com/klahr/zonventure/internal/icscal"
	"github.com/klahr/zonventure/internal/listing"
)

// eventWords are an event's words in its main language, and their
// translations.
type eventWords struct {
	main  listing.Texts
	lang  string // the adventure's main language
	texts listing.Listing
}

// eventWords loads an event's words. None, for an event it cannot read:
// everything then goes out as it would without translations.
func (h *Handlers) eventWords(ctx context.Context, eventID int64) eventWords {
	var (
		w         eventWords
		adv, own  []byte
		mainTexts = &w.main
	)
	if err := h.DB.QueryRowContext(ctx, `
		SELECT COALESCE(e.name, ''), COALESCE(a.title, e.adventure_slug), COALESCE(e.meeting_place, ''),
		       COALESCE(a.start_label, ''), COALESCE(a.buyer_note, ''), `+listing.Columns+`
		FROM events e
		CROSS JOIN LATERAL (SELECT e.translations) AS l (translations)
		LEFT JOIN adventures a ON a.org_id = e.org_id AND a.slug = e.adventure_slug
		LEFT JOIN adventure_graphs g ON g.id = COALESCE(e.graph_id, a.published_graph_id)
		WHERE e.id = $1`, eventID).Scan(&mainTexts.Name, &mainTexts.Title, &mainTexts.MeetingPlace,
		&mainTexts.StartLabel, &mainTexts.BuyerNote, &w.lang, &adv, &own); err != nil {
		return eventWords{}
	}
	w.texts, _ = listing.Scan(w.lang, adv, own)
	return w
}

// speaks reports whether lang is one of the event's other languages.
func (w eventWords) speaks(lang string) bool {
	return lang != "" && lang != w.lang && slices.Contains(w.texts.Languages(), lang)
}

// name is what the event is called in t: its own name, or its
// adventure's title when it has none.
func (w eventWords) name(t listing.Texts) string {
	if strings.TrimSpace(w.main.Name) != "" {
		return t.Name
	}
	return t.Title
}

// lookIn is l for a team that plays in lang: its emails in that
// language, the event named and the note to buyers said in it. As it is
// for the main language, or one the adventure is not played in.
func (w eventWords) lookIn(l look, lang string) look {
	if !w.speaks(lang) {
		return l
	}
	t := w.texts.In(lang, w.main)
	l.Lang = emailtpl.Lang(lang)
	l.Event = w.name(t)
	l.BuyerNote = t.BuyerNote
	return l
}

// placeIn is where a team that plays in lang meets: place, the event's
// meeting place, said in lang where it is translated.
func (w eventWords) placeIn(place, lang string) string {
	if place == "" || !w.speaks(lang) {
		return place
	}
	return w.texts.In(lang, w.main).MeetingPlace
}

// calendarIn is the event's calendar entry for a team that plays in
// lang: named, and placed by its meeting place, in that language.
// Without a meeting place it is placed by the start's coordinates,
// which need no translating. Its link is introduced in the language of
// the team's emails, l's or lang.
func (w eventWords) calendarIn(evt icscal.Event, l look, lang string) icscal.Event {
	evt = evt.Labelled(emailtpl.T(w.lookIn(l, lang).Lang, "Join link:"))
	if !w.speaks(lang) {
		return evt
	}
	t := w.texts.In(lang, w.main)
	return evt.Named(w.name(t), w.placeIn(strings.TrimSpace(w.main.MeetingPlace), lang))
}
