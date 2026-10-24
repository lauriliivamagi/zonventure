package emailtpl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/klahr/zonventure/internal/settings"
	"github.com/klahr/zonventure/internal/sitename"
)

// The emails, one function each: what they say, in a language, given
// what they are about. Senders call them for the real thing and the
// preview calls them with Sample data, so a preview is exactly what will
// be sent. Every word goes through T, so each has its Swedish in lang.go.

// Lines keeps an author's line breaks in Markdown, as they wrote them:
// in-game emails and arrangers' messages are written as plain text that
// may use Markdown emphasis and links.
func Lines(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n")
	paras := strings.Split(s, "\n\n")
	for i, p := range paras {
		paras[i] = strings.ReplaceAll(strings.TrimSpace(p), "\n", "  \n")
	}
	return strings.Join(paras, "\n\n")
}

// mdEscape keeps a name from being read as Markdown.
func mdEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "`", "\\`", "<", "&lt;", "#", `\#`).Replace(s)
}

// --- signing in (always the platform's own brand) ---------------------------

func PasswordReset(lang, link string) (string, Content) {
	return T(lang, "Reset your %s password", sitename.Name()), Content{
		Lang:      lang,
		Preheader: T(lang, "Choose a new password — the link works for an hour."),
		Heading:   T(lang, "Reset your password"),
		Body:      T(lang, "Someone — hopefully you — asked to reset the password for this %s account.", mdEscape(sitename.Name())),
		Buttons:   []Button{{T(lang, "Choose a new password"), link}},
		Footer:    T(lang, "The link expires in 1 hour. If you didn't ask for this, ignore the email: your password stays as it is."),
	}
}

func VerifyEmail(lang, link string) (string, Content) {
	return T(lang, "Verify your %s email", sitename.Name()), Content{
		Lang:      lang,
		Preheader: T(lang, "One click and you're in."),
		Heading:   T(lang, "Welcome to %s!", sitename.Name()),
		Body:      T(lang, "Confirm your email address to finish setting up your account."),
		Buttons:   []Button{{T(lang, "Confirm my email"), link}},
		Footer:    T(lang, "The link expires in 24 hours. If you didn't sign up, you can ignore this email."),
	}
}

func EmailChange(lang, link string) (string, Content) {
	return T(lang, "Confirm your new %s email", sitename.Name()), Content{
		Lang:      lang,
		Preheader: T(lang, "Confirm the address to start using it."),
		Heading:   T(lang, "Confirm your new email"),
		Body:      T(lang, "You asked to use this address for your %s account. Confirm it to make the change.", mdEscape(sitename.Name())),
		Buttons:   []Button{{T(lang, "Confirm this address"), link}},
		Footer:    T(lang, "If you didn't ask for this, ignore the email: your account keeps its current address."),
	}
}

// OrgInvitation asks an account to join org — newAccount when it was
// made for the invitation and must choose its password first (link
// sets it); nothing changes
// until they accept in the console that link opens. role is the one
// the invitation names (org_admin or org_member).
func OrgInvitation(lang, org, role, link string, newAccount bool) (string, Content) {
	if newAccount {
		body := T(lang, "An admin of %s has made you a %s account to join their organization as an event host. Choose your password, then accept or decline.", mdEscape(org), mdEscape(sitename.Name()))
		switch role {
		case "org_admin":
			body = T(lang, "An admin of %s has made you a %s account to join their organization as an admin. Choose your password, then accept or decline.", mdEscape(org), mdEscape(sitename.Name()))
		case "org_writer":
			body = T(lang, "An admin of %s has made you a %s account to join their organization as a writer. Choose your password, then accept or decline.", mdEscape(org), mdEscape(sitename.Name()))
		}
		return T(lang, "%s invites you to join them", org), Content{
			Lang:      lang,
			Preheader: T(lang, "Choose your password to answer. The link works for 7 days."),
			Heading:   T(lang, "Join %s?", mdEscape(org)),
			Body:      body,
			Buttons:   []Button{{T(lang, "Choose your password"), link}},
			Footer:    T(lang, "If you don't know them, ignore this email: nothing happens unless you choose a password and accept."),
		}
	}
	body := T(lang, "An admin of %s wants to add your %s account to their organization as an event host. Sign in to accept or decline.", mdEscape(org), mdEscape(sitename.Name()))
	switch role {
	case "org_admin":
		body = T(lang, "An admin of %s wants to add your %s account to their organization as an admin. Sign in to accept or decline.", mdEscape(org), mdEscape(sitename.Name()))
	case "org_writer":
		body = T(lang, "An admin of %s wants to add your %s account to their organization as a writer. Sign in to accept or decline.", mdEscape(org), mdEscape(sitename.Name()))
	}
	return T(lang, "%s invites you to join them", org), Content{
		Lang:      lang,
		Preheader: T(lang, "Accept or decline when you next sign in."),
		Heading:   T(lang, "Join %s?", mdEscape(org)),
		Body:      body,
		Buttons:   []Button{{T(lang, "Sign in to answer"), link}},
		Footer:    T(lang, "If you don't know them, decline: you stay out of their organization."),
	}
}

// What an AccountChanged notice is about.
const (
	ChangedEmail    = "email"
	ChangedPassword = "password"
	ChangedTOTPOn   = "totp_on"
	ChangedTOTPOff  = "totp_off"
)

// AccountChanged tells the address on file that something about signing
// in changed, so a hijacked session can't change it unseen. For
// ChangedEmail it goes to the old address and newEmail names the new one.
// contact is the platform's contact address (settings.Legal), if set.
func AccountChanged(lang, what, newEmail, revertLink, contact string) (string, Content) {
	site := mdEscape(sitename.Name())
	var heading, body string
	footer := T(lang, "If this was you, there's nothing to do. If it wasn't, someone else may have access to your account: reset your password straight away.")
	if contact != "" {
		footer = T(lang, "If this was you, there's nothing to do. If it wasn't, someone else may have access to your account: reset your password straight away and write to %[1]s at %[2]s.", site, mdEscape(contact))
	}
	var buttons []Button
	switch what {
	case ChangedEmail:
		heading = T(lang, "Your email address was changed")
		body = T(lang, "The email address for your %[1]s account was changed to %[2]s. This address no longer gets the account's emails.", site, mdEscape(newEmail))
		if revertLink != "" {
			buttons = []Button{{T(lang, "Put my address back"), revertLink}}
			footer = T(lang, "If this was you, there's nothing to do. If it wasn't, use the button within 7 days: it puts this address back, signs out every session and revokes every API token. Then reset your password.")
		}
	case ChangedPassword:
		heading = T(lang, "Your password was changed")
		body = T(lang, "The password for your %s account was changed.", site)
	case ChangedTOTPOn:
		heading = T(lang, "Two-factor authentication was turned on")
		body = T(lang, "Two-factor authentication was turned on for your %s account.", site)
	default:
		heading = T(lang, "Two-factor authentication was turned off")
		body = T(lang, "Two-factor authentication was turned off for your %s account.", site)
	}
	return T(lang, "Security notice for your %s account", sitename.Name()), Content{
		Lang:      lang,
		Preheader: heading,
		Heading:   heading,
		Body:      body,
		Buttons:   buttons,
		Footer:    footer,
	}
}

