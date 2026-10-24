import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { play } from "@zonventure/api";
import { play as playApi } from "@zonventure/api";
import { Markdown } from "./Markdown.tsx";
import { TileBoard } from "./Tiles.tsx";
import { ChoiceForm } from "./Choices.tsx";
import { LockBoard } from "./Lock.tsx";
import { OrderBoard } from "./Order.tsx";
import { ItemSlotsBoard } from "./ItemSlots.tsx";
import { JournalNotes, useUnreadNotes } from "./Journal.tsx";
import { MediaList } from "./Media.tsx";
import { Blocks } from "./blocks/index.tsx";
import { QuizResult } from "./blocks/Quiz.tsx";
import { fillTeam, type BlockContext, type Motion } from "./blocks/context.ts";
import type { MapPoint } from "./StaticMap.tsx";
import { QrButton, canScan } from "./Qr.tsx";
import { brandVars, isHex, loadFont, withTheme } from "./brand.ts";
import { hasPicture } from "./backdrop.ts";
import "./fonts/adventure-fonts.css";
import "./animations.css";
import { formatSeconds, secondsUntil, useEngineClock } from "./clock.ts";
import { english, type Feedback, type Translate } from "./feedback.ts";

/**
 * One step of an adventure, as a player sees it.
 *
 * This is the only renderer of a View. The player app shows the live
 * game with it; the designer's preview and simulator show the draft with
 * it. An author therefore previews the exact screen, wording, colours
 * and motion a team will get — there is no "preview version" to drift.
 *
 * The look is play.html's, which players know: the brand face for
 * titles and moments, the answer bar docked at the bottom in the same
 * place on every step, a "?" for hints that opens a sheet, confetti for
 * a right answer and a shake for a wrong one, each step settling into
 * place piece by piece. All motion stops under prefers-reduced-motion.
 *
 * It is controlled: it holds only what is being typed, which sheet is
 * open and which moment is playing. Everything else comes from the view,
 * and every action goes out through onAction. It never moves on by
 * itself — a wrong optimistic advance shows the next clue to a team that
 * has not earned it.
 *
 * The surface is always light, as play.html's was: outdoor sunlight
 * readability beats night comfort, whatever the device theme.
 */
export interface PlayViewProps {
  view: play.View;
  onAction: (action: play.PlayAction) => void;
  /** An action is in flight; controls are disabled. */
  busy?: boolean;
  feedback?: Feedback | null;
  /**
   * Whether the clock runs. True in the player app; false in the
   * simulator, where time moves only when the author advances it.
   */
  live?: boolean;
  t?: Translate;
  /** Extra line under a zone requirement, such as the distance to it. */
  zoneNote?: ReactNode;
  /** The same line for any place, such as each start a team may choose. */
  placeNote?: (zone: play.PlayZone) => ReactNode;
  /** Where the team is, for compass and map blocks. */
  position?: MapPoint;
  /** The team's name, for {{team}} in a text. */
  teamName?: string;
  /** The designer's preview: blocks use no sensors and ask no permissions. */
  preview?: boolean;
  /**
   * Shows what plays out over time — a chat's messages — at once: the
   * designer's step preview, where the author edits the text.
   */
  instant?: boolean;
  /** The simulator's device, for compass and level blocks to read. */
  motion?: Motion;
  /** The simulator playing as one of several phones: a player's own pick (a mission, a sabotage) works there. */
  crew?: boolean;
  /** The adventure's palette. */
  brand?: play.Brand | null;
  /**
   * The team has moved on from the step shown: it stays, read-only, with
   * what happened there, until the player continues.
   */
  hold?: { feedback: Feedback; onContinue: () => void };
  /** The adventure's farewell, shown under the end step's own text. */
  outro?: string;
  /**
   * The finished team's place, time and recap link, for the end step.
   * Provisional while other teams still play.
   */
  finish?: play.Finish & { provisional?: boolean };
  /**
   * Scopes what the phone remembers — which journal notes are read — to
   * one team, so two teams sharing a phone do not share "new".
   */
  storageKey?: string;
  /** Sends the team's picture for a photo block; the player app's, absent elsewhere. */
  photo?: (block: string, file: File) => Promise<void>;
  /** Takes or gives back a role for a roles block; the player app's, absent elsewhere. */
  claimRole?: (role: string, claim: boolean) => Promise<void>;
  /** Hands a kit item to a teammate, and takes one a teammate hands over; the player app's, absent elsewhere. */
  giveItem?: (item: string) => Promise<play.ItemGift>;
  receiveItem?: (code: string) => Promise<{ name?: string; item: string }>;
  /** The organisers' newest message, for a bulletin block. */
  bulletin?: { text: string; created_at: string };
  /**
   * The adventure's cheat password, once this phone has unlocked
   * cheating: every answer is taken as right, and puzzles get a Solve
   * button. The server checks it on each cheat.
   */
  cheat?: string;
}

