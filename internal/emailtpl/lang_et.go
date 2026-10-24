package emailtpl

import (
	"fmt"
	"time"
)

// Estonian, this fork's: what players, ticket and box buyers and party
// hosts get, and the frame around every email (estonian_test.go). What
// organisers and designers get goes out in English.
//
// The words follow the player app's (web/apps/play/src/locales/et.json):
// "sina" to the reader, "teie" to a team greeted by name, a name never
// inflected but named after its kind ("üritusel %s", "võistkond %s"), a
// date after a colon or "on", times as 14.00.
var etCatalog = map[string]string{
	// The page around every email.
	"%s · sent with %s": "%s · saadetud keskkonnast %s",

	// Pictures' alt text.
	"A dotted route across a town, from map pin to map pin": "Punktiirjoonega teekond üle linna, peatusest peatuseni",
	"An envelope flying out of a phone":                     "Telefonist välja lendav ümbrik",
	"A golden ticket rising out of an envelope":             "Ümbrikust kerkiv kuldne pilet",
	"A wrapped gift box with a key tag":                     "Kinkekarp, mille paela küljes ripub võti",
	"The adventure's cover picture":                         "Seikluse kaanepilt",

	// Players.
	"You get this because you play with %s.":                 "Said selle kirja, sest osaled mängus, mille korraldaja on %s.",
	"You get this because your team plays %[1]s with %[2]s.": "Said selle kirja, sest sinu võistkond osaleb üritusel %[1]s, mille korraldaja on %[2]s.",
	"Your team **%s** is ready to play. Forward each link to the right teammate — opening it claims that player's place and starts their game.": "Sinu võistkond **%s** on mänguks valmis. Saada iga link edasi õigele võistkonnakaaslasele – kes lingi avab, saab selle mängija koha ja tema mäng algab.",
	"A calendar invitation is attached, so the event lands in your calendar at the right time.":                                                 "Kaasas on kalendrikutse, et saaksid ürituse õige kellaajaga oma kalendrisse lisada.",
	"Player %d":          "Mängija %d",
	"Team %d":            "Võistkond %d",
	"You're in!":         "Oled oodatud!",
	"You're in: %s":      "Oled oodatud: %s",
	"Your %s team links": "%s: sinu võistkonna lingid",
	"The links for team %s — one per player.":                            "Võistkonna %s lingid – igale mängijale oma.",
	"Your %s ticket — team link inside":                                  "%s: sinu pilet ja võistkonna link",
	"Set up your team and start whenever you're ready.":                  "Pane võistkond kokku ja alusta siis, kui oled valmis.",
	"Your %s party — host link inside":                                   "%s: sinu pidu ja mängujuhi link",
	"Set up the party, print the cards and show the players their link.": "Valmista pidu ette, prindi kaardid välja ja näita mängijatele nende linki.",
	"%s is booked": "%s on broneeritud",
	"Open the host page to print and hide the cards and show the players their link. The first team to join starts the game, within 60 days of booking; play as many games as you like for 14 days from then.": "Ava mängujuhi leht, et kaardid välja printida ja ära peita ning mängijatele nende linki näidata. Mäng algab, kui esimene võistkond liitub; see peab juhtuma 60 päeva jooksul pärast broneerimist. Sellest alates saad 14 päeva jooksul mängida nii mitu mängu, kui soovid.",
	"Save your party adventures":                                "Salvesta oma peoseiklused",
	"Confirm your email address to keep your party adventures.": "Kinnita oma e-posti aadress, et peoseiklused alles jääksid.",
	"Open the link to save your party adventures to this address. Then you can open them from any phone or computer. The link works for an hour.": "Ava link, et salvestada peoseiklused selle aadressi alla. Siis saad need avada mis tahes telefonist või arvutist. Link kehtib ühe tunni.",
	"Save my party adventures":                                     "Salvesta minu peoseiklused",
	"Didn't ask for this? Ignore this email and nothing is saved.": "Sa ei palunud seda? Jäta see kiri tähelepanuta ja midagi ei salvestata.",
	"Your party adventures":                                        "Sinu peoseiklused",
	"Open your party adventures.":                                  "Ava oma peoseiklused.",
	"Open the link to see every party adventure saved to this address. The link works for an hour.": "Ava link, et näha kõiki selle aadressi alla salvestatud peoseiklusi. Link kehtib ühe tunni.",
	"Open my party adventures":                           "Ava minu peoseiklused",
	"Didn't ask for this? Ignore this email.":            "Sa ei palunud seda? Jäta see kiri tähelepanuta.",
	"%s is ready to play":                                "%s on mänguks valmis",
	"Print the cards, hide them and let the teams join.": "Prindi kaardid välja, peida need ära ja lase võistkondadel liituda.",
	"Open the host page to print the cards and the answer key, and to show the players their link. Start the first game by %s; from then you can play as many games as you like for 14 days.": "Ava mängujuhi leht, et kaardid ja vastuste leht välja printida ning mängijatele nende linki näidata. Esimese mängu viimane alguspäev on %s. Esimesest mängust alates saad 14 päeva jooksul mängida nii mitu mängu, kui soovid.",
	"Open the host page to print the cards and the answer key, and to show the players their link. You can play as many games as you like until %s.":                                          "Ava mängujuhi leht, et kaardid ja vastuste leht välja printida ning mängijatele nende linki näidata. Mängida saad nii mitu mängu, kui soovid; viimane mängupäev on %s.",
	"Players join here: %s": "Mängijad liituvad siin: %s",
	"Anyone with the host link can run and change your party adventure, so keep it to yourself.":    "Mängujuhi lingiga saab igaüks sinu peoseiklust juhtida ja muuta, nii et hoia see enda teada.",
	"Your receipt comes in a separate email.":                                                       "Kviitung tuleb eraldi kirjaga.",
	"%[1]s — party adventure, up to %[2]d teams per game":                                           "%[1]s – peoseiklus, võistkondi mängus kuni %[2]d",
	"%[1]s — more teams, up to %[2]d teams per game":                                                "%[1]s – lisavõistkonnad, võistkondi mängus kuni %[2]d",
	"The party adventure and everything about the players is deleted 14 days after the first game.": "Peoseiklus ja kõik mängijate andmed kustutatakse 14 päeva pärast esimest mängu.",
	"Keep %s?": "%s – kas jätad selle alles?",
	"Create a free account and the party adventure is yours to keep.":                                                                          "Loo tasuta konto ja peoseiklus jääb sulle alles.",
	"Your party adventure is deleted on %s. Create a free account and it moves there instead, cards and all, to play again whenever you like.": "Sinu peoseikluse kustutamise päev on %s. Kui lood tasuta konto, kolib see koos kõigi kaartidega hoopis sinna ja saad seda millal tahes uuesti mängida.",
	"Moving it ends the games you have now; their summaries were already mailed to you.":                                                       "Kolimine lõpetab praegused mängud; nende kokkuvõtted on sulle juba saadetud.",
	"Keep my party adventure":                                  "Jäta minu peoseiklus alles",
	"Not interested? Do nothing and it is deleted as planned.": "Ei soovi? Ära tee midagi ja see kustutatakse plaanipäraselt.",
	"%s is in your account":                                    "%s on nüüd sinu kontol",
	"Your party adventure is now an adventure in your account. Edit it, print it and host it from there. The summaries of the games you played were mailed to you before.": "Sinu peoseiklus on nüüd seiklus sinu kontol. Seal saad seda muuta, välja printida ja mängujuhina läbi viia. Mängitud mängude kokkuvõtted on sulle juba varem saadetud.",
	"%s has ended": "%s on lõppenud",
	"Your party adventure's days are over, so it and everything about its players has been deleted. The summaries of the games you played were mailed to you before.": "Sinu peoseikluse aeg on läbi, nii et see ja kõik mängijate andmed on kustutatud. Mängitud mängude kokkuvõtted on sulle juba varem saadetud.",
	"Thank you for playing!": "Aitäh, et mängisid!",
	"Join %s":                "Tule mängima: %s",
	"You get this because the host of %s sent you the player link.":                  "Said selle kirja, sest mängujuht saatis sulle mängijalingi peole %s.",
	"Open the link, name your team and play.":                                        "Ava link, anna võistkonnale nimi ja mängi.",
	"Open the link on the phone your team plays on, give the team a name and start.": "Ava link telefonis, millega sinu võistkond mängib, anna võistkonnale nimi ja alusta.",
	"Join the game":               "Liitu mänguga",
	"%[1]d teams, %[2]d players.": "Võistkondi: %[1]d, mängijaid: %[2]d.",
	"Ranking":                     "Edetabel",
	"%[1]d. %[2]s — %[3]s points": "%[1]d. %[2]s – %[3]s p",
	"%d min":                      "%d min",
	"did not finish":              "ei jõudnud lõpuni",
	"Questions":                   "Küsimused",
	"- %[1]s: %[2]d%% right (%[3]d of %[4]d teams)":                    "- %[1]s: %[2]d%% õigesti (võistkondi: %[3]d/%[4]d)",
	"%[1]s — results of game %[2]d":                                    "%[1]s – mängu %[2]d tulemused",
	"Who won, and how the questions went.":                             "Kes võitis ja kuidas küsimustega läks.",
	"Game %d is over":                                                  "Mäng %d on läbi",
	"You get this because you host %s.":                                "Said selle kirja, sest oled mängujuht peol %s.",
	"Anyone with this link can run the party, so keep it to yourself.": "Selle lingiga saab igaüks pidu juhtida, nii et hoia see enda teada.",
	"Open the host page":                                               "Ava mängujuhi leht",

	"Everything about the players is deleted a while after the party.":             "Kõik mängijate andmed kustutatakse mõni aeg pärast pidu.",
	"Thanks — your adventure is ready":                                             "Aitäh! Sinu seiklus on valmis",
	"A gift from %s — your adventure is ready":                                     "%s kinkis sulle seikluse",
	"%s has given you a ticket. Set up your team and start whenever you're ready.": "%s kinkis sulle pileti. Pane võistkond kokku ja alusta siis, kui oled valmis.",
	"A gift from %[1]s: your ticket to %[2]s":                                      "%[1]s kinkis sulle pileti: %[2]s",

	"Open your team page to claim your own place, share the others with your teammates, and start playing whenever you're ready.": "Ava võistkonna leht, võta endale koht, jaga ülejäänud kohti võistkonnakaaslastega ja alusta mängu siis, kui oled valmis.",
	"Prefer to type it in? Go to %[1]s and enter your voucher code: **%[2]s**":                                                    "Tahad koodi ise sisestada? Mine aadressile %[1]s ja sisesta oma kood: **%[2]s**",
	"Files from the organisers": "Failid korraldajatelt",
	"We're checking it with %s now. Once it checks out, we email your team link to this address, usually within a working day.": "Kontrollime koodi praegu partneri %s juures. Kui kood on korras, saadame võistkonna lingi sellele aadressile, tavaliselt ühe tööpäeva jooksul.",
	"Your envelope goes to:": "Ümbrik saadetakse aadressile:",
	"Something wrong? Contact the organiser before the envelope is posted.": "Midagi on valesti? Võta korraldajaga ühendust enne, kui ümbrik posti läheb.",
	"We've got your %s code":                                "%s: saime sinu koodi kätte",
	"We're checking it and will email your team link soon.": "Kontrollime koodi ja saadame peagi sinu võistkonna lingi.",
	"To print": "Printimiseks",
	"Print these at home before you set off: the adventure starts with them.":  "Prindi need kodus enne teeleasumist välja: seiklus algab nendega.",
	"Download them before you set off. They are also in the menu of the game.": "Laadi need alla enne teeleasumist. Need on ka mängu menüüs.",
	"Open the team page":          "Ava võistkonna leht",
	"The link works for 30 days.": "Link kehtib 30 päeva.",
	"Lost it? Open the play page and enter this email address to get a new link.": "Link kadunud? Ava mängu leht ja sisesta see e-posti aadress, siis saad uue lingi.",
	"Lost it? Go to %s and enter this email address to get a new link.":           "Link kadunud? Mine aadressile %s ja sisesta see e-posti aadress, siis saad uue lingi.",
	"Your %s play links":           "%s: sinu mängulingid",
	"Your way back into the game.": "Sinu tee tagasi mängu.",
	"Your play links":              "Sinu mängulingid",
	"Here are the links for the teams this address is registered with. Each works on one device: opening it claims it for that device.": "Siin on nende võistkondade lingid, millega see aadress on seotud. Iga link töötab korraga ühes seadmes: link kuulub seadmele, mis selle avab.",
	"- **%[1]s** in %[2]s, team %[3]s: [%[4]s](%[4]s)\n":                                                   "- **%[1]s**, üritus %[2]s, võistkond %[3]s: [%[4]s](%[4]s)\n",
	"- **%[1]s**, team %[2]s: [%[3]s](%[3]s)\n":                                                            "- **%[1]s**, võistkond %[2]s: [%[3]s](%[3]s)\n",
	"You get this because someone asked for the links of this address. Your links are the same as before.": "Said selle kirja, sest keegi küsis selle aadressi linke. Sinu lingid on samad mis enne.",
	"Your %s box is confirmed!":                                                                            "%s: sinu karp on kinnitatud!",
	"The link works for a year.":                                                                           "Link kehtib ühe aasta.",
	"Your %s adventure — team link inside":                                                                 "%s: sinu seiklus ja võistkonna link",
	"About your %s box":                                                                                    "%s: sinu karbi kood",
	"We couldn't confirm the code from your %[1]s box with %[1]s.":                                         "Me ei saanud sinu karbi koodi partneri %[1]s juures kinnitada.",
	"The organiser's reason: %s":                                                                           "Korraldaja põhjendus: %s",
	"Please check the code and redeem it again, or reply to this email and we'll sort it out.":             "Kontrolli koodi ja lunasta see uuesti või vasta sellele kirjale – lahendame asja.",
	"Your %s box redemption is cancelled":                                                                  "%s: karbi lunastamine on tühistatud",
	"Your redemption of a %s box has been cancelled. Reply to this email if that's a surprise.":            "Sinu karbi (%s) lunastamine on tühistatud. Kui see tuleb üllatusena, vasta sellele kirjale.",

	// Samples for the previews.
	"The Seagulls":                    "Kajakad",
	"Harbour Mystery":                 "Sadama saladus",
	"Meet at the lighthouse at 14:00": "Kohtume tuletorni juures kell 14.00",
	"Hi {{team}}!\n\nWe start from the old lighthouse — look for the **red flag**.\nBring a charged phone and good shoes.\n\nSee you there!": "Tere, {{team}}!\n\nAlustame vana tuletorni juurest – otsige **punast lippu**.\nVõtke kaasa laetud telefon ja head jalanõud.\n\nKohtumiseni!",
	"A letter has arrived": "Saabus kiri",
	"Dear detectives,\n\nThe keeper's last entry mentions a tide table. Find it before the water rises.\n\n— A friend": "Kallid detektiivid!\n\nTuletornivahi viimases sissekandes on juttu loodete tabelist. Leidke see enne, kui vesi tõuseb.\n\n– Üks sõber",
	"A message from the organisers":                                               "Sõnum korraldajatelt",
	"New file: %s. Open the menu and look under “Files from the organisers”.":     "Uus fail: %s. Ava menüü ja vaata jaotisest „Failid korraldajatelt“.",
	"%d new files: %s. Open the menu and look under “Files from the organisers”.": "Uued failid (%d): %s. Ava menüü ja vaata jaotisest „Failid korraldajatelt“.",
	"%s is cancelled":                   "%s jääb ära",
	"Your player links no longer work.": "Teie mängijalingid enam ei tööta.",
	"Hi %[1]s! The organisers have cancelled %[2]s, so your player links no longer work. Questions? Contact the organisers.": "Tere, %[1]s! Korraldajad on ürituse %[2]s ära jätnud, nii et teie mängijalingid enam ei tööta. Küsimusi? Võtke korraldajatega ühendust.",
	"Hi %s!\n\nThis is how your message will look.":                "Tere, %s!\n\nNii näeb sinu sõnum välja.",
	"Dear %s,\n\nThis is how an email step reads during the game.": "Tere, %s!\n\nNii näeb välja e-kiri, mille mäng mängu ajal saadab.",
	"Saturday's game": "laupäevane mäng",

	// Start time, meeting place and the organiser's contact address.
	"It starts **%s**.":                           "Algus: **%s**.",
	"The time has changed: it now starts **%s**.": "Aeg on muutunud. Uus algus: **%s**.",
	"Meeting place: **%s**":                       "Kogunemiskoht: **%s**",
	"New time: %s":                                "Uus aeg – %s",
	"It now starts %s.":                           "Uus algus: %s.",
	"New time for %s":                             "%s: uus aeg",
	"Questions? Write to the organiser at %s.":    "Küsimusi? Kirjuta korraldajale: %s.",
	"Something wrong? Write to the organiser at %s before the envelope is posted.":                                                     "Midagi on valesti? Kirjuta korraldajale enne, kui ümbrik posti läheb: %s.",
	"Please check the code and redeem it again, or write to %s and we'll sort it out.":                                                 "Kontrolli koodi ja lunasta see uuesti või kirjuta aadressile %s – lahendame asja.",
	"Hi %[1]s! The organisers have cancelled %[2]s, so your player links no longer work. Questions? Write to the organisers at %[3]s.": "Tere, %[1]s! Korraldajad on ürituse %[2]s ära jätnud, nii et teie mängijalingid enam ei tööta. Küsimusi? Kirjutage korraldajatele: %[3]s.",

	// The calendar entry's link (admin/event_words.go, player/recover.go).
	"Join link:": "Liitumislink:",
}

var (
	etWeekdays = []string{"pühapäev", "esmaspäev", "teisipäev", "kolmapäev", "neljapäev", "reede", "laupäev"}
	etMonths   = []string{"jaanuar", "veebruar", "märts", "aprill", "mai", "juuni", "juuli", "august", "september", "oktoober", "november", "detsember"}
)

// EstonianDate is t's day as Estonian writes it: "24. oktoober 2026".
func EstonianDate(t time.Time) string {
	return fmt.Sprintf("%d. %s %d", t.Day(), etMonths[t.Month()-1], t.Year())
}

// etWhen is When in Estonian: "laupäev, 24. oktoober 2026 kell 14.00
// (EEST)".
func etWhen(t time.Time) string {
	return fmt.Sprintf("%s, %s kell %s (%s)", etWeekdays[t.Weekday()], EstonianDate(t), t.Format("15.04"), t.Format("MST"))
}