// AccountDeletion confirms that the account is deleted on when (a date
// already written in lang), or, as the reminder the day before, that it
// is deleted tomorrow. link opens the console, where signing in shows
// Cancel deletion.
func AccountDeletion(lang, when, link string, reminder bool) (string, Content) {
	site := mdEscape(sitename.Name())
	subject := T(lang, "Your %s account will be deleted on %s", sitename.Name(), when)
	heading := T(lang, "Your account will be deleted")
	body := T(lang, "You asked to delete your %[1]s account. It and everything only it holds will be deleted for good on %[2]s.", site, mdEscape(when))
	if reminder {
		subject = T(lang, "Your %s account will be deleted tomorrow", sitename.Name())
		heading = T(lang, "Your account will be deleted tomorrow")
		body = T(lang, "Your %[1]s account will be deleted for good on %[2]s, as you asked. After that it can't be restored.", site, mdEscape(when))
	}
	return subject, Content{
		Lang:      lang,
		Preheader: T(lang, "Changed your mind? Sign in and cancel the deletion before %s.", when),
		Heading:   heading,
		Body:      body,
		Buttons:   []Button{{T(lang, "Keep my account"), link}},
		Footer:    T(lang, "To keep the account, sign in before then and choose Cancel deletion. If you didn't ask for this, sign in, cancel it and change your password."),
	}
}

// --- players -----------------------------------------------------------------

// PlayerLink is one player's way into a game.
type PlayerLink struct {
	Team, Player, URL string
	Event             string
}

// brandName is who a player's email is from in its subject: the
// organisation, else the platform.
func brandName(org string) string {
	if org != "" {
		return org
	}
	return sitename.Name()
}

func playerFooter(lang, event, org string) string {
	if event == "" {
		return T(lang, "You get this because you play with %s.", org)
	}
	return T(lang, "You get this because your team plays %[1]s with %[2]s.", event, org)
}

// TeamInvitation is a team's links, one per player.
func TeamInvitation(lang, team, event, org string, players []PlayerLink, calendar bool) (string, Content) {
	var b strings.Builder
	b.WriteString(T(lang, "Your team **%s** is ready to play. Forward each link to the right teammate — opening it claims that player's place and starts their game.", mdEscape(team)))
	b.WriteString("\n\n")
	for _, p := range players {
		b.WriteString("- **" + mdEscape(p.Player) + "**: [" + p.URL + "](" + p.URL + ")\n")
	}
	if calendar {
		b.WriteString("\n" + T(lang, "A calendar invitation is attached, so the event lands in your calendar at the right time."))
	}
	heading := T(lang, "You're in!")
	if event != "" {
		heading = T(lang, "You're in: %s", event)
	}
	return T(lang, "Your %s team links", brandName(org)), Content{
		Lang:      lang,
		Preheader: T(lang, "The links for team %s — one per player.", team),
		Heading:   heading,
		Body:      b.String(),
		Footer:    playerFooter(lang, event, org),
	}
}

// Location is time zone tz, an organisation's; the instance's
// (settings.TimeZone) when it names none this server knows.
func Location(tz string) *time.Location {
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	if l, err := time.LoadLocation(settings.TimeZone); err == nil {
		return l
	}
	return time.UTC
}

var (
	svWeekdays = []string{"söndag", "måndag", "tisdag", "onsdag", "torsdag", "fredag", "lördag"}
	svMonths   = []string{"januari", "februari", "mars", "april", "maj", "juni", "juli", "augusti", "september", "oktober", "november", "december"}
)

// When is a start time as an email states it, in time zone tz:
// "Saturday 18 October 2026, 14:00 (CEST)".
func When(lang string, t time.Time, tz string) string {
	t = t.In(Location(tz))
	if lang == "sv" {
		return fmt.Sprintf("%s %d %s %d kl. %s (%s)", svWeekdays[t.Weekday()], t.Day(), svMonths[t.Month()-1], t.Year(), t.Format("15:04"), t.Format("MST"))
	}
	if lang == "et" {
		return etWhen(t)
	}
	return t.Format("Monday 2 January 2006, 15:04 (MST)")
}

// WithSchedule says in a team's invitation when event starts and where
// to meet, either left out with "". changed says the time has moved
// since the team's last invitation.
func WithSchedule(subject string, c Content, event, when, place string, changed bool) (string, Content) {
	lang := c.Lang
	var lines []string
	if when != "" {
		if changed {
			lines = append(lines, T(lang, "The time has changed: it now starts **%s**.", when))
		} else {
			lines = append(lines, T(lang, "It starts **%s**.", when))
		}
	}
	if place != "" {
		lines = append(lines, T(lang, "Meeting place: **%s**", mdEscape(place)))
	}
	if len(lines) > 0 {
		c.Body = strings.Join(lines, "  \n") + "\n\n" + c.Body
	}
	if changed && when != "" {
		subject = T(lang, "New time: %s", subject)
		c.Preheader = T(lang, "It now starts %s.", when)
		if event != "" {
			c.Heading = T(lang, "New time for %s", event)
		}
	}
	return subject, c
}

// contactLink is an organiser's address as a link to write to it.
func contactLink(contact string) string {
	return "[" + mdEscape(contact) + "](mailto:" + contact + ")"
}

// WithContact adds where a buyer writes with questions: the
// organisation's contact address; none leaves c as it is.
func WithContact(c Content, contact string) Content {
	if contact == "" {
		return c
	}
	c.Body += "\n\n" + T(c.Lang, "Questions? Write to the organiser at %s.", contactLink(contact))
	return c
}

// TeamLink is the one link a buyer sets their team up from. voucher (an
// already display-formatted code) and playURL are optional: when both
// are set the body also offers the type-it-in path, so a buyer reading
// the mail on a different device than the one they'll play on can just
// enter the code at /play. Pass "" for either to omit that line.
func TeamLink(lang, teamURL, playURL, voucher, org string) (string, Content) {
	body := T(lang, "Open your team page to claim your own place, share the others with your teammates, and start playing whenever you're ready.")
	if voucher != "" && playURL != "" {
		body += "\n\n" + T(lang, "Prefer to type it in? Go to %[1]s and enter your voucher code: **%[2]s**", playURL, voucher)
	}
	return T(lang, "Your %s ticket — team link inside", brandName(org)), Content{
		Lang:      lang,
		Preheader: T(lang, "Set up your team and start whenever you're ready."),
		Heading:   T(lang, "Thanks — your adventure is ready"),
		Body:      body,
		Buttons:   []Button{{T(lang, "Open the team page"), teamURL}},
		Footer:    T(lang, "The link works for 30 days.") + " " + lostLink(lang, playURL) + " " + playerFooter(lang, "", org),
	}
}

// AsGift turns a TeamLink mail into a free ticket from org to event: it reads as
// a gift, not as a purchase.
func AsGift(lang, org, event string, c Content) (string, Content) {
	c.Heading = T(lang, "A gift from %s — your adventure is ready", org)
	c.Preheader = T(lang, "%s has given you a ticket. Set up your team and start whenever you're ready.", org)
	if event == "" {
		event = sitename.Name()
	}
	return T(lang, "A gift from %[1]s: your ticket to %[2]s", org, event), c
}