export function PlayView(props: PlayViewProps) {
  const { view } = props;
  const brand = useMemo(() => withTheme(props.brand, view.theme, view.theme_look), [props.brand, view.theme, view.theme_look]);
  const t = props.t ?? english;
  const live = props.live ?? true;
  const now = useEngineClock(view.now, live);
  const burst = useBurst(props.feedback ?? null, brand?.style);
  useAnswerSound(props.feedback ?? null, brand?.style);
  const [hintsOpen, setHintsOpen] = useState(false);

  // Paired exits: with a step transition, the step that was here leaves
  // as the next one enters. What leaves is a copy of its page, kept from
  // the last render, never its live parts, so nothing in it plays again.
  const transition = brand?.style?.transition;
  const moves = !!transition && transition !== "none";
  const rootRef = useRef<HTMLDivElement>(null);
  const stepRef = useRef<HTMLDivElement>(null);
  const kept = useRef<{ node: string | undefined; page: HTMLElement | null; top: number }>({ node: view.node, page: null, top: 0 });
  const [leaving, setLeaving] = useState<{ page: HTMLElement; top: number; id: number } | null>(null);
  useLayoutEffect(() => {
    const was = kept.current;
    if (was.page && was.node !== view.node && moves && burst?.kind !== "celebrate" && !prefersReducedMotion()) {
      setLeaving({ page: was.page, top: was.top, id: Date.now() });
    }
    const el = stepRef.current;
    const root = rootRef.current;
    kept.current =
      el && root && moves
        ? { node: view.node, page: el.cloneNode(true) as HTMLElement, top: el.getBoundingClientRect().top - root.getBoundingClientRect().top }
        : { node: view.node, page: null, top: 0 };
  });
  useEffect(() => {
    if (!leaving) return;
    const id = setTimeout(() => setLeaving(null), 300);
    return () => clearTimeout(id);
  }, [leaving]);
  const [bagOpen, setBagOpen] = useState(false);
  const [journalOpen, setJournalOpen] = useState(false);
  const storageKey = props.storageKey ?? "local";
  const unread = useUnreadNotes(view.journal, storageKey, journalOpen);
  const endsIn = view.ends_at ? secondsUntil(view.ends_at, now) : null;

  // Live, the phone calls time-up itself when the countdown ends; the
  // engine re-checks the clock, so an early or repeated call is harmless.
  const firedTimeUp = useRef(false);
  useEffect(() => {
    if (live && endsIn !== null && endsIn <= 0 && !firedTimeUp.current) {
      firedTimeUp.current = true;
      props.onAction({ kind: "timeup" });
    }
  }, [live, endsIn, props]);

  // The time-up message is shown once per team, until acknowledged.
  const upKey = `zv.timeup:${storageKey}`;
  const [upSeen, setUpSeen] = useState(() => {
    try {
      return localStorage.getItem(upKey) === "1";
    } catch {
      return false;
    }
  });
  const style = useMemo(() => brandVars(brand) as CSSProperties, [brand]);
  useEffect(() => loadFont(brand?.font), [brand?.font]);
  useEffect(() => loadFont(brand?.heading_font), [brand?.heading_font]);

  // A new step closes a sheet left open on the old one.
  useEffect(() => {
    setHintsOpen(false);
    setBagOpen(false);
    setJournalOpen(false);
  }, [view.node]);

  const kind = view.finished || view.type === "end" ? "end" : view.type;
  const step: StepProps = { ...props, t, now, openHints: () => setHintsOpen(true) };

  let body: ReactNode;
  if (kind === "start" && view.starts?.length) body = <StartChoice {...step} />;
  else if (kind === "end") body = <EndStep {...step} />;
  else if (kind === "puzzle") body = <PuzzleStep {...step} />;
  else if (kind === "story") body = <StoryStep {...step} />;
  else if (kind === "hub") body = <HubStep {...step} />;
  else if (kind === "split") body = <SplitStep {...step} />;
  else if (kind === "join") body = <JoinStep {...step} />;
  else body = <p className="pv-muted">{t("Moving on…")}</p>;

  const readyHints = view.next_hint && secondsUntil(view.next_hint.available_at, now) <= 0 ? 1 : 0;

  return (
    <div ref={rootRef} className={`pv pv--${kind ?? "none"}` + (isHex(brand?.submit_bg) ? " pv--dock-brand" : "") + (leaving ? " is-leaving" : "") + (hasPicture(brand) ? " pv--photo" : "")} data-theme="light" style={style} {...styleAttrs(brand?.style)}>
      {hasPicture(brand) && <div className="pv-backdrop-photo" aria-hidden />}
      <header className="pv-head">
        {!view.hide_score && (
          <div className="pv-score">
            <span className="pv-visually-hidden">{t("Score")} </span>
            <span className="pv-score__n">{round(view.score - view.penalty_score)}</span>
            <span className="pv-score__l">{t("pts")}</span>
          </div>
        )}
        {endsIn !== null && (
          <span className={"pv-clock" + (endsIn < 600 ? " is-urgent" : "")} aria-label={t("Time left: {{time}}", { time: formatSeconds(Math.max(0, endsIn)) })}>
            <span aria-hidden>⏱</span> {formatSeconds(Math.max(0, endsIn))}
          </span>
        )}
        {kind !== "end" && view.items?.length ? (
          <button type="button" className="pv-bag-toggle" onClick={() => setBagOpen(true)} aria-label={t("Items")}>
            <span aria-hidden>🎒</span> {view.items.length}
          </button>
        ) : null}
        {/* The journal is the case file: always one tap away, the end
            screen included, so a team can check its notes before an
            accusation and read them back afterwards. */}
        {view.has_journal || view.journal?.length ? (
          <button
            type="button"
            className="pv-bag-toggle pv-journal-toggle"
            onClick={() => setJournalOpen(true)}
            aria-label={unread ? t("Journal, {{n}} new", { n: unread }) : t("Journal")}
          >
            <span aria-hidden>📓</span> {view.journal?.length ?? 0}
            {unread > 0 && <span className="pv-hint-toggle__badge">{unread}</span>}
          </button>
        ) : null}
        {kind === "puzzle" && (view.hint_count ?? 0) > 0 && (
          <button type="button" className="pv-hint-toggle" onClick={() => setHintsOpen(true)} aria-label={t("Hints")}>
            <span className="pv-hint-toggle__glyph" aria-hidden>?</span>
            {t("Hint")}
            {readyHints > 0 && <span className="pv-hint-toggle__badge">{readyHints}</span>}
          </button>
        )}
      </header>

      {/* Inside a home screen's branch the team can always walk away and
          come back — legacy's back button, labelled with where it goes. */}
      {view.back_to && kind !== "end" && (
        <button type="button" className="pv-back" disabled={props.busy} onClick={() => props.onAction({ kind: "back", node: view.node ?? "" })}>
          <span aria-hidden>←</span> {view.back_label || t("Back")}
        </button>
      )}

      {!!view.groups?.length && kind !== "split" && kind !== "join" ? <GroupStrip view={view} t={t} busy={!!props.busy} onAction={props.onAction} /> : null}

      {view.players_min ? (
        <p className="pv-waiting" role="status">
          {t("Waiting for your team: {{n}} of {{m}} players have joined. You can start once everyone is in.", { n: view.players_in ?? 0, m: view.players_min })}
        </p>
      ) : null}

      {/* Keyed on the node, so each step plays its entrance — held back
          while a celebration covers the screen, then released. */}
      <div
        key={view.node ?? "none"}
        ref={stepRef}
        className={"pv-step" + (burst?.kind === "celebrate" ? "" : " is-entered")}
        {...(transition ? { "data-transition": transition } : {})}
        inert={!!props.hold}
      >
        {body}
      </div>
      {props.hold && (
        <div className="pv-dock pv-dock--hold">
          <FeedbackLine feedback={props.hold.feedback} />
          <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" onClick={props.hold.onContinue}>
            {t("Continue")}
          </button>
        </div>
      )}
      {leaving && transition && <Leaving key={leaving.id} page={leaving.page} top={leaving.top} transition={transition} />}

      {hintsOpen && <HintSheet {...step} onClose={() => setHintsOpen(false)} />}
      {bagOpen && (
        <Sheet title={t("Items")} t={t} onClose={() => setBagOpen(false)}>
          <Bag view={view} t={t} give={props.giveItem} receive={props.receiveItem} content={(bs) => <Blocks blocks={bs} {...blockContext(step)} />} />
        </Sheet>
      )}
      {view.timed_out && !upSeen && (
        <Sheet title={t("Time is up")} t={t} onClose={() => {
          setUpSeen(true);
          try {
            localStorage.setItem(upKey, "1");
          } catch {
            /* private mode: shown again after a reload */
          }
        }}>
          <Markdown className="pv-text" text={fillTeam(view.time_up_text, { teamName: props.teamName }) || t("The time for this adventure has run out.")} />
        </Sheet>
      )}
      {journalOpen && (
        <Sheet title={t("Journal")} t={t} onClose={() => setJournalOpen(false)}>
          <JournalNotes journal={view.journal ?? []} storageKey={storageKey} t={t} teamName={props.teamName} />
        </Sheet>
      )}
      {burst && <BurstOverlay burst={burst} style={brand?.style} />}
    </div>
  );
}

interface StepProps extends PlayViewProps {
  t: Translate;
  now: number;
  openHints: () => void;
}

// --- moments ----------------------------------------------------------------

type Burst = NonNullable<Feedback["burst"]> & { id: number };

const BURST_MS = { celebrate: 1500, rebuff: 900 } as const;

/** Plays each new feedback's burst once, unless motion is reduced. */
function useBurst(feedback: Feedback | null, style?: play.LookStyle): Burst | null {
  const [burst, setBurst] = useState<Burst | null>(null);
  const seq = useRef(0);
  useEffect(() => {
    if (!feedback?.burst || prefersReducedMotion() || style?.celebrate === "none") return;
    // The look's own words, in place of the player's.
    const own = feedback.burst.kind === "celebrate" ? style?.cheer_text : style?.miss_text;
    const b = { ...feedback.burst, ...(own?.trim() ? { head: own.trim() } : {}), id: ++seq.current };
    setBurst(b);
    const id = setTimeout(() => setBurst((cur) => (cur?.id === b.id ? null : cur)), BURST_MS[b.kind]);
    return () => clearTimeout(id);
  }, [feedback]);
  return burst;
}

/**
 * A look's styles as data attributes play-ui.css reads: buttons, the
 * answer bar and titles. Only the names in these lists ever reach the
 * page; anything else from a newer server is left off.
 */
function styleAttrs(st: play.LookStyle | undefined): Record<string, string> {
  if (!st) return {};
  const out: Record<string, string> = {};
  const pick = (attr: string, v: string | undefined, list: readonly string[]) => {
    if (v && list.includes(v)) out[attr] = v;
  };
  pick("data-btn-fill", st.button_fill, ["gradient", "flat", "outline", "soft"]);
  pick("data-btn-size", st.button_size, ["small", "large"]);
  pick("data-dock", st.dock, ["floating"]);
  pick("data-titles", st.title_style, ["ribbon", "underline", "shadow"]);
  if (st.title_upper) out["data-titles-upper"] = "";
  if (isHex(st.answer_button_color)) out["data-answer-bg"] = "";
  if (isHex(st.answer_button_color) || isHex(st.answer_button_text_color)) out["data-answer-fg"] = "";
  return out;
}