// lostLink says where a player gets a lost link sent again: the player
// app's sign-in page, beside playURL (".../play").
func lostLink(lang, playURL string) string {
	if playURL == "" {
		return T(lang, "Lost it? Open the play page and enter this email address to get a new link.")
	}
	return T(lang, "Lost it? Go to %s and enter this email address to get a new link.", strings.TrimSuffix(playURL, "/play")+"/app/play/")
}

// FileLink is one of the files the organisers left for an event's
// players, and where to download it.
type FileLink struct {
	Name, URL string
}

// WithFiles lists the event's files under the body, one download link
// each, for a ticket or an invitation; none leaves c as it is.
func WithFiles(c Content, files []FileLink) Content {
	if len(files) == 0 {
		return c
	}
	var b strings.Builder
	b.WriteString("\n\n**" + T(c.Lang, "Files from the organisers") + "**  \n")
	b.WriteString(T(c.Lang, "Download them before you set off. They are also in the menu of the game.") + "\n\n")
	for _, f := range files {
		b.WriteString("- [" + mdEscape(f.Name) + "](" + f.URL + ")\n")
	}
	c.Body += strings.TrimRight(b.String(), "\n")
	return c
}

// WithPrints lists the adventure's print materials the buyer prints at
// home, one download link each; none leaves c as it is.
func WithPrints(c Content, files []FileLink) Content {
	if len(files) == 0 {
		return c
	}
	var b strings.Builder
	b.WriteString("\n\n**" + T(c.Lang, "To print") + "**  \n")
	b.WriteString(T(c.Lang, "Print these at home before you set off: the adventure starts with them.") + "\n\n")
	for _, f := range files {
		b.WriteString("- [" + mdEscape(f.Name) + "](" + f.URL + ")\n")
	}
	c.Body += strings.TrimRight(b.String(), "\n")
	return c
}

// PartyHost is a hosted event's host link: the page the buyer sets the
// event up and runs it from. The link is their only key, so the mail
// says so.
func PartyHost(lang, hostURL, party, org string) (string, Content) {
	return T(lang, "Your %s party — host link inside", brandName(org)), Content{
		Lang:      lang,
		Preheader: T(lang, "Set up the party, print the cards and show the players their link."),
		Heading:   T(lang, "%s is booked", party),
		Body: T(lang, "Open the host page to print and hide the cards and show the players their link. The first team to join starts the game, within 60 days of booking; play as many games as you like for 14 days from then.") +
			"\n\n" + T(lang, "Anyone with this link can run the party, so keep it to yourself."),
		Buttons: []Button{{T(lang, "Open the host page"), hostURL}},
		Footer:  T(lang, "Everything about the players is deleted a while after the party.") + " " + T(lang, "You get this because you host %s.", party),
	}
}

// HostedPlayerLink is the player link a host sends someone from the host page:
// it opens the game, where the team names itself.
func HostedPlayerLink(lang, playerURL, party, org string) (string, Content) {
	return T(lang, "Join %s", party), Content{
		Lang:      lang,
		Preheader: T(lang, "Open the link, name your team and play."),
		Heading:   T(lang, "Join %s", party),
		Body:      T(lang, "Open the link on the phone your team plays on, give the team a name and start."),
		Buttons:   []Button{{T(lang, "Join the game"), playerURL}},
		Footer:    T(lang, "You get this because the host of %s sent you the player link.", party),
	}
}

// TrailConfirm saves the trails a browser holds to an email address:
// the link confirms the address is the reader's.
func TrailConfirm(lang, link string) (string, Content) {
	return T(lang, "Save your party adventures"), Content{
		Lang:      lang,
		Preheader: T(lang, "Confirm your email address to keep your party adventures."),
		Heading:   T(lang, "Save your party adventures"),
		Body:      T(lang, "Open the link to save your party adventures to this address. Then you can open them from any phone or computer. The link works for an hour."),
		Buttons:   []Button{{T(lang, "Save my party adventures"), link}},
		Footer:    T(lang, "Didn't ask for this? Ignore this email and nothing is saved."),
	}
}

// TrailSignin opens the quiz trails saved to an email address.
func TrailSignin(lang, link string) (string, Content) {
	return T(lang, "Your party adventures"), Content{
		Lang:      lang,
		Preheader: T(lang, "Open your party adventures."),
		Heading:   T(lang, "Your party adventures"),
		Body:      T(lang, "Open the link to see every party adventure saved to this address. The link works for an hour."),
		Buttons:   []Button{{T(lang, "Open my party adventures"), link}},
		Footer:    T(lang, "Didn't ask for this? Ignore this email."),
	}
}

// TrailPaid is a paid quiz trail's host link, mailed to the buyer: where
// to print the cards and show the players their link. Stripe mails the
// receipt. Until is the day to start the first game by, or, once one
// has started, the last day to play.
func TrailPaid(lang, hostURL, playerURL, title, until, org string, started, paid bool) (string, Content) {
	keep := T(lang, "Anyone with the host link can run and change your party adventure, so keep it to yourself.")
	if paid {
		keep += " " + T(lang, "Your receipt comes in a separate email.")
	}
	when := T(lang, "Open the host page to print the cards and the answer key, and to show the players their link. Start the first game by %s; from then you can play as many games as you like for 14 days.", until)
	if started {
		when = T(lang, "Open the host page to print the cards and the answer key, and to show the players their link. You can play as many games as you like until %s.", until)
	}
	return T(lang, "%s is ready to play", title), Content{
		Lang:      lang,
		Preheader: T(lang, "Print the cards, hide them and let the teams join."),
		Heading:   T(lang, "%s is ready to play", title),
		Body: when +
			"\n\n" + T(lang, "Players join here: %s", playerURL) +
			"\n\n" + keep,
		Buttons: []Button{{T(lang, "Open the host page"), hostURL}},
		Footer:  T(lang, "The party adventure and everything about the players is deleted 14 days after the first game.") + " " + T(lang, "You get this because you host %s.", title),
	}
}

// TrailKeep offers, a week into play, to keep a quiz trail for good in
// an account of the buyer's own.
func TrailKeep(lang, keepURL, title, until string) (string, Content) {
	return T(lang, "Keep %s?", title), Content{
		Lang:      lang,
		Preheader: T(lang, "Create a free account and the party adventure is yours to keep."),
		Heading:   T(lang, "Keep %s?", title),
		Body: T(lang, "Your party adventure is deleted on %s. Create a free account and it moves there instead, cards and all, to play again whenever you like.", until) +
			"\n\n" + T(lang, "Moving it ends the games you have now; their summaries were already mailed to you."),
		Buttons: []Button{{T(lang, "Keep my party adventure"), keepURL}},
		Footer:  T(lang, "Not interested? Do nothing and it is deleted as planned."),
	}
}

// TrailEnded says a quiz trail is gone: deleted when its days were over,
// or moved into the buyer's account (kept).
func TrailEnded(lang, title string, kept bool, org string) (string, Content) {
	if kept {
		return T(lang, "%s is in your account", title), Content{
			Lang:    lang,
			Heading: T(lang, "%s is in your account", title),
			Body:    T(lang, "Your party adventure is now an adventure in your account. Edit it, print it and host it from there. The summaries of the games you played were mailed to you before."),
			Footer:  playerFooter(lang, "", org),
		}
	}
	return T(lang, "%s has ended", title), Content{
		Lang:    lang,
		Heading: T(lang, "%s has ended", title),
		Body:    T(lang, "Your party adventure's days are over, so it and everything about its players has been deleted. The summaries of the games you played were mailed to you before.") + "\n\n" + T(lang, "Thank you for playing!"),
		Footer:  playerFooter(lang, "", org),
	}
}

// RoundTeam is one team's line in a game summary.
type RoundTeam struct {
	Name     string
	Players  int
	Score    float64
	Finished bool
	Minutes  int
}

// RoundCard is one question's line in a game summary: how many of the
// teams that reached it got it right.
type RoundCard struct {
	Title   string
	Reached int
	Right   int
}

// RoundSummary is a hosted event's summary of one finished game, mailed
// to the host: who played, the ranking, and how each question went.
func RoundSummary(lang, hostURL, party, org string, round int, teams []RoundTeam, cards []RoundCard) (string, Content) {
	players := 0
	for _, t := range teams {
		players += t.Players
	}
	var b strings.Builder
	b.WriteString(T(lang, "%[1]d teams, %[2]d players.", len(teams), players))
	if len(teams) > 0 {
		b.WriteString("\n\n**" + T(lang, "Ranking") + "**\n\n")
		for i, t := range teams {
			line := T(lang, "%[1]d. %[2]s — %[3]s points", i+1, mdEscape(t.Name), formatPoints(t.Score))
			if t.Finished {
				line += ", " + T(lang, "%d min", t.Minutes)
			} else {
				line += ", " + T(lang, "did not finish")
			}
			b.WriteString(line + "\n")
		}
	}
	if len(cards) > 0 {
		b.WriteString("\n**" + T(lang, "Questions") + "**\n\n")
		for _, c := range cards {
			pct := 0
			if c.Reached > 0 {
				pct = (c.Right*200 + c.Reached) / (2 * c.Reached)
			}
			b.WriteString(T(lang, "- %[1]s: %[2]d%% right (%[3]d of %[4]d teams)", mdEscape(c.Title), pct, c.Right, c.Reached) + "\n")
		}
	}
	return T(lang, "%[1]s — results of game %[2]d", party, round), Content{
		Lang:      lang,
		Preheader: T(lang, "Who won, and how the questions went."),
		Heading:   T(lang, "Game %d is over", round),
		Body:      b.String(),
		Buttons:   []Button{{T(lang, "Open the host page"), hostURL}},
		Footer:    T(lang, "You get this because you host %s.", party),
	}
}

// formatPoints shows a score without a needless fraction.
func formatPoints(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// BoxConfirmed is a box buyer's team link, once their code checks out.
func BoxConfirmed(lang, partner, teamURL, playURL, voucher, org string) (string, Content) {
	_, c := TeamLink(lang, teamURL, playURL, voucher, org)
	c.Heading = T(lang, "Your %s box is confirmed!", partner)
	c.Footer = T(lang, "The link works for a year.") + " " + lostLink(lang, playURL)
	return T(lang, "Your %s adventure — team link inside", brandName(org)), c
}

// BoxReceived tells a box buyer their code has arrived and is being
// checked, and where their envelope goes: a wrong address is caught
// before anything is posted. address is the postal address, a line each.
func BoxReceived(lang, partner string, address []string, contact string) (string, Content) {
	subject := T(lang, "We've got your %s code", partner)
	return subject, WithEnvelope(Content{
		Lang:      lang,
		Preheader: T(lang, "We're checking it and will email your team link soon."),
		Heading:   subject,
		Body:      T(lang, "We're checking it with %s now. Once it checks out, we email your team link to this address, usually within a working day.", partner),
	}, address, contact)
}

// WithEnvelope says where the buyer's envelope is posted, a line each of
// address, so a wrong address is caught before it goes out, and whom to
// write to then (contact, optional); no address leaves c as it is.
func WithEnvelope(c Content, address []string, contact string) Content {
	if len(address) == 0 {
		return c
	}
	lines := make([]string, len(address))
	for i, l := range address {
		lines[i] = mdEscape(l)
	}
	c.Body += "\n\n" + T(c.Lang, "Your envelope goes to:") + "  \n" + strings.Join(lines, "  \n")
	if contact != "" {
		c.Body += "\n\n" + T(c.Lang, "Something wrong? Write to the organiser at %s before the envelope is posted.", contactLink(contact))
	} else {
		c.Body += "\n\n" + T(c.Lang, "Something wrong? Contact the organiser before the envelope is posted.")
	}
	return c
}

// BoxRejected tells a buyer their box's code did not check out; contact
// is the organiser's address, when there is one.
func BoxRejected(lang, partner, reason, contact string) (string, Content) {
	subject := T(lang, "About your %s box", partner)
	body := T(lang, "We couldn't confirm the code from your %[1]s box with %[1]s.", partner)
	if reason != "" {
		body += "\n\n" + T(lang, "The organiser's reason: %s", reason)
	}
	if contact != "" {
		body += "\n\n" + T(lang, "Please check the code and redeem it again, or write to %s and we'll sort it out.", contactLink(contact))
	} else {
		body += "\n\n" + T(lang, "Please check the code and redeem it again, or reply to this email and we'll sort it out.")
	}
	return subject, Content{
		Lang:    lang,
		Heading: subject,
		Body:    body,
	}
}

// BoxCancelled tells a buyer their redemption was withdrawn.
func BoxCancelled(lang, partner string) (string, Content) {
	subject := T(lang, "Your %s box redemption is cancelled", partner)
	return subject, Content{
		Lang:    lang,
		Heading: subject,
		Body:    T(lang, "Your redemption of a %s box has been cancelled. Reply to this email if that's a surprise.", partner),
	}
}

// BoxWaiting tells an organisation's admins a code waits to be checked.
func BoxWaiting(lang, link string) (string, Content) {
	subject := T(lang, "A box redemption is waiting for review")
	c := Content{Lang: lang, Heading: subject, Body: T(lang, "Someone has redeemed a partner box. Check the code in the partner's portal, then approve or reject it.")}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Review the redemption"), link}}
	}
	return subject, c
}

// --- paying for teams --------------------------------------------------------

// BalanceLow tells an organisation's admins its balance has room for
// few more teams; teams says how many, link opens Billing.
func BalanceLow(lang, balance string, teams int64, link string) (string, Content) {
	subject := T(lang, "Your balance is running low")
	c := Content{Lang: lang, Heading: subject,
		Body: T(lang, "Your balance is %[1]s: room for about %[2]d more teams. Top up so no team is turned away at the start.", balance, teams)}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Top up"), link}}
	}
	return subject, c
}