/**
 * The step that was here, leaving: its copied page, stilled, under a
 * layer that plays the exit matching the step transition. Not something
 * to read or press — it is gone in a quarter of a second.
 */
function Leaving({ page, top, transition }: { page: HTMLElement; top: number; transition: string }) {
  const layer = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const copy = page.cloneNode(true) as HTMLElement;
    copy.classList.remove("is-entered");
    copy.classList.add("pv-step--leaving");
    copy.removeAttribute("data-transition");
    for (const el of copy.querySelectorAll("[id]")) el.removeAttribute("id");
    layer.current?.replaceChildren(copy);
  }, [page]);
  return <div ref={layer} className="pv-leave" data-transition={transition} style={{ top }} aria-hidden inert />;
}

function prefersReducedMotion(): boolean {
  return typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
}

const CONFETTI = ["#e24080", "#ad1357", "#ffc93c", "#4cc38a", "#3b82f6", "#ff8a3d"];

/**
 * A look's own sound on a right or a wrong answer — the moments a burst
 * marks. Played on the team's own action, which lets a phone play it;
 * a sound the phone refuses is skipped. Reduced motion stills the burst,
 * not the sound.
 */
function useAnswerSound(feedback: Feedback | null, style?: play.LookStyle) {
  useEffect(() => {
    const kind = feedback?.burst?.kind;
    const id = kind === "celebrate" ? style?.sound_right : kind === "rebuff" ? style?.sound_wrong : undefined;
    if (!id || !(id > 0) || typeof Audio === "undefined") return;
    const sound = new Audio(playApi.mediaURL(Math.trunc(id)));
    void sound.play().catch(() => {});
    return () => sound.pause();
    // Keyed on the feedback alone: each answer plays its sound once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [feedback]);
}

/** A run of emoji split into what a reader sees as single characters. */
function graphemes(s: string): string[] {
  const seg = typeof Intl !== "undefined" && "Segmenter" in Intl ? new Intl.Segmenter(undefined, { granularity: "grapheme" }) : null;
  return seg ? Array.from(seg.segment(s), (x) => x.segment).filter((g) => g.trim()) : Array.from(s).filter((g) => g.trim());
}

function BurstOverlay({ burst, style }: { burst: Burst; style?: play.LookStyle | undefined }) {
  const colours = style?.confetti_colors?.filter(isHex).slice(0, 8);
  const palette = colours?.length ? colours : CONFETTI;
  const emoji = style?.confetti_emoji ? graphemes(style.confetti_emoji).slice(0, 8) : [];
  // Pieces are fixed per burst, so a re-render does not reshuffle them.
  const pieces = useMemo(
    () =>
      burst.kind !== "celebrate" || style?.celebrate === "quiet"
        ? []
        : Array.from({ length: emoji.length ? 24 : 36 }, (_, i) => ({
            emoji: emoji.length ? emoji[i % emoji.length] : undefined,
            left: `${Math.random() * 100}%`,
            "--c": palette[i % palette.length],
            "--d": `${Math.round(Math.random() * 350)}ms`,
            "--dx": `${Math.round((Math.random() - 0.5) * 220)}px`,
            "--rot": `${Math.round((Math.random() - 0.5) * 1080)}deg`,
          })),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [burst.id, burst.kind],
  );
  if (burst.kind === "celebrate") {
    return (
      <div className="pv-burst pv-burst--celebrate" aria-hidden>
        {pieces.map(({ emoji: e, ...p }, i) => (
          <i key={i} className={"pv-confetti" + (e ? " pv-confetti--emoji" : "")} style={p as CSSProperties}>
            {e}
          </i>
        ))}
        <div className="pv-burst__disc">
          <svg viewBox="0 0 52 52" className="pv-burst__mark">
            <path d="M14 27 l8 8 l16 -18" />
          </svg>
        </div>
        <div className="pv-burst__head">{burst.head}</div>
        {burst.sub && <div className="pv-burst__sub">{burst.sub}</div>}
      </div>
    );
  }
  return (
    <div className="pv-burst pv-burst--rebuff" aria-hidden>
      <div className="pv-burst__disc">
        <svg viewBox="0 0 52 52" className="pv-burst__mark">
          <path d="M17 17 L35 35 M35 17 L17 35" />
        </svg>
      </div>
      <div className="pv-burst__head">{burst.head}</div>
    </div>
  );
}

// --- shared pieces --------------------------------------------------------

function FeedbackLine({ feedback }: { feedback: Feedback | null | undefined }) {
  // Always rendered, so a screen reader announces each new message.
  return (
    <p className={"pv-result" + (feedback ? ` pv-result--${feedback.tone}` : "")} role="status" aria-live="polite">
      {feedback?.text}
    </p>
  );
}

/**
 * The step's title, for a step without blocks: a puzzle or story places
 * its title with a block, and shows none while that block is switched off.
 */
function Title({ view }: { view: play.View }) {
  if (view.type === "puzzle" || view.type === "story" || view.blocks?.some((b) => b.kind === "title")) return null;
  return view.title ? <h1 className="pv-title">{view.title}</h1> : null;
}

/** The place on Google Maps: in the Maps app on a phone that has it, else on the web. */
const mapURL = (z: play.PlayZone) => `https://www.google.com/maps/search/?api=1&query=${z.lat},${z.lng}`;

function ZoneCard({ view, t, note, onStation, busy }: { view: play.View; t: Translate; note?: ReactNode; onStation?: (code: string) => void; busy?: boolean }) {
  const z = view.zone;
  if (!z) return null;
  if (view.stations) return <StationCard view={view} zone={z} t={t} {...(onStation ? { onStation } : {})} busy={!!busy} />;
  // A hidden place: finding it is the puzzle, so no name, map or distance.
  const name = z.hidden ? t("the place") : z.name || t("the marked place");
  // One teammate at the place opens it for all; only they are there.
  const me = view.here_me ?? view.here;
  const others = view.here && !me;
  const count = view.here_count ?? 0;
  return (
    <div className={"pv-zone" + (view.here ? " is-here" : "")}>
      <span className="pv-zone__pin" aria-hidden>
        {view.here ? "✓" : "⌖"}
      </span>
      <div className="pv-zone__body">
        {/* Arriving is announced; the distance under it, which changes every few seconds, is not. */}
        <strong aria-live="polite">
          {me
            ? t("You are at {{place}}.", { place: name })
            : others
              ? count > 1
                ? t("{{n}} of your team are at {{place}}.", { n: count, place: name })
                : t("A teammate is at {{place}}.", { place: name })
              : z.hidden
                ? t("Find the place.")
                : t("Go to {{place}}.", { place: name })}
        </strong>
        {!me && note && !z.hidden && <span className="pv-zone__note">{note}</span>}
      </div>
      {!z.hidden && (
        <a href={mapURL(z)} target="_blank" rel="noopener noreferrer">
          {t("Map")}
        </a>
      )}
    </div>
  );
}

/**
 * A party's zone, played as a card the host hid: the team gets there by
 * scanning it, or typing the code printed on it. Where the zone was drawn
 * on a map means nothing in someone's garden, so no map and no distance.
 */
function StationCard({ view, zone, t, onStation, busy }: { view: play.View; zone: play.PlayZone; t: Translate; onStation?: (code: string) => void; busy: boolean }) {
  const [code, setCode] = useState("");
  const name = zone.hidden || !zone.name ? null : zone.name;
  if (view.here) {
    return (
      <div className="pv-zone is-here">
        <span className="pv-zone__pin" aria-hidden>
          ✓
        </span>
        <div className="pv-zone__body">
          <strong>{name ? t("You found {{place}}.", { place: name }) : t("You found the card.")}</strong>
        </div>
      </div>
    );
  }
  const send = (c: string) => {
    if (c.trim() && onStation) onStation(c.trim());
  };
  return (
    <div className="pv-zone pv-station">
      <div className="pv-station__head">
        <span className="pv-zone__pin" aria-hidden>
          ▦
        </span>
        <div className="pv-zone__body">
          <strong>{name ? t("Find the card: {{place}}.", { place: name }) : t("Find the next card.")}</strong>
          <span className="pv-zone__note">{t("Scan it, or type the code printed on it.")}</span>
        </div>
      </div>
      {onStation && (
        <form
          className="pv-station__entry"
          onSubmit={(e) => {
            e.preventDefault();
            send(code);
            setCode("");
          }}
        >
          <QrButton t={t} disabled={busy} onScan={send} />
          <input
            className="pv-input"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder={t("Code on the card")}
            aria-label={t("Code on the card")}
            autoCapitalize="characters"
            autoComplete="off"
            spellCheck={false}
          />
          <button type="submit" className="pv-btn pv-btn--primary" disabled={busy || !code.trim()}>
            {t("Go")}
          </button>
        </form>
      )}
    </div>
  );
}

// --- steps ------------------------------------------------------------------

/** What a step's blocks may use besides themselves. */
function blockContext(p: StepProps): BlockContext {
  return {
    position: p.position,
    t: p.t,
    placeNote: p.placeNote,
    teamName: p.teamName,
    now: p.now,
    items: p.view.items,
    arrival: p.view.input?.kind === "search",
    node: p.view.node,
    act: p.onAction,
    busy: !!p.busy,
    preview: !!p.preview,
    instant: !!p.instant,
    motion: p.motion,
    // Votes come from players' own phones: never in the simulator or a preview.
    live: (p.live ?? true) && !p.preview,
    crew: !!p.crew,
    photo: p.photo,
    bulletin: p.bulletin,
    roles: p.view.roles,
    claimRole: p.claimRole,
    rolesChosen: p.view.roles_chosen,
  };
}

function PuzzleStep(props: StepProps) {
  const { view, t, now, busy, onAction, feedback } = props;
  const node = view.node ?? "";
  // A scan-only puzzle falls back to typing the code when the camera fails.
  const [typing, setTyping] = useState(false);
  const live = props.live ?? true;

  const blockedFor = secondsUntil(view.blocked_until, now);
  const timeLeft = view.deadline ? secondsUntil(view.deadline, now) : Infinity;
  const timeUp = timeLeft <= 0;
  // Cheating works away from the place, so a tester can walk a route from a desk.
  const cheat = props.cheat;
  const needsPlace = !!view.zone && !view.here && !cheat;
  const locked = !!busy || blockedFor > 0 || needsPlace;
  const answer = (g: string) => onAction(cheat ? { kind: "cheat", node, password: cheat } : { kind: "answer", node, guess: g });

  // Live, the client calls Timeout itself when the countdown ends — the
  // engine re-checks the time, so an early or repeated call is harmless.
  const firedTimeout = useRef(false);
  useEffect(() => {
    if (live && timeUp && view.has_timeout && !firedTimeout.current) {
      firedTimeout.current = true;
      onAction({ kind: "timeout", node });
    }
  }, [live, timeUp, view.has_timeout, node, onAction]);

  const input: play.PlayInput = view.input ?? { kind: "text" };
  // Another role's to answer: the step shows, its answer does not.
  const mine = !input.not_yours;
  const tiles = mine && input.kind === "tiles" && input.image && input.rows && input.cols ? input : null;
  const search = mine && input.kind === "search";
  const choices = mine && input.kind === "choice" && input.fields?.length ? input.fields : null;
  const lock = mine && input.kind === "lock" && input.wheels && input.symbols?.length ? input : null;
  const slots = mine && input.kind === "items" && input.slots?.length ? input : null;
  const order = mine && input.kind === "order" && input.cards?.length ? input.cards : null;
  const questions = mine && input.kind === "questions";
  const tasks = mine && input.kind === "tasks" ? (view.blocks ?? []).filter((b) => b.kind === "task" && !b.locked) : null;
  const keys = mine && input.kind === "keys";
  const scan = mine && input.kind === "scan" && !typing && canScan();
  const placeholder =
    input.placeholder ||
    (input.parts ? t(input.ordered ? "{{n}} answers, in order, with commas" : "{{n}} answers, with commas", { n: input.parts }) : t("Your answer"));

  return (
    <>
      <Title view={view} />
      <ZoneCard view={view} t={t} note={props.zoneNote} busy={!!busy} onStation={(code) => onAction({ kind: "station", node, guess: code })} />
      <Blocks blocks={view.blocks} {...blockContext(props)} />

      {view.deadline && (
        <p className={"pv-timer" + (timeLeft < 60 ? " is-urgent" : "")}>
          <span aria-hidden>⏱</span> {timeUp ? t("Time is up.") : t("Time left: {{time}}", { time: formatSeconds(timeLeft) })}
        </p>
      )}

      {view.hints && view.hints.length > 0 && (
        <button type="button" className="pv-hints-peek" onClick={props.openHints}>
          {view.hints.length === 1 ? t("1 hint revealed") : t("{{n}} hints revealed", { n: view.hints.length })} →
        </button>
      )}

      {tiles && !timeUp && (
        <TileBoard
          key={node}
          image={tiles.image!}
          rows={tiles.rows!}
          cols={tiles.cols!}
          storageKey={node}
          disabled={locked}
          t={t}
          onSolved={answer}
        />
      )}

      {lock && !timeUp && (
        <LockBoard
          key={node}
          wheels={lock.wheels!}
          symbols={lock.symbols!}
          start={lock.start}
          disabled={locked}
          label={needsPlace ? t("Not there yet") : undefined}
          t={t}
          onTry={answer}
        />
      )}

      {order && !timeUp && (
        <OrderBoard
          key={node}
          cards={order}
          storageKey={node}
          disabled={locked}
          label={needsPlace ? t("Not there yet") : undefined}
          t={t}
          onTry={answer}
        />
      )}

      {slots && !timeUp && (
        <ItemSlotsBoard
          key={node}
          input={slots}
          items={view.items ?? []}
          disabled={locked}
          label={needsPlace ? t("Not there yet") : undefined}
          t={t}
          onTry={answer}
          onLeave={() => onAction({ kind: "leave", node })}
        />
      )}

      {choices && !timeUp && (
        <ChoiceForm
          key={node}
          fields={choices}
          disabled={locked}
          label={needsPlace ? t("Not there yet") : undefined}
          t={t}
          onAnswer={answer}
        />
      )}

      {search && !timeUp && (
        <div className={"pv-zone pv-search" + (input.at ? " is-here" : "")}>
          <span className="pv-zone__pin" aria-hidden>
            {input.at ? "⌖" : "?"}
          </span>
          <div className="pv-zone__body">
            <strong role="status">{input.at ? t("You are at {{place}}.", { place: input.at }) : t("You're not there yet.")}</strong>
            {/* A compass on the step already says how far. */}
            {!input.at && props.zoneNote && !view.blocks?.some((b) => b.kind === "compass") && <span className="pv-zone__note">{props.zoneNote}</span>}
          </div>
        </div>
      )}

      {view.can_skip && <SkipLink {...props} />}
      {cheat && (
        <button type="button" className="pv-link" disabled={locked} onClick={() => onAction({ kind: "cheat", node, password: cheat })}>
          {t("Solve without answering (cheat)")}
        </button>
      )}

      {view.together && view.together.of > 1 && <TogetherLine together={view.together} t={t} />}
      {keys && input.posts && input.posts.length > 0 && !input.post_roles && (
        <p className="pv-muted">{t("Turn your key at one of the posts: {{posts}}. One player at each.", { posts: input.posts.join(", ") })}</p>
      )}
      {keys && input.posts && input.post_roles && (
        <div className="pv-posts">
          <p className="pv-muted">{t("Each key is turned at its own post:")}</p>
          <ul>
            {input.posts.map((place, i) => {
              const role = input.post_roles?.[i];
              const own = input.my_posts?.[i] && role;
              return (
                <li key={i} className={own ? "is-mine" : ""}>
                  <span aria-hidden>{input.posts_held?.[i] ? "✓ " : "⌖ "}</span>
                  {role ? t("{{role}} at {{place}}", { role, place }) : t("Anyone at {{place}}", { place })}
                  {own && <strong> · {t("your post")}</strong>}
                  {input.posts_held?.[i] && <span> · {t("in place")}</span>}
                </li>
              );
            })}
          </ul>
          {!input.my_posts?.some(Boolean) && <p className="pv-muted">{t("No post is yours here: go with one of them and help.")}</p>}
        </div>
      )}

      <div className="pv-dock">
        <FeedbackLine feedback={feedback} />
        {!mine ? (
          <p className="pv-muted pv-not-yours">{t("Only the {{role}} can answer this. Find them and work it out together.", { role: input.by ?? "" })}</p>
        ) : keys && !timeUp ? (
          <button
            type="button"
            className="pv-btn pv-btn--primary pv-btn--wide"
            disabled={locked || needsPlace}
            onClick={() => onAction({ kind: "turn", node })}
          >
            {needsPlace ? t("Not there yet") : view.together?.mine ? t("Turn it again") : t("Turn your key")}
          </button>
        ) : search && !timeUp ? (
          <button
            type="button"
            className="pv-btn pv-btn--primary pv-btn--wide"
            disabled={locked}
            onClick={() => answer("")}
          >
            {blockedFor > 0 ? formatSeconds(blockedFor) : t("We're here")}
          </button>
        ) : scan && !timeUp ? (
          <>
            <QrButton
              t={t}
              disabled={locked}
              label={blockedFor > 0 ? formatSeconds(blockedFor) : needsPlace ? t("Not there yet") : input.button || t("Scan the code")}
              onScan={answer}
            />
            <button type="button" className="pv-link" onClick={() => setTyping(true)}>
              {t("Type the code instead")}
            </button>
          </>
        ) : tasks && !timeUp ? (
          <p className="pv-muted">{t("{{n}} of {{m}} tasks done. Each of you does your own, on your own phone.", { n: tasks.filter((b) => b.kind === "task" && b.picked).length, m: tasks.length })}</p>
        ) : questions && !timeUp ? (
          <ContinueButton view={view} t={t} disabled={locked} label={blockedFor > 0 ? formatSeconds(blockedFor) : needsPlace ? t("Continue when you are there") : undefined} onClick={() => answer("")} />
        ) : tiles || choices || lock || slots || order ? null : timeUp ? (
          view.has_timeout && !live ? (
            <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" disabled={busy} onClick={() => onAction({ kind: "timeout", node })}>
              {t("Continue")}
            </button>
          ) : null
        ) : (
          <AnswerForm
            input={input}
            t={t}
            placeholder={needsPlace ? t("Not there yet") : placeholder}
            disabled={needsPlace}
            locked={locked}
            button={input.button || t("Answer")}
            {...(blockedFor > 0 ? { countdown: formatSeconds(blockedFor) } : {})}
            onAnswer={answer}
            feedback={feedback ?? null}
          />
        )}
      </div>
    </>
  );
}

/**
 * How far the players are with a puzzle they do together: keys turned
 * in a two-key launch, or players agreed on a unanimous answer. A key's
 * count drops when its window closes, as the server says on recheck.
 */
function TogetherLine({ together, t }: { together: play.Together; t: Translate }) {
  const { done, of, mine } = together;
  const text =
    together.kind === "keys"
      ? t("{{done}} of {{of}} keys turned", { done, of }) + (mine ? ` · ${t("yours is")}` : "")
      : t("{{done}} of {{of}} players have given the answer", { done, of }) + (mine ? ` · ${t("you have")}` : "");
  return (
    <p className="pv-together" role="status">
      <span className="pv-together__dots" aria-hidden>
        {Array.from({ length: of }, (_, i) => (
          <span key={i} className={"pv-together__dot" + (i < done ? " is-on" : "")} />
        ))}
      </span>
      {text}
    </p>
  );
}

/**
 * The typed answer: one box, or one per part of a list answer, joined
 * with commas as the matchers split it. A right answer moves the team on
 * and remounts this step; a wrong one keeps the text so a typo can be
 * fixed rather than retyped.
 */
function AnswerForm(props: {
  input: play.PlayInput;
  t: Translate;
  placeholder: string;
  /** The button's accessible name; the button itself shows an arrow. */
  button: string;
  /** A lockout's time left, shown in the button in place of the arrow. */
  countdown?: string;
  disabled: boolean;
  locked: boolean;
  onAnswer: (guess: string) => void;
  feedback: Feedback | null;
}) {
  const { input, t, locked, onAnswer } = props;
  // After a wrong answer the text is selected, so typing replaces it and
  // a typo is still fixable — no clearing by hand on a phone keyboard.
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => {
    if (props.feedback?.tone !== "bad") return;
    const first = formRef.current?.querySelector("input");
    if (first && first.value) first.select();
  }, [props.feedback]);
  // A short list gets a box each; a long one stays one comma-separated line.
  const boxes = input.parts && input.parts >= 2 && input.parts <= MAX_PART_BOXES ? input.parts : 0;
  const [guess, setGuess] = useState("");
  const [parts, setParts] = useState<string[]>(() => Array(boxes).fill(""));
  const g = boxes ? parts.map((p) => p.trim()).filter(Boolean).join(", ") : guess.trim();
  const field = (value: string, set: (v: string) => void, label: string, placeholder: string) => (
    <input
      key={label}
      className="pv-input"
      aria-label={label}
      placeholder={placeholder}
      value={value}
      onChange={(e) => set(e.target.value)}
      inputMode={input.kind === "number" ? "decimal" : "text"}
      autoComplete="off"
      autoCapitalize="off"
      spellCheck={false}
      enterKeyHint="send"
      disabled={props.disabled}
    />
  );
  return (
    <form
      ref={formRef}
      className="pv-answer"
      onSubmit={(e) => {
        e.preventDefault();
        if (g && !locked) onAnswer(g);
      }}
    >
      {boxes ? (
        <div className="pv-answer__parts" role="group" aria-label={t("Your answer")}>
          {parts.map((p, i) =>
            field(p, (v) => setParts(parts.map((x, j) => (j === i ? v : x))), t("Answer {{n}}", { n: i + 1 }), props.disabled ? props.placeholder : input.ordered ? t("{{n}}.", { n: i + 1 }) : ""),
          )}
        </div>
      ) : (
        field(guess, setGuess, t("Your answer"), props.placeholder)
      )}
      <button
        className={"pv-btn pv-btn--primary pv-answer__send" + (props.countdown ? " is-counting" : "")}
        disabled={locked || !g}
        aria-label={props.countdown ? `${props.button} (${props.countdown})` : props.button}
        title={props.button}
      >
        {props.countdown ?? (
          <svg viewBox="0 0 24 24" width="24" height="24" aria-hidden="true">
            <path d="M5 12h13M12 5l7 7-7 7" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        )}
      </button>
    </form>
  );
}

/** The most parts a list answer gets separate boxes for. */
const MAX_PART_BOXES = 6;

function SkipLink({ view, t, busy, onAction }: StepProps) {
  const [confirm, setConfirm] = useState(false);
  const node = view.node ?? "";
  if (!confirm) {
    // A walk's "go on anyway" is a way on, in the look's own buttons; a
    // game's skip stays a quiet link, as giving up should.
    return (
      <button type="button" className={view.tour ? "pv-btn pv-skip" : "pv-link"} disabled={busy} onClick={() => setConfirm(true)}>
        {t("Skip this puzzle")}
      </button>
    );
  }
  return (
    <div className="pv-confirm" role="group">
      <span>{t("Skip this puzzle? You won't get its points.")}</span>
      <button type="button" className="pv-btn pv-btn--primary" disabled={busy} onClick={() => onAction({ kind: "skip", node })}>
        {t("Yes")}
      </button>
      <button type="button" className="pv-btn" onClick={() => setConfirm(false)}>
        {t("No")}
      </button>
    </div>
  );
}

function HintSheet(props: StepProps & { onClose: () => void }) {
  const { view, t, now, busy, onAction, onClose } = props;
  const [confirm, setConfirm] = useState(false);
  const hint = view.next_hint;
  const hintIn = hint ? secondsUntil(hint.available_at, now) : 0;
  const node = view.node ?? "";

  const reveal = () => {
    setConfirm(false);
    if (hint) onAction({ kind: "hint", node, index: hint.index });
  };

  return (
    <Sheet title={t("Hints")} t={t} onClose={onClose}>
        <div className="pv-sheet__body">
          {view.hints?.map((h, i) => (
            <div key={i} className="pv-hint">
              <div className="pv-hint__n">{t("Hint {{n}}", { n: i + 1 })}</div>
              <Markdown text={fillTeam(h.text, { teamName: props.teamName })} />
              <MediaList media={h.media} />
            </div>
          ))}
          {!view.hints?.length && !hint && <p className="pv-muted">{t("No hints for this one.")}</p>}
        </div>
        {hint &&
          (hintIn > 0 ? (
            <p className="pv-sheet__locked">{t("Next hint in {{time}}", { time: formatSeconds(hintIn) })}</p>
          ) : confirm ? (
            <div className="pv-confirm">
              <span>{t("This hint costs {{n}} points.", { n: hint.cost ?? 0 })}</span>
              <button type="button" className="pv-btn pv-btn--primary" disabled={busy} onClick={reveal}>
                {t("Yes")}
              </button>
              <button type="button" className="pv-btn" onClick={() => setConfirm(false)}>
                {t("No")}
              </button>
            </div>
          ) : (
            <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" disabled={busy} onClick={() => (hint.cost ? setConfirm(true) : reveal())}>
              {hint.cost ? t("Get a hint (−{{n}} points)", { n: hint.cost }) : t("Get a hint")}
            </button>
          ))}
    </Sheet>
  );
}

/** A bottom sheet over the step. Escape or a tap outside closes it. */
/** A panel sliding up over the step: the hints, and the app's inbox. */
export function Sheet({ title, t, onClose, children }: { title: string; t: Translate; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="pv-sheet-back" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="pv-sheet" role="dialog" aria-modal="true" aria-label={title}>
        <div className="pv-sheet__head">
          <h3>{title}</h3>
          <button type="button" className="pv-x" onClick={onClose} aria-label={t("Close")}>
            ×
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

function StoryStep(props: StepProps) {
  const { view, t, busy, onAction, zoneNote, feedback } = props;
  const node = view.node ?? "";
  // Away from the place, cheating continues anyway; there, it is a plain continue.
  const cheatPast = !!props.cheat && !!view.zone && !view.here;
  const needsPlace = !!view.zone && !view.here && !cheatPast;
  return (
    <>
      <Title view={view} />
      <ZoneCard view={view} t={t} note={zoneNote} busy={!!busy} onStation={(code) => onAction({ kind: "station", node, guess: code })} />
      <Blocks blocks={view.blocks} {...blockContext(props)} />
      <div className="pv-dock">
        <FeedbackLine feedback={feedback} />
        <ContinueButton
          view={view}
          t={t}
          disabled={!!busy || needsPlace}
          label={needsPlace ? t("Continue when you are there") : undefined}
          onClick={() => onAction(cheatPast ? { kind: "cheat", node, password: props.cheat ?? "" } : { kind: "continue", node })}
        />
      </div>
    </>
  );
}

/**
 * Continue past a step's quiz questions: held, and counting, until the
 * team has picked every one it can see. A teaser is not asked yet. Held
 * too while the players choose their roles.
 */
function ContinueButton({ view, t, disabled, label, onClick }: { view: play.View; t: Translate; disabled: boolean; label?: string | undefined; onClick: () => void }) {
  const asked = (view.blocks ?? []).filter((b) => (b.kind === "quiz" || b.kind === "tiebreak") && !b.locked);
  const answered = asked.filter((b) => "picked" in b && b.picked).length;
  const open = answered < asked.length;
  // A step where the players choose their roles waits for all of them.
  const choosing = view.roles_chosen === false && !view.roles?.some((r) => r.final) && (view.blocks ?? []).some((b) => b.kind === "roles" && b.choose && !b.locked);
  return (
    <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" disabled={disabled || open || choosing} onClick={onClick}>
      {label ?? (open ? t("{{answered}} of {{total}} answered", { answered, total: asked.length }) : choosing ? t("Waiting for everyone to choose") : t("Continue"))}
    </button>
  );
}

/**
 * Where the team splits up: what the groups are off to do, which this
 * phone goes with, and every group — so a player knows where the others
 * went. Continue sets the phone's group off down its path.
 */
function SplitStep({ view, t, busy, onAction, feedback }: StepProps) {
  const node = view.node ?? "";
  return (
    <>
      <Title view={view} />
      {view.text && <Markdown text={view.text} className="pv-text" />}
      {view.group && (
        <p className="pv-group-me" role="status">
          <span className="pv-group-me__label">{t("Your group")}</span>
          <span className="pv-group-me__name">{view.group}</span>
        </p>
      )}
      <Groups view={view} t={t} busy={!!busy} onAction={onAction} />
      <div className="pv-dock">
        <FeedbackLine feedback={feedback} />
        <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" disabled={!!busy} onClick={() => onAction({ kind: "continue", node })}>
          {t("Set off")}
        </button>
      </div>
    </>
  );
}

/**
 * A group waiting where its groups meet, for the others of its split to
 * arrive: then the team goes on together.
 */
function JoinStep({ view, t, busy, onAction }: StepProps) {
  // awaiting is left out at zero.
  const left = view.awaiting ?? 0;
  let text: string;
  if (view.groups?.length && view.groups.every((g) => g.arrived)) {
    text = t("Every group is here. The team goes on together.");
  } else if (left <= 0) {
    text = t("The groups meet here. The team goes on together once every group is here.");
  } else {
    text = left === 1 ? t("Waiting for 1 more group. The team goes on together once it is here.") : t("Waiting for {{n}} more groups. The team goes on together once they are here.", { n: left });
  }
  return (
    <>
      <Title view={view} />
      <p className="pv-waiting" role="status">
        {text}
      </p>
      <Groups view={view} t={t} busy={!!busy} onAction={onAction} />
    </>
  );
}

/**
 * Every group of a split team, where it is and whether it has arrived. A
 * phone in more than one group may switch to another of its own still
 * on its way, or to one that is anyone's: no one is with it, or they
 * are all away.
 */
function Groups({ view, t, busy, onAction }: { view: play.View; t: Translate; busy: boolean; onAction: (action: play.PlayAction) => void }) {
  const groups = view.groups ?? [];
  const arrived = groups.filter((g) => g.arrived).length;
  return (
    <section className="pv-groups" aria-label={t("Groups")}>
      <header className="pv-groups__head">
        <span>{t("Groups")}</span>
        {(arrived > 0 || view.type === "join") && <span>{t("{{n}} of {{m}} arrived", { n: arrived, m: groups.length })}</span>}
      </header>
      <ul className="pv-groups__list">
        {groups.map((g, i) => (
          <li key={g.port} className={"pv-groups__group" + (g.current ? " is-current" : "") + (g.arrived ? " is-arrived" : "")}>
            <span className="pv-groups__mark" aria-hidden="true">
              {g.arrived ? "✓" : i + 1}
            </span>
            <span className="pv-groups__body">
              <span className="pv-groups__name">
                {g.label}
                {g.mine ? <span className="pv-groups__tag">{t("you")}</span> : g.open && <span className="pv-groups__tag pv-groups__tag--open">{t("open to anyone")}</span>}
              </span>
              <span className="pv-groups__at">{g.arrived ? t("Arrived") : g.at}</span>
            </span>
            {(g.mine || g.open) && !g.arrived && !g.current && (
              <button
                type="button"
                className="pv-groups__walk"
                disabled={busy}
                aria-label={t("Walk with {{group}}", { group: g.label })}
                onClick={() => onAction({ kind: "walk", port: g.port })}
              >
                {t("Switch")}
              </button>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * The groups in one line above a split team's other steps: this view's
 * group first, then a tap on another of its own or an open one to walk
 * with that instead.
 */
function GroupStrip({ view, t, busy, onAction }: { view: play.View; t: Translate; busy: boolean; onAction: (action: play.PlayAction) => void }) {
  return (
    <ul className="pv-group-strip" aria-label={t("Groups")}>
      {(view.groups ?? []).map((g) => {
        const cls = "pv-group-strip__chip" + (g.current ? " is-current" : "") + (g.arrived ? " is-arrived" : "");
        const label = (
          <>
            {g.arrived && <span aria-hidden="true">✓</span>}
            {g.label}
          </>
        );
        return (
          <li key={g.port}>
            {(g.mine || g.open) && !g.arrived && !g.current ? (
              <button
                type="button"
                className={cls + " is-walkable"}
                disabled={busy}
                title={g.at}
                aria-label={t("Walk with {{group}}", { group: g.label })}
                onClick={() => onAction({ kind: "walk", port: g.port })}
              >
                {label}
              </button>
            ) : (
              <span className={cls} title={g.arrived ? t("Arrived") : g.at}>
                {label}
              </span>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function HubStep({ view, t, busy, onAction, feedback, teamName }: StepProps) {
  const node = view.node ?? "";
  const offer = view.offer ?? [];
  return (
    <>
      <Title view={view} />
      {view.text ? <Markdown className="pv-text" text={fillTeam(view.text, { teamName })} /> : <p className="pv-text">{t("Choose where to go next.")}</p>}
      <ul className="pv-offer">
        {offer.map((o, i) => (
          <li key={o.port}>
            <button
              type="button"
              className="pv-tile"
              style={{ "--i": i } as CSSProperties}
              disabled={busy || o.locked}
              onClick={() => onAction({ kind: "choose", node, port: o.port })}
            >
              {o.image && <img className="pv-tile__img" src={playApi.mediaURL(o.image.id)} alt="" loading="lazy" draggable={false} />}
              <span className="pv-tile__label">
                {o.label || o.port}
                {o.note && <span className="pv-tile__note">{o.note}</span>}
              </span>
              {o.visited && <span className="pv-tile__seen">{t("Visited")}</span>}
              <span className="pv-tile__go" aria-hidden>
                {o.locked ? "🔒" : o.visited ? "✓" : "→"}
              </span>
              {o.locked && <span className="pv-visually-hidden">{t("locked")}</span>}
            </button>
          </li>
        ))}
      </ul>
      {!view.can_finish && offer.every((o) => o.locked) && (
        <p className="pv-muted">{t("Every way on is taken right now. Wait a moment and try again.")}</p>
      )}
      <div className="pv-dock">
        <FeedbackLine feedback={feedback} />
        {view.can_finish && (
          <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" disabled={busy} onClick={() => onAction({ kind: "choose", node, port: "done" })}>
            {t("Move on")}
          </button>
        )}
      </div>
    </>
  );
}

/** Several starts, or one with a place: the team picks where to begin and walks there. */
function StartChoice({ view, t, busy, onAction, placeNote }: StepProps) {
  const starts = view.starts ?? [];
  const needsCode = !!view.start_requires_code;
  const [code, setCode] = useState("");
  const codeOk = !needsCode || code.trim().length > 0;
  // begin() carries the code only when one is required.
  const begin = (port: string) => onAction(needsCode ? { kind: "begin", node: "", port, guess: code.trim() } : { kind: "begin", node: "", port });

  // Pure code gate — a single start with no place. Keep it dead simple
  // for first-time players: a code box and one Start button.
  const only = starts.length === 1 ? starts[0] : undefined;
  const simpleGate = needsCode && !!only && !only.zone;

  const codeField = needsCode ? (
    <input
      className="pv-code-input"
      type="text"
      inputMode="text"
      autoCapitalize="characters"
      autoComplete="off"
      spellCheck={false}
      value={code}
      onChange={(e) => setCode(e.target.value)}
      placeholder={t("Start code")}
      aria-label={t("Start code")}
    />
  ) : null;

  if (simpleGate) {
    return (
      <>
        <h2 className="pv-title">{t("Enter the code to begin")}</h2>
        <p className="pv-text">{t("Your host has the code. Type it in to start — the clock starts when you do.")}</p>
        {codeField}
        <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" disabled={busy || !codeOk} onClick={() => only && begin(only.node)}>
          {t("Start the adventure")}
        </button>
      </>
    );
  }

  return (
    <>
      <h2 className="pv-title">{needsCode ? t("Enter the code to begin") : starts.length > 1 ? t("Where do you start?") : t("Head to the start")}</h2>
      <p className="pv-text">
        {needsCode ? t("Your host has the code. Type it in, then start — the clock starts when you do.") : starts.length > 1 ? t("Pick a start and go there. The adventure begins when you arrive.") : t("The adventure begins when you arrive.")}
      </p>
      {codeField}
      <ul className="pv-offer">
        {starts.map((o, i) => {
          const z = o.zone;
          const name = o.title || z?.name || t("Start");
          return (
            <li key={o.node} className={"pv-zone pv-begin" + (o.here ? " is-here" : "")} style={{ "--i": i } as CSSProperties}>
              <div className="pv-begin__head">
                <span className="pv-zone__pin" aria-hidden>
                  {o.here ? "✓" : "⌖"}
                </span>
                <div className="pv-zone__body">
                  <strong>{name}</strong>
                  {z && !z.hidden && !o.here && placeNote && <span className="pv-zone__note">{placeNote(z)}</span>}
                  {z && o.here && <span className="pv-zone__note">{t("You are here.")}</span>}
                </div>
                {z && !z.hidden && (
                  <a href={mapURL(z)} target="_blank" rel="noopener noreferrer">
                    {t("Map")}
                  </a>
                )}
              </div>
              <button type="button" className="pv-btn pv-btn--primary pv-btn--wide" disabled={busy || !o.here || !codeOk} onClick={() => begin(o.node)}>
                {o.here ? t("Start here") : t("Get there to start")}
              </button>
            </li>
          );
        })}
      </ul>
    </>
  );
}

/** A span of seconds as m:ss, h:mm:ss past an hour, or "2d 5h" past a day. */
function duration(sec: number): string {
  if (sec >= 86400) return `${Math.floor(sec / 86400)}d ${Math.floor((sec % 86400) / 3600)}h`;
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = String(sec % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

/**
 * The unmasking, once the team has finished: who held each secret role,
 * which of their goals they met, and whether the team named them.
 */
function Reveal({ reveal, t }: { reveal: play.RevealView[]; t: Translate }) {
  return (
    <section className="pv-reveal">
      {reveal
        .filter((r) => r.dealt)
        .map((r) => (
          <div key={r.role} className="pv-reveal__role">
            <h3 className="pv-reveal__who">{t("The {{role}} was {{name}}!", { role: r.name || r.role, name: r.holder || t("someone who left") })}</h3>
            {r.caught !== undefined && (
              <p className={"pv-reveal__verdict" + (r.caught ? " is-caught" : "")}>
                {r.caught ? t("Your team named them. Well spotted.") : t("They got away with it.")}
              </p>
            )}
            {r.goals && r.goals.length > 0 && (
              <ul className="pv-reveal__goals">
                {r.goals.map((g, i) => (
                  <li key={i} className={g.met ? "is-met" : ""}>
                    <span aria-hidden>{g.met ? "✓" : "✗"}</span> {g.text}
                    <span className="pv-visually-hidden">{g.met ? t("done") : t("foiled")}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ))}
    </section>
  );
}

function EndStep(props: StepProps) {
  const { view, t, outro, finish } = props;
  const failed = view.outcome === "fail";
  // A game that keeps its results secret shows them on the recap, once the host finishes it.
  const held = !!view.results_held;
  return (
    <div className={"pv-summary" + (failed ? " is-fail" : "")}>
      {/* A walk ends without a trophy. */}
      {!view.tour && (
        <div className="pv-summary__badge" aria-hidden>
          {failed ? "⚑" : "★"}
        </div>
      )}
      <h2 className="pv-summary__head">{failed ? t("The adventure is over.") : t("You made it!")}</h2>
      <Blocks blocks={view.blocks} {...blockContext(props)} />
      {view.quiz && <QuizResult quiz={view.quiz} t={t} />}
      {view.reveal?.some((r) => r.dealt) && <Reveal reveal={view.reveal} t={t} />}
      {outro && <Markdown className="pv-text" text={fillTeam(outro, { teamName: props.teamName })} />}
      {held && (
        <p className="pv-summary__held">
          {finish?.provisional === false ? t("The game is finished. See how you did!") : t("Your points and place are revealed when the game is finished.")}
        </p>
      )}
      {!held && !view.hide_score && <div className="pv-stats">
        <div className="pv-stat">
          <div className="pv-stat__v">{round(view.score)}</div>
          <div className="pv-stat__l">{t("Points")}</div>
        </div>
        <div className="pv-stat">
          <div className="pv-stat__v">{view.penalty_score > 0 ? `−${round(view.penalty_score)}` : "0"}</div>
          <div className="pv-stat__l">{t("Hints and penalties")}</div>
        </div>
        <div className="pv-stat pv-stat--total">
          <div className="pv-stat__v">{round(view.score - view.penalty_score)}</div>
          <div className="pv-stat__l">{t("Total")}</div>
        </div>
      </div>}
      {finish && (finish.rank || finish.seconds) ? (
        <div className="pv-stats">
          {finish.rank ? (
            <div className="pv-stat">
              <div className="pv-stat__v">
                {finish.rank}
                {finish.teams ? <span className="pv-stat__of"> / {finish.teams}</span> : null}
              </div>
              <div className="pv-stat__l">{finish.provisional ? t("Place so far") : t("Place")}</div>
            </div>
          ) : null}
          {finish.seconds ? (
            <div className="pv-stat">
              <div className="pv-stat__v">{duration(finish.seconds)}</div>
              <div className="pv-stat__l">{t("Time")}</div>
            </div>
          ) : null}
        </div>
      ) : null}
      {finish?.recap_url && (
        <div className="pv-summary__recap">
          <a className="pv-btn pv-btn--primary pv-btn--wide" href={finish.recap_url}>
            {held ? t("See your results") : t("See your team's recap")}
          </a>
          {finish.provisional && held && <p className="pv-summary__note">{t("It opens when the game is finished.")}</p>}
        </div>
      )}
      <Bag view={view} t={t} titled content={(bs) => <Blocks blocks={bs} {...blockContext(props)} />} />
      {!props.preview && <p className="pv-summary__note">{t("You can close this window now.")}</p>}
    </div>
  );
}

/** The item's first picture, if it has one: its face in the backpack. */
function itemPicture(it: play.ItemView) {
  const b = it.blocks?.find((b) => b.kind === "image" && !b.locked);
  return b && b.kind === "image" ? b : undefined;
}

/** An item's face in the backpack: its picture, else its icon, else its first letter. */
function ItemFace({ item }: { item: play.ItemView }) {
  const pic = itemPicture(item);
  return (
    <span className={"pv-bag__icon" + (pic ? " pv-bag__icon--pic" : "")} aria-hidden>
      {pic ? <img src={playApi.mediaURL(pic.media)} alt="" loading="lazy" decoding="async" /> : item.icon || [...(item.name || item.slug).trim()][0]?.toUpperCase()}
    </span>
  );
}

/**
 * What the team carries. With give, the player's own kit items can be
 * handed on: tapping Give shows a code for a teammate to scan from their
 * backpack, with receive. Nothing when the backpack is empty.
 */
function Bag({ view, t, give, receive, titled, content }: { view: play.View; t: Translate; titled?: boolean; content?: (blocks: play.PlayBlock[]) => ReactNode; give?: ((item: string) => Promise<play.ItemGift>) | undefined; receive?: ((code: string) => Promise<{ name?: string; item: string }>) | undefined }) {
  const items = view.items ?? [];
  const [gift, setGift] = useState<{ item: play.ItemView; gift: play.ItemGift } | null>(null);
  const [note, setNote] = useState<string | null>(null);
  // The item opened to look at, by slug: it follows the item as the view changes.
  const [opened, setOpened] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!gift) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [gift]);
  // Taken: the item is no longer this player's to give.
  const still = gift && items.find((i) => i.slug === gift.item.slug);
  useEffect(() => {
    if (gift && !still?.give) {
      setGift(null);
      setNote(still?.holder ? t("{{name}} has the {{item}} now.", { name: still.holder, item: gift.item.name || gift.item.slug }) : null);
    }
  }, [gift, still, t]);
  const refused = (err: unknown) => {
    const code = (err as { code?: string }).code;
    return code === "gift_expired" ? t("That code has run out. Ask for a new one.")
      : code === "gift_own" ? t("That is your own code. A teammate scans it.")
      : code === "gift_gone" ? t("That item is no longer theirs to give.")
      : code === "gift_unknown" ? t("That is not a code for an item.")
      : t("Something went wrong. Try again.");
  };

  if (gift) {
    const left = Math.max(0, Math.round((Date.parse(gift.gift.expires_at) - now) / 1000));
    return (
      <section className="pv-gift">
        <h3>{t("Give the {{item}}", { item: gift.item.name || gift.item.slug })}</h3>
        <img className="pv-gift__qr" src={gift.gift.qr} alt={t("A code for a teammate to scan")} />
        <p>{left > 0 ? t("A teammate scans this from their backpack. It works for {{time}}.", { time: formatSeconds(left) }) : t("The code has run out.")}</p>
        <button type="button" className="pv-btn" onClick={() => setGift(null)}>{t("Done")}</button>
      </section>
    );
  }
  const open = content && items.find((i) => i.slug === opened && i.blocks?.length);
  if (open) {
    return (
      <section className="pv-item">
        <button type="button" className="pv-back" onClick={() => setOpened(null)}>
          <span aria-hidden>←</span> {t("All items")}
        </button>
        <header className="pv-item__head">
          {/* Its picture follows in the body: the face only stands in for one. */}
          {!itemPicture(open) && <ItemFace item={open} />}
          <h3>{open.name || open.slug}</h3>
        </header>
        <div className="pv-item__body">{content(open.blocks ?? [])}</div>
      </section>
    );
  }
  return (
    <>
      {items.length > 0 && (
        <section className="pv-bag" aria-label={t("Items")}>
          {titled && <h3>{t("Items")}</h3>}
          <ul className="pv-bag__grid">
            {items.map((it) => (
              <li key={it.slug} className={"pv-bag__item" + (it.not_mine ? " is-away" : "")}>
                {content && it.blocks?.length ? (
                  <button type="button" className="pv-bag__open" onClick={() => setOpened(it.slug)} aria-label={t("Examine the {{item}}", { item: it.name || it.slug })}>
                    <ItemFace item={it} />
                    <span className="pv-bag__name">{it.name || it.slug}</span>
                    <span className="pv-bag__hint" aria-hidden>{t("Tap to examine")}</span>
                  </button>
                ) : (
                  <>
                    <ItemFace item={it} />
                    <span className="pv-bag__name">{it.name || it.slug}</span>
                  </>
                )}
                {it.qty > 1 && <span className="pv-bag__qty">×{it.qty}</span>}
                {it.holder
                  ? <span className="pv-bag__role">{it.not_mine ? t("with {{name}}", { name: it.holder }) : t("yours to use")}</span>
                  : it.role && <span className="pv-bag__role">{t("belongs to the {{role}}", { role: it.role })}</span>}
                {give && it.give && (
                  <button
                    type="button"
                    className="pv-btn pv-bag__give"
                    onClick={() => {
                      setNote(null);
                      give(it.slug).then((g) => setGift({ item: it, gift: g }), (err) => setNote(refused(err)));
                    }}
                  >
                    {t("Give")}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
      {receive && items.some((it) => it.role || it.holder) && (
        <QrButton
          t={t}
          label={t("Scan to receive an item")}
          onScan={(code) => {
            setNote(null);
            receive(code).then((r) => setNote(t("You have the {{item}} now.", { item: r.name || r.item })), (err) => setNote(refused(err)));
          }}
        />
      )}
      {note && <p className="pv-bag__note" role="status">{note}</p>}
    </>
  );
}

/**
 * The event-start moment: the adventure's title over a bar that fills,
 * then gone. Shown once, when the game begins; skipped entirely under
 * reduced motion, where a frozen ceremony would only be in the way.
 */
export function StartCeremony({ title, brand, onDone }: { title: string; brand?: play.Brand | null; onDone: () => void }) {
  const style = useMemo(() => brandVars(brand) as CSSProperties, [brand]);
  // Once per mount, whatever the parent re-renders: a fresh onDone each
  // render must not keep pushing the end back.
  const done = useRef(onDone);
  done.current = onDone;
  useEffect(() => {
    const id = setTimeout(() => done.current(), prefersReducedMotion() ? 0 : 1300);
    return () => clearTimeout(id);
  }, []);
  return (
    <div className="pv-start" data-theme="light" style={style} aria-hidden>
      <h1>{title}</h1>
      <div className="pv-start__bar">
        <i />
      </div>
    </div>
  );
}

const round = (n: number) => Math.round(n * 10) / 10;