// BalanceEmpty tells an organisation's admins its balance is used up:
// new teams can't start until it is topped up.
func BalanceEmpty(lang, balance, link string) (string, Content) {
	subject := T(lang, "Your balance is used up")
	c := Content{Lang: lang, Heading: subject,
		Body: T(lang, "Your balance is %s. New teams can't start until you top up; teams already playing play on.", balance)}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Top up"), link}}
	}
	return subject, c
}

// BillFailed tells an organisation's admins its monthly bill couldn't
// be charged to its card; final says the retries are over and no new
// team can start until it is paid.
func BillFailed(lang, period, amount, reason string, final bool, link string) (string, Content) {
	subject := T(lang, "We couldn't charge your card for %s", period)
	body := T(lang, "Your bill for %[1]s, %[2]s, couldn't be charged to your card.", period, amount)
	if reason != "" {
		body += " " + T(lang, "The bank said: %s", mdEscape(reason))
	}
	if final {
		body += "\n\n" + T(lang, "New teams can't start until the bill is paid. Add a working card and pay it now.")
	} else {
		body += "\n\n" + T(lang, "We'll try again tomorrow. To avoid that, check your card or add another.")
	}
	c := Content{Lang: lang, Heading: subject, Body: body}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Open Billing"), link}}
	}
	return subject, c
}

// --- requests for a quote ----------------------------------------------------

// Request is a quote request as its emails show it. The choices are the
// form's keys (Occasion "kickoff", Budget "5k_15k", …); Date is
// YYYY-MM-DD, or Month YYYY-MM when the date is open.
type Request struct {
	Org                         string
	Occasion, Date, Month       string
	TimeOfDay, Duration         string
	People                      int
	City, Venue                 string
	HelpChoose                  bool
	Scope                       string
	GameMaster                  bool
	Extras, Languages           []string
	Budget, Currency, Message   string
	Name, Company, Email, Phone string
}

// RequestOccasion is an occasion's name, "" for one the form doesn't have.
func RequestOccasion(lang, key string) string {
	switch key {
	case "kickoff":
		return T(lang, "Kickoff")
	case "team_day":
		return T(lang, "Team day")
	case "conference":
		return T(lang, "Conference activity")
	case "customer_event":
		return T(lang, "Customer event")
	case "other":
		return T(lang, "Other occasion")
	}
	return ""
}

// requestWhen is when the event is meant to be: a date, or a month that
// is open, with the time of day and the length after it.
func requestWhen(lang string, r Request) string {
	var parts []string
	if d, err := time.Parse("2006-01-02", r.Date); err == nil {
		if lang == "sv" {
			parts = append(parts, fmt.Sprintf("%d %s %d", d.Day(), svMonths[d.Month()-1], d.Year()))
		} else {
			parts = append(parts, d.Format("2 January 2006"))
		}
	} else if m, err := time.Parse("2006-01", r.Month); err == nil {
		month := m.Format("January 2006")
		if lang == "sv" {
			month = fmt.Sprintf("%s %d", svMonths[m.Month()-1], m.Year())
		}
		parts = append(parts, T(lang, "sometime in %s", month))
	}
	switch r.TimeOfDay {
	case "morning":
		parts = append(parts, T(lang, "morning"))
	case "afternoon":
		parts = append(parts, T(lang, "afternoon"))
	case "evening":
		parts = append(parts, T(lang, "evening"))
	}
	switch r.Duration {
	case "1h":
		parts = append(parts, T(lang, "1 hour"))
	case "2h":
		parts = append(parts, T(lang, "2 hours"))
	case "3h":
		parts = append(parts, T(lang, "3 hours"))
	case "half_day":
		parts = append(parts, T(lang, "half a day"))
	}
	return strings.Join(parts, ", ")
}

// requestBudget is a budget range in the instance's currency.
func requestBudget(lang, key, currency string) string {
	k := func(n int) string { return wholeMoney(lang, n, currency) }
	switch key {
	case "under_5k":
		return T(lang, "under %s", k(5000))
	case "5k_15k":
		return k(5000) + " – " + k(15000)
	case "15k_40k":
		return k(15000) + " – " + k(40000)
	case "over_40k":
		return T(lang, "over %s", k(40000))
	case "unknown":
		return T(lang, "doesn't know yet")
	}
	return ""
}

// wholeMoney is a round amount with its thousands grouped: "15 000 SEK"
// in Swedish, "15,000 SEK" in English.
func wholeMoney(lang string, units int, currency string) string {
	sep := ","
	if lang == "sv" {
		sep = "\u00a0"
	}
	s := strconv.Itoa(units)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + sep + s[i:]
	}
	return s + " " + strings.ToUpper(currency)
}

// requestLines are a request's answers, a line each, as Markdown.
func requestLines(lang string, r Request) []string {
	line := func(label, value string) string { return "**" + label + ":** " + value }
	out := []string{line(T(lang, "Occasion"), RequestOccasion(lang, r.Occasion))}
	if w := requestWhen(lang, r); w != "" {
		out = append(out, line(T(lang, "When"), w))
	}
	out = append(out, line(T(lang, "People"), fmt.Sprint(r.People)))
	where := mdEscape(r.City)
	if r.Venue != "" {
		where += ", " + mdEscape(r.Venue)
	}
	if r.HelpChoose {
		where += " " + T(lang, "(wants help choosing where)")
	}
	out = append(out, line(T(lang, "Where"), where))
	var scope string
	switch r.Scope {
	case "ready_made":
		scope = T(lang, "a ready-made adventure they run themselves")
	case "adapted":
		scope = T(lang, "an adventure adapted for them")
	case "new":
		scope = T(lang, "a new adventure made for them")
	}
	if r.GameMaster {
		scope += ", " + T(lang, "with a game master on the day")
	}
	out = append(out, line(T(lang, "Wants"), scope))
	var extras []string
	for _, x := range r.Extras {
		switch x {
		case "own_questions":
			extras = append(extras, T(lang, "their own questions"))
		case "logo":
			extras = append(extras, T(lang, "their logo"))
		case "accessibility":
			extras = append(extras, T(lang, "accessible for everyone"))
		case "prize_ceremony":
			extras = append(extras, T(lang, "a prize ceremony"))
		case "big_screen":
			extras = append(extras, T(lang, "results on a big screen"))
		}
	}
	if len(extras) > 0 {
		out = append(out, line(T(lang, "Also"), strings.Join(extras, ", ")))
	}
	var langs []string
	for _, l := range r.Languages {
		switch l {
		case "sv":
			langs = append(langs, T(lang, "Swedish"))
		case "en":
			langs = append(langs, T(lang, "English"))
		}
	}
	if len(langs) > 0 {
		out = append(out, line(T(lang, "Language"), strings.Join(langs, ", ")))
	}
	if b := requestBudget(lang, r.Budget, r.Currency); b != "" {
		out = append(out, line(T(lang, "Budget"), b))
	}
	if strings.TrimSpace(r.Message) != "" {
		out = append(out, line(T(lang, "Message"), "  \n"+strings.Join(strings.Split(mdEscape(strings.TrimSpace(r.Message)), "\n"), "  \n")))
	}
	return out
}

// requestTitle is a request in a few words: "Kickoff, 12 May 2026, 100
// people".
func requestTitle(lang string, r Request) string {
	parts := []string{RequestOccasion(lang, r.Occasion)}
	if d, err := time.Parse("2006-01-02", r.Date); err == nil {
		if lang == "sv" {
			parts = append(parts, fmt.Sprintf("%d %s", d.Day(), svMonths[d.Month()-1]))
		} else {
			parts = append(parts, d.Format("2 January"))
		}
	}
	parts = append(parts, T(lang, "%d people", r.People))
	return strings.Join(parts, ", ")
}

// RequestNew brings an organisation a request for a quote: the answers,
// and who asked. Replying goes to the requester; link opens the
// organisation's requests.
func RequestNew(lang string, r Request, link string) (string, Content) {
	subject := T(lang, "Quote request: %s", requestTitle(lang, r))
	who := []string{"**" + mdEscape(r.Name) + "**"}
	if r.Company != "" {
		who = append(who, mdEscape(r.Company))
	}
	who = append(who, contactLink(r.Email))
	if r.Phone != "" {
		who = append(who, "["+mdEscape(r.Phone)+"](tel:"+telDigits(r.Phone)+")")
	}
	body := T(lang, "%s asks for a quote. Reply to this email to answer them.", mdEscape(r.Name)) +
		"\n\n" + strings.Join(requestLines(lang, r), "  \n") +
		"\n\n" + strings.Join(who, "  \n")
	c := Content{Lang: lang, Preheader: requestTitle(lang, r), Heading: T(lang, "A request for a quote"), Body: body}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Open your requests"), link}}
	}
	return subject, c
}

// RequestReceived tells a requester their request reached the
// organisation, with what they asked for, so a mistake is caught. contact
// is the organisation's address, when there is one.
func RequestReceived(lang string, r Request, contact string) (string, Content) {
	subject := T(lang, "Your request to %s", r.Org)
	body := T(lang, "Thanks, %[1]s. %[2]s has your request and will get back to you with a quote.", mdEscape(r.Name), mdEscape(r.Org)) +
		"\n\n" + strings.Join(requestLines(lang, r), "  \n")
	if contact != "" {
		body += "\n\n" + T(lang, "Something to add? Write to %s.", contactLink(contact))
	} else {
		body += "\n\n" + T(lang, "Something to add? Reply to this email.")
	}
	return subject, Content{Lang: lang, Preheader: requestTitle(lang, r), Heading: T(lang, "Thanks for your request"), Body: body}
}

// BookOrdered confirms a book order to its buyer: what was bought and
// where it will be posted. contact is the seller's address, when there
// is one.
func BookOrdered(lang, org, book string, address []string, contact string) (string, Content) {
	subject := T(lang, "Your order: %s", book)
	body := T(lang, "Thanks for your order! %[1]s posts **%[2]s** to:", mdEscape(org), mdEscape(book)) +
		"\n\n" + addressBlock(address)
	if contact != "" {
		body += "\n\n" + T(lang, "Questions about your order? Write to %s.", contactLink(contact))
	}
	return subject, Content{Lang: lang, Preheader: book, Heading: T(lang, "Thanks for your order"), Body: body}
}

// BookOrderNew tells the seller a book was ordered and is to be posted,
// with a button to its orders.
func BookOrderNew(lang, book string, address []string, link string) (string, Content) {
	subject := T(lang, "New order: %s", book)
	body := T(lang, "**%s** was ordered. Post it to:", mdEscape(book)) + "\n\n" + addressBlock(address)
	return subject, Content{Lang: lang, Heading: subject, Body: body, Buttons: []Button{{T(lang, "Orders to post"), link}}}
}

// addressBlock is a postal address, a line each.
func addressBlock(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = mdEscape(l)
	}
	return strings.Join(out, "  \n")
}

// telDigits is a phone number as a tel: link takes it: its digits, and a
// leading plus.
func telDigits(phone string) string {
	var b strings.Builder
	for i, r := range strings.TrimSpace(phone) {
		if (r >= '0' && r <= '9') || (r == '+' && i == 0) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ListingWaiting tells the platform's admins an organisation's listing
// waits for review before it goes public.
func ListingWaiting(lang, org, link string) (string, Content) {
	subject := T(lang, "A listing is waiting for review")
	c := Content{Lang: lang, Heading: subject, Body: T(lang, "%s has put something up for sale. It goes public once it is approved.", mdEscape(org))}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Review listings"), link}}
	}
	return subject, c
}

// ListingLogHead gives the platform's admins the listing review log's
// latest hash, to keep: should the log be rewritten, its head no longer
// matches the one they were sent.
func ListingLogHead(lang, head string, entries int64, problems int, link string) (string, Content) {
	subject := T(lang, "Listing review log: today's fingerprint")
	status := T(lang, "Today's check found nothing wrong: the log adds up, and approved listings serve what was approved.")
	if problems > 0 {
		subject = T(lang, "Listing review check: %d problems", problems)
		status = T(lang, "Today's check found %d problems: something was changed outside review. Open the log and check it.", problems)
	}
	c := Content{Lang: lang, Heading: subject,
		Body: status + "\n\n" + T(lang, "The listing review log holds %d entries. Its latest hash is:", entries) + "\n\n`" + head + "`\n\n" +
			T(lang, "Keep this email. Checking the log recomputes every hash; if its latest no longer matches one sent here, the log was changed.")}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Check the log"), link}}
	}
	return subject, c
}

// EventResultsSealed gives an organisation's admins an event's results
// fingerprint once the event is over, to keep: should the record be
// rewritten, its fingerprint no longer matches the one they were sent.
func EventResultsSealed(lang, event, ended, fingerprint string, entries int64, problems int, link string) (string, Content) {
	subject := T(lang, "Results of “%s”: the fingerprint to keep", event)
	status := T(lang, "“%s” ended %s. Its results record is sealed and adds up: %d entries, every answer, hint and score change.", mdEscape(event), ended, entries)
	if problems > 0 {
		subject = T(lang, "Results of “%s”: %d problems", event, problems)
		status = T(lang, "“%s” ended %s. Checking its results record found %d problems: results were changed outside the game. Open the event and check them.", mdEscape(event), ended, problems)
	}
	c := Content{Lang: lang, Heading: subject,
		Body: status + "\n\n" + T(lang, "The event's fingerprint is:") + "\n\n`" + fingerprint + "`\n\n" +
			T(lang, "Keep this email. Check results on the event page recomputes the record; if its fingerprint no longer matches this one, the results were changed after the event.")}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Check results"), link}}
	}
	return subject, c
}

// ListingApproved tells an organisation's admins their listing is public.
func ListingApproved(lang, what, link string) (string, Content) {
	subject := T(lang, "“%s” is approved", what)
	c := Content{Lang: lang, Heading: subject, Body: T(lang, "Your listing for “%s” is approved and now public.", mdEscape(what))}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Open it"), link}}
	}
	return subject, c
}

// ListingRejected tells an organisation's admins their listing was
// turned down, and why. What was public before stays up.
func ListingRejected(lang, what, reason, link string) (string, Content) {
	subject := T(lang, "“%s” was not approved", what)
	c := Content{
		Lang:    lang,
		Heading: subject,
		Body:    T(lang, "Your listing for “%s” was not approved. If an earlier version is public, it stays as it was. Change the listing, save it and press Submit for review.", mdEscape(what)),
		Quote:   reason,
	}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Open it"), link}}
	}
	return subject, c
}

// OrgPageApproved tells an organisation's admins their organisation
// page change is public: a review decision, worded as the page and not
// as a listing.
func OrgPageApproved(lang, org, link string) (string, Content) {
	subject := T(lang, "Your organisation page is approved")
	c := Content{Lang: lang, Heading: subject, Body: T(lang, "The changes to the organisation page of “%s” are approved and now public.", mdEscape(org))}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Open it"), link}}
	}
	return subject, c
}

// OrgPageRejected tells an organisation's admins their organisation
// page change was turned down, and why. What was public stays up.
func OrgPageRejected(lang, org, reason, link string) (string, Content) {
	subject := T(lang, "Your organisation page was not approved")
	c := Content{
		Lang:    lang,
		Heading: subject,
		Body:    T(lang, "The changes to the organisation page of “%s” were not approved. What is public now stays as it was. Change the page under Settings → Organization, save it and press Submit for review.", mdEscape(org)),
		Quote:   reason,
	}
	if link != "" {
		c.Buttons = []Button{{T(lang, "Open it"), link}}
	}
	return subject, c
}

// PlayLinks is every link an address has, sent when it asks.
func PlayLinks(lang string, links []PlayerLink, org string) (string, Content) {
	var b strings.Builder
	b.WriteString(T(lang, "Here are the links for the teams this address is registered with. Each works on one device: opening it claims it for that device."))
	b.WriteString("\n\n")
	for _, l := range links {
		if l.Event != "" {
			b.WriteString(T(lang, "- **%[1]s** in %[2]s, team %[3]s: [%[4]s](%[4]s)\n", mdEscape(l.Player), mdEscape(l.Event), mdEscape(l.Team), l.URL))
		} else {
			b.WriteString(T(lang, "- **%[1]s**, team %[2]s: [%[3]s](%[3]s)\n", mdEscape(l.Player), mdEscape(l.Team), l.URL))
		}
	}
	return T(lang, "Your %s play links", brandName(org)), Content{
		Lang:      lang,
		Preheader: T(lang, "Your way back into the game."),
		Heading:   T(lang, "Your play links"),
		Body:      b.String(),
		Footer:    T(lang, "You get this because someone asked for the links of this address. Your links are the same as before."),
	}
}

// GameEmail is what an email step sends a team as it passes. Its words
// are the author's, in the adventure's language; only the footer is ours.
func GameEmail(lang, subject, body, event, org string) (string, Content) {
	return subject, Content{
		Lang:      lang,
		Preheader: firstLine(body),
		Body:      Lines(body),
		Footer:    playerFooter(lang, event, org),
	}
}

// TeamMessage is what an arranger writes to the teams of an event.
func TeamMessage(lang, subject, body, event, org string) (string, Content) {
	return subject, Content{
		Lang:      lang,
		Preheader: firstLine(body),
		Heading:   subject,
		Body:      Lines(body),
		Footer:    playerFooter(lang, event, org),
	}
}

// EventCancelled tells a team the event it was invited to is cancelled;
// contact is the organisers' address, when there is one.
func EventCancelled(lang, team, event, org, contact string) (string, Content) {
	subject := T(lang, "%s is cancelled", event)
	body := T(lang, "Hi %[1]s! The organisers have cancelled %[2]s, so your player links no longer work. Questions? Contact the organisers.", mdEscape(team), mdEscape(event))
	if contact != "" {
		body = T(lang, "Hi %[1]s! The organisers have cancelled %[2]s, so your player links no longer work. Questions? Write to the organisers at %[3]s.", mdEscape(team), mdEscape(event), contactLink(contact))
	}
	return subject, Content{
		Lang:      lang,
		Preheader: T(lang, "Your player links no longer work."),
		Heading:   subject,
		Body:      body,
		Footer:    playerFooter(lang, event, org),
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 110 {
		return string(r[:110]) + "…"
	}
	return s
}

// --- buying from the platform --------------------------------------------------

// Receipt is a paid event pass as its receipt states it.
type Receipt struct {
	Number    int64
	Seller    string
	Buyer     string
	Adventure string
	Players   int
	// TopUp is players added to a pass the buyer already had.
	TopUp       bool
	Paid        time.Time
	AmountCents int
	Currency    string
	Refunded    bool
}

// money is an amount as a receipt states it: "2205.00 SEK", with a
// decimal comma in Swedish.
func money(lang string, cents int, currency string) string {
	s := fmt.Sprintf("%d.%02d %s", cents/100, cents%100, strings.ToUpper(currency))
	if lang == "sv" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// PurchaseReceipt is an event pass's receipt, in time zone tz. Mailed to
// its buyer, link is where it downloads; without one it is the receipt
// a download serves.
func PurchaseReceipt(lang string, r Receipt, tz string, link string) (string, Content) {
	paid := r.Paid.In(Location(tz))
	date := paid.Format("2 January 2006")
	if lang == "sv" {
		date = fmt.Sprintf("%d %s %d", paid.Day(), svMonths[paid.Month()-1], paid.Year())
	}
	item := T(lang, "Event pass for %[1]d players: %[2]s", r.Players, r.Adventure)
	if r.TopUp {
		item = T(lang, "%[1]d more players on an event pass: %[2]s", r.Players, r.Adventure)
	}
	lines := []string{
		T(lang, "Receipt no. %d", r.Number),
		T(lang, "Date: %s", date),
		T(lang, "Seller: %s", mdEscape(r.Seller)),
		T(lang, "Buyer: %s", mdEscape(r.Buyer)),
		T(lang, "Item: %s", mdEscape(item)),
		T(lang, "Amount paid: **%s**", money(lang, r.AmountCents, r.Currency)),
	}
	if r.Refunded {
		lines = append(lines, T(lang, "This purchase has been refunded."))
	}
	c := Content{
		Lang:      lang,
		Preheader: T(lang, "Thanks for your purchase."),
		Heading:   T(lang, "Receipt"),
		Body:      strings.Join(lines, "  \n"),
	}
	if link != "" {
		c.Heading = T(lang, "Thanks for your purchase")
		c.Buttons = []Button{{T(lang, "Download receipt"), link}}
	}
	return T(lang, "Your %s receipt", r.Seller), c
}

// --- designers -----------------------------------------------------------------

// Work is where a designer notification points.
type Work struct {
	Adventure, Step, Link, SettingsLink string
}

func (w Work) footer(lang string) string {
	f := T(lang, "You get this because you work on %s.", w.Adventure)
	if w.SettingsLink != "" {
		f += T(lang, " Choose which emails you get in your account settings: %s", w.SettingsLink)
	}
	return f
}

func (w Work) on(lang string) string {
	if w.Step == "" {
		return w.Adventure
	}
	return T(lang, "“%[1]s” in %[2]s", w.Step, w.Adventure)
}

// Assistant is how an email names someone's assistant.
func Assistant(lang, who string) string { return T(lang, "%s's assistant", who) }

func Mention(lang, who string, w Work, comment string) (string, Content) {
	return T(lang, "%[1]s mentioned you in %[2]s", who, w.Adventure), Content{
		Lang:      lang,
		Preheader: firstLine(comment),
		Heading:   T(lang, "%s mentioned you", who),
		Body:      T(lang, "On %s:", w.on(lang)),
		Quote:     comment,
		Buttons:   []Button{{T(lang, "Open the conversation"), w.Link}},
		Footer:    w.footer(lang),
	}
}

func StepAssigned(lang, who string, w Work) (string, Content) {
	return T(lang, "%[1]s gave you “%[2]s”", who, w.Step), Content{
		Lang:      lang,
		Preheader: T(lang, "You now own a step in %s.", w.Adventure),
		Heading:   T(lang, "A step is yours"),
		Body:      T(lang, "%[1]s made you the owner of %[2]s. Its status, and the conversation about it, are in the step's inspector.", who, w.on(lang)),
		Buttons:   []Button{{T(lang, "Open the step"), w.Link}},
		Footer:    w.footer(lang),
	}
}

func SuggestionMade(lang, who string, w Work, note string) (string, Content) {
	return T(lang, "%[1]s suggested a change to “%[2]s”", who, w.Step), Content{
		Lang:      lang,
		Preheader: T(lang, "Accept or decline it in the designer."),
		Heading:   T(lang, "A suggested change"),
		Body:      T(lang, "%[1]s suggested a change to %[2]s, a step you own. You can see it field by field, then accept or decline it.", who, w.on(lang)),
		Quote:     note,
		Buttons:   []Button{{T(lang, "Review the suggestion"), w.Link}},
		Footer:    w.footer(lang),
	}
}

func SuggestionDecided(lang, who string, accepted bool, w Work) (string, Content) {
	subject, pre, heading, body := "%[1]s declined your suggestion on “%[2]s”", "Your suggestion was declined.", "Suggestion declined", "%[1]s declined the change you suggested to %[2]s."
	if accepted {
		subject, pre, heading, body = "%[1]s accepted your suggestion on “%[2]s”", "Your suggestion was accepted.", "Suggestion accepted", "%[1]s accepted the change you suggested to %[2]s."
	}
	return T(lang, subject, who, w.Step), Content{
		Lang:      lang,
		Preheader: T(lang, pre),
		Heading:   T(lang, heading),
		Body:      T(lang, body, who, w.on(lang)),
		Buttons:   []Button{{T(lang, "Open the step"), w.Link}},
		Footer:    w.footer(lang),
	}
}

func ReviewRequested(lang, who string, w Work) (string, Content) {
	return T(lang, "“%s” is ready for review", w.Step), Content{
		Lang:      lang,
		Preheader: T(lang, "%s asks for a look.", who),
		Heading:   T(lang, "Ready for review"),
		Body:      T(lang, "%[1]s marked %[2]s as needing review. It's yours, so have a look and set it to ready — or comment on what to change.", who, w.on(lang)),
		Buttons:   []Button{{T(lang, "Review the step"), w.Link}},
		Footer:    w.footer(lang),
	}
}

// --- previews ------------------------------------------------------------------

// Kind is an email for the preview: its name and sample content.
type Kind struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Audience string `json:"audience"`
	// Sample builds it with made-up names, in the given brand's name.
	Sample func(lang, org string) (string, Content) `json:"-"`
}

func sampleWork(lang string) Work {
	return Work{Adventure: T(lang, "Harbour Mystery"), Step: T(lang, "The keeper's logbook"), Link: "https://example.org/app/arrange/", SettingsLink: "https://example.org/app/arrange/#/settings/account"}
}

// Kinds are every email, for the preview, players' first.
var Kinds = []Kind{
	{"team_invitation", "Team invitation", "players", func(lang, org string) (string, Content) {
		return TeamInvitation(lang, T(lang, "The Seagulls"), T(lang, "Harbour Mystery"), org, []PlayerLink{
			{Player: "Anna", URL: "https://example.org/play?t=a1b2c3"},
			{Player: "Ben", URL: "https://example.org/play?t=d4e5f6"},
		}, true)
	}},
	{"team_message", "Message to teams", "players", func(lang, org string) (string, Content) {
		body := strings.ReplaceAll(T(lang, "Hi {{team}}!\n\nWe start from the old lighthouse — look for the **red flag**.\nBring a charged phone and good shoes.\n\nSee you there!"), "{{team}}", T(lang, "The Seagulls"))
		return TeamMessage(lang, T(lang, "Meet at the lighthouse at 14:00"), body, T(lang, "Harbour Mystery"), org)
	}},
	{"event_cancelled", "Event cancelled", "players", func(lang, org string) (string, Content) {
		return EventCancelled(lang, T(lang, "The Seagulls"), T(lang, "Harbour Mystery"), org, "")
	}},
	{"game_email", "Email during the game", "players", func(lang, org string) (string, Content) {
		return GameEmail(lang, T(lang, "A letter has arrived"), T(lang, "Dear detectives,\n\nThe keeper's last entry mentions a tide table. Find it before the water rises.\n\n— A friend"), T(lang, "Harbour Mystery"), org)
	}},
	{"team_link", "Ticket: team link", "players", func(lang, org string) (string, Content) {
		return TeamLink(lang, "https://example.org/team/x7y8z9", "https://example.org/play", "X7Y8Z9", org)
	}},
	{"play_links", "Lost links", "players", func(lang, org string) (string, Content) {
		return PlayLinks(lang, []PlayerLink{{Team: T(lang, "The Seagulls"), Player: "Anna", Event: T(lang, "Harbour Mystery"), URL: "https://example.org/play?t=a1b2c3"}}, org)
	}},
	{"mention", "Mentioned in a conversation", "designers", func(lang, _ string) (string, Content) {
		return Mention(lang, "anna", sampleWork(lang), T(lang, "@ben could you make the second hint a bit less obvious?"))
	}},
	{"step_assigned", "Given a step", "designers", func(lang, _ string) (string, Content) {
		return StepAssigned(lang, "anna", sampleWork(lang))
	}},
	{"suggestion", "Suggestion on your step", "designers", func(lang, _ string) (string, Content) {
		return SuggestionMade(lang, "anna", sampleWork(lang), T(lang, "A clearer title, so teams know where to look."))
	}},
	{"review_requested", "Review requested", "designers", func(lang, _ string) (string, Content) {
		return ReviewRequested(lang, "anna", sampleWork(lang))
	}},
}

// KindByID finds a preview kind.
func KindByID(id string) (Kind, bool) {
	for _, k := range Kinds {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}

// Money is an amount in cents as an email states it: "245.00 SEK", with
// a decimal comma in Swedish.
func Money(lang string, cents int64, currency string) string {
	if cents < 0 {
		return "−" + money(lang, int(-cents), currency)
	}
	return money(lang, int(cents), currency)
}
