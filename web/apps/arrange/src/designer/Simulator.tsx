import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, play } from "@zonventure/api";
import { metresBetween, nodeBySlug, portLabel, zoneContains, type Envelope, type Graph } from "@zonventure/graph";
import { PlayView, StartCeremony, feedbackForRefusal, feedbackForResult, formatSeconds, type Feedback, type Motion } from "@zonventure/play-ui";
import "@zonventure/play-ui/play-ui.css";
import { I18n } from "@zonventure/i18n";
import playEn from "../../../play/src/locales/en.json";
import playSv from "../../../play/src/locales/sv.json";
import playEt from "../../../play/src/locales/et.json";
import phoneSv from "../locales/phone/sv.json";
import phoneEt from "../locales/phone/et.json";
import { useT } from "../i18n.tsx";
import { SpoofMap, type OtherPin, type Point } from "./SpoofMap.tsx";
import { DevicePanel } from "./DevicePanel.tsx";
import { VISIBLE } from "./dz/types.tsx";
import { locationError, zoneName } from "./zones.ts";
import { Clapperboard, LocateFixed } from "lucide-react";
import { Button, ICON } from "./dz/ui.tsx";
import { DEMO_ANSWER_EVENT, REPLAY_EVENT } from "./blocks/Animation.tsx";

/** internal/graph/session.StartPlaceSlug: the start screen's zone for a required starting point. */
const START_PLACE = "_start";

/**
 * Preview and test-play, in one.
 *
 * Both show the draft exactly as a team would get it: the screen is
 * @zonventure/play-ui's PlayView, the component the player app renders,
 * and every action runs through the server's simulator, which applies
 * the live engine's rules (internal/graph/session) to the unsaved graph.
 * A right answer here is a right answer in the event; a hint that opens
 * after five minutes here opens after five minutes there.
 *
 * Two modes:
 *
 *   - "step" previews the selected step on its own, as a team arriving
 *     with no history would see it. It follows the selection and every
 *     edit, so the author sees a change as they type it.
 *   - "run" plays the whole adventure from Start. It keeps going across
 *     edits — each action is run against the latest draft — so an author
 *     can fix a step mid-run and carry on.
 *
 * Two controls exist only here: moving the clock, and placing the team
 * in a zone. They are how an author tries a hint delay, a time limit or
 * a zone gate without waiting or walking.
 *
 * With more than one phone the team is a crew: each phone is a player
 * of its own, with its roles and its place, and acts as itself. The
 * chosen phone is the big one, and every phone shows small underneath,
 * each in its own place, so a screen that differs — a role's clue, a
 * part of a split secret, a mission picked — is seen at a glance.
 */

export type SimMode = "step" | "run";

interface Props {
  slug: string;
  graph: Graph;
  envelope: Envelope;
  selected: string | null;
  mode: SimMode;
  /** Without it the mode is fixed and its toggle hidden. */
  onMode?: (m: SimMode) => void;
  /** Bumped by the parent to start a run over. */
  restart: number;
  /** Reports where the simulated team is, for the canvas. */
  onPosition: (slug: string | null) => void;
  /** The adventure's palette, as currently edited. */
  brand: play.Brand;
  /** Shown in the start ceremony. */
  title: string;
  /** Only the phone, small, without the controls beside it: a preview of the look. */
  bare?: boolean;
  /** The language the team plays in: the toolbar's, so the phone shows a translation. */
  lang?: string;
}

interface LogLine {
  id: number;
  text: string;
  tone?: "warn";
}

/**
 * Screens to preview on, in CSS pixels — the size the browser lays the
 * page out at, which is what decides whether a prompt wraps or a button
 * fits. The player is used on phones; the tablet is for a team sharing
 * one screen.
 */
export const DEVICES = [
  { id: "iphone-se", label: "iPhone SE", w: 375, h: 667 },
  { id: "iphone", label: "iPhone 16", w: 393, h: 852 },
  { id: "iphone-max", label: "iPhone 16 Pro Max", w: 440, h: 956 },
  { id: "pixel", label: "Pixel 9", w: 412, h: 923 },
  { id: "galaxy", label: "Galaxy S24", w: 360, h: 780 },
  { id: "ipad-mini", label: "iPad mini", w: 744, h: 1133 },
] as const;
type DeviceId = (typeof DEVICES)[number]["id"];

/** The most phones a crew has (admin maxCrew), and how small the others show. */
const MAX_PHONES = 6;
const MINI = 0.2;

/** What a phone's screen is, to tell when it changed since it was looked at. */
const signature = (v: play.View) => JSON.stringify([v.node, v.blocks, v.finished]);

/** 0 is "fit": as large as the window allows, never above actual size. */
const ZOOMS = [0, 1, 0.75, 0.5] as const;
const BEZEL = 8;

function stored<T>(key: string, ok: (v: unknown) => v is T, fallback: T): T {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(key) ?? "null");
    return ok(v) ? v : fallback;
  } catch {
    return fallback;
  }
}
function keep(key: string, v: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(v));
  } catch {
    /* private mode: remembered for this visit only */
  }
}
const isDevice = (v: unknown): v is DeviceId => DEVICES.some((d) => d.id === v);
const isZoom = (v: unknown): v is number => (ZOOMS as readonly unknown[]).includes(v);

/** Tracks the window height, which "fit" scales the device to. */
function useWindowHeight(): number {
  const [h, setH] = useState(() => window.innerHeight);
  useEffect(() => {
    const on = () => setH(window.innerHeight);
    window.addEventListener("resize", on);
    return () => window.removeEventListener("resize", on);
  }, []);
  return h;
}

/**
 * The player app's own catalogs, so the phone reads as a team's would, with
 * the simulator's few words of its own on the phone (locales/phone).
 */
const withSim = (play: Record<string, string>, sim: Record<string, string>) => ({ ...play, ...sim });
const PHONE_CATALOGS = { en: playEn as Record<string, string>, sv: withSim(playSv, phoneSv), et: withSim(playEt, phoneEt) };

export function Simulator({ slug, graph, envelope, selected, mode, onMode, restart, onPosition, brand, title, bare, lang }: Props) {
  const t = useT();

  const [team, setTeam] = useState<play.SimTeam | null>(null);
  const [view, setView] = useState<play.View | null>(null);
  // The phone speaks the adventure's language, as the player app does,
  // not the author's: falling back to theirs where the app has no words.
  const phoneLang = [view?.lang, graph.language, t.current()].find((l) => !!l && l in PHONE_CATALOGS) ?? "en";
  const tr = useMemo(() => {
    const i18n = new I18n(PHONE_CATALOGS, phoneLang);
    // A walk's words, as its phones will say them.
    if (graph.tour) i18n.setVariant("tour");
    return i18n.t;
  }, [phoneLang, graph.tour]);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [busy, setBusy] = useState(false);
  // The simulated phone, flat, still and facing north in a quiet room
  // until the author turns it, shakes it or makes some noise.
  const [motion, setMotion] = useState<Motion>({ heading: 0, beta: 0, gamma: 0, loudness: 0, shake: 0 });
  const [zone, setZone] = useState("");
  const [log, setLog] = useState<LogLine[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);
  const [ceremony, setCeremony] = useState(false);
  const [deviceId, setDeviceId] = useState<DeviceId>(() => stored("zonventure.preview.device", isDevice, "iphone"));
  const [zoom, setZoom] = useState<number>(() => stored("zonventure.preview.zoom", isZoom, 0));
  const winH = useWindowHeight();
  const device = DEVICES.find((d) => d.id === deviceId) ?? DEVICES[1];
  const frameW = device.w + 2 * BEZEL;
  const frameH = device.h + 2 * BEZEL;
  // Room left under the designer's toolbar, less the window's title bar,
  // padding and a margin: about 180 px.
  const scale = bare ? Math.min(0.6, Math.max(0.3, (winH - 260) / frameH)) : zoom || Math.min(1, Math.max(0.3, (winH - 180) / frameH));

  // The latest draft, read at send time so a run in progress uses the
  // graph as it is now rather than as it was when the run started.
  const envRef = useRef(envelope);
  envRef.current = envelope;
  // Where the author last put the team — a zone or a point on the map —
  // read by the preview effect so re-placing on an edit keeps it there.
  const whereRef = useRef<play.SimAction | null>(null);
  const logId = useRef(0);
  // Read when a team is placed or started, so a fresh one plays in it too.
  const langRef = useRef(lang);
  langRef.current = lang;

  // A crew: how many phones (1 is the team as a whole), who the author
  // gave each secret role (absent: dealt at random), the crew the server
  // hands back, each phone's screen, and what each showed when last seen.
  const [phoneCount, setPhoneCount] = useState(1);
  const [secretPick, setSecretPick] = useState<Record<string, number>>({});
  const [crew, setCrew] = useState<play.SimCrew | null>(null);
  const crewRef = useRef<play.SimCrew | null>(null);
  const [phones, setPhones] = useState<play.SimPhone[]>([]);
  const [seen, setSeen] = useState<Record<number, string>>({});
  // A new team gets a new crew: the same phones where they stood, no picks.
  const freshCrew = (): play.SimCrew | null => {
    if (bare || phoneCount < 2) return null;
    const old = crewRef.current;
    const players = Array.from({ length: phoneCount }, (_, i) => {
      const at = old?.players[i]?.position;
      return { slot: i + 1, name: t.t("Player {{n}}", { n: i + 1 }), ...(at ? { position: at } : {}) };
    });
    const secrets = Object.fromEntries(Object.entries(secretPick).filter(([, slot]) => slot <= phoneCount));
    return { players, as: old?.as && old.as <= phoneCount ? old.as : 1, ...(Object.keys(secrets).length ? { secrets } : {}) };
  };
  const freshRef = useRef(freshCrew);
  freshRef.current = freshCrew;
  const crewKey = `${phoneCount}:${JSON.stringify(secretPick)}`;

  const say = useCallback((text: string, tone?: "warn") => {
    setLog((l) => [{ id: ++logId.current, text, ...(tone ? { tone } : {}) }, ...l].slice(0, 30));
  }, []);

  const titleOf = useCallback((s: string) => nodeBySlug(graph, s)?.title || s, [graph]);

  const run = useCallback(
    async (action: play.SimAction, from: play.SimTeam | null, signal?: AbortSignal) => {
      setBusy(true);
      setError(null);
      try {
        const fresh = action.kind === "start" || action.kind === "place";
        const r = await play.simulate(slug, envRef.current, from, action, signal, fresh ? freshRef.current() : crewRef.current);
        if (r.team) setTeam(r.team);
        setView(r.result.view);
        crewRef.current = r.crew ?? null;
        setCrew(r.crew ?? null);
        setPhones(r.phones ?? []);
        if (fresh) setSeen(Object.fromEntries((r.phones ?? []).map((p) => [p.slot, signature(p.view)])));
        onPosition(r.result.view.node ?? null);
        return r;
      } catch (e) {
        if (signal?.aborted) return null;
        setError(e instanceof ApiError ? e.message : t.t("Could not reach the server. Check your connection."));
        return null;
      } finally {
        setBusy(false);
      }
    },
    [slug, onPosition, t],
  );

  // Step mode: place a fresh team on the selected step, again on every
  // edit. Debounced, so typing a prompt does not fire a request per key.
  const placeable = mode === "step" && selected && VISIBLE.has(nodeBySlug(graph, selected)?.type ?? "");
  useEffect(() => {
    if (mode !== "step") return;
    setFeedback(null);
    if (!placeable || !selected) {
      setView(null);
      setTeam(null);
      onPosition(null);
      return;
    }
    const ac = new AbortController();
    const id = setTimeout(() => {
      void run({ kind: "place", node: selected }, null, ac.signal)
        .then((r) => (r?.team && langRef.current && langRef.current !== graph.language ? run({ kind: "language", lang: langRef.current }, r.team, ac.signal) : r))
        .then((r) => {
          // Keep the team where the author put it, so previewing a
          // zone-gated step does not reset to "not there" on every edit.
          const where = whereRef.current;
          if (r?.team && where) void run(where, r.team, ac.signal);
        });
    }, 300);
    return () => {
      clearTimeout(id);
      ac.abort();
    };
    // envelope is a dependency on purpose: every edit re-places the
    // team, which is what makes this a live preview.
  }, [mode, placeable, selected, envelope, run, onPosition, crewKey]);

  // Run mode: start from the top when entered or restarted.
  useEffect(() => {
    if (mode !== "run") return;
    setFeedback(null);
    setLog([]);
    setZone("");
    whereRef.current = null;
    const ac = new AbortController();
    void run({ kind: "start" }, null, ac.signal).then((r) => {
      if (r?.team && !r.refusal && langRef.current && langRef.current !== graph.language) void run({ kind: "language", lang: langRef.current }, r.team, ac.signal);
      if (r?.refusal) say(r.refusal.message, "warn");
      else if (r) {
        setCeremony(true);
        say(
          r.result.view.node
            ? t.t("Started at {{step}}.", { step: titleOf(r.result.view.node) })
            : t.t("Started. The team picks where to begin."),
        );
      }
    });
    return () => ac.abort();
    // Deliberately not keyed on the graph: a run carries on across edits.
    // A different crew is a different team, so it starts over.
  }, [mode, restart, nonce, run, crewKey]);

  // The toolbar's language changed: the team in play switches, as a player can.
  useEffect(() => {
    if (team && lang) void run({ kind: "language", lang }, team);
  }, [lang]);

  // Play again (the block animation editor): remount the step so it enters anew.
  const [replay, setReplay] = useState(0);
  useEffect(() => {
    const again = () => setReplay((n) => n + 1);
    window.addEventListener(REPLAY_EVENT, again);
    return () => window.removeEventListener(REPLAY_EVENT, again);
  }, []);
  // Try it (the look's celebration controls): a right or a wrong answer,
  // acted out with the player's own words, nothing sent to the server.
  useEffect(() => {
    const act = (e: Event) => {
      const right = (e as CustomEvent<boolean>).detail;
      setFeedback(
        right
          ? { tone: "good", text: tr("Correct!"), burst: { kind: "celebrate", head: tr("Correct!") } }
          : { tone: "bad", text: tr("That's not it. Try again."), burst: { kind: "rebuff", head: tr("Not quite") } },
      );
    };
    window.addEventListener(DEMO_ANSWER_EVENT, act);
    return () => window.removeEventListener(DEMO_ANSWER_EVENT, act);
  }, [tr]);

  // Clear the canvas marker when the simulator goes away.
  useEffect(() => () => onPosition(null), [onPosition]);

  const asSlot = crew?.as ?? 1;
  const nameOf = (slot: number) => crew?.players.find((p) => p.slot === slot)?.name ?? "";
  // A crew's line says which phone did it.
  const sayAs = (text: string, tone?: "warn") => say(crewRef.current ? `${nameOf(crewRef.current.as ?? 1)}: ${text}` : text, tone);
  const pick = (slot: number) => {
    if (!crewRef.current) return;
    crewRef.current = { ...crewRef.current, as: slot };
    setCrew(crewRef.current);
    setFeedback(null);
    const mine = phones.find((p) => p.slot === slot);
    if (mine) setView(mine.view);
  };
  // The phone in view has been looked at.
  useEffect(() => {
    const mine = phones.find((p) => p.slot === asSlot);
    if (mine) setSeen((s) => ({ ...s, [asSlot]: signature(mine.view) }));
  }, [phones, asSlot]);

  const act = async (a: play.PlayAction | Extract<play.SimAction, { kind: "force" }>) => {
    const before = view?.node;
    const r = await run(a, team);
    if (!r) return;
    if (r.refusal) {
      if (r.refusal.code === "graph") {
        setFeedback(null);
        sayAs(t.t("The draft stopped the team: {{why}}", { why: r.refusal.message }), "warn");
      } else {
        setFeedback(feedbackForRefusal(r.refusal.code, tr));
      }
      return;
    }
    // A pass celebrates like the right answer it stands in for.
    setFeedback(feedbackForResult(a.kind === "force" ? "answer" : a.kind, r.result, tr, before));
    const after = r.result.view.node;
    const left = before ? nodeBySlug(graph, before) : undefined;
    const port = r.result.port && left ? portLabel(left, r.result.port, graph.roles) : r.result.port;
    if (a.kind === "answer") sayAs(t.t("Answered “{{guess}}”.", { guess: a.guess }));
    if (a.kind === "force") sayAs(t.t("Passed without answering."));
    if (a.kind === "seal") sayAs(t.t("Picked at the mission."));
    if (a.kind === "task") sayAs(r.result.accepted ? t.t("Did their task.") : t.t("Tried their task: not right."));
    if (a.kind === "sabotage") sayAs(t.t("Pressed the sabotage."));
    if (r.result.port) {
      const via = (r.passed ?? []).filter((s) => s !== after).map(titleOf);
      say(
        via.length
          ? t.t("Left by “{{port}}”, through {{via}}, to {{step}}.", { port: port ?? "", via: via.join(" → "), step: titleOf(after ?? "") })
          : t.t("Left by “{{port}}” to {{step}}.", { port: port ?? "", step: titleOf(after ?? "") }),
      );
    } else if (before && after && before !== after) {
      say(t.t("Moved to {{step}}.", { step: titleOf(after) }));
    }
  };

  const wait = (seconds: number) => {
    if (!team) return;
    setFeedback(null);
    void run({ kind: "wait", seconds }, team).then((r) => {
      if (!r) return;
      say(t.t("Waited {{time}}.", { time: formatSeconds(seconds) }));
      if (r.result.time_up) say(t.t("Time is up: the team was moved to {{step}}.", { step: titleOf(r.result.view.node ?? "") }), "warn");
    });
  };

  // The adventure's required starting point, while the team waits at it:
  // a place the team can be put, like the draft's own zones.
  const startPlace = view?.starts?.find((o) => o.zone?.slug === START_PLACE)?.zone;
  const places = startPlace ? [...(graph.zones ?? []), startPlace] : (graph.zones ?? []);
  const placeName = (z: { slug: string; name?: string }) =>
    z.slug === START_PLACE ? (z.name ? t.t("Starting point: {{name}}", { name: z.name }) : t.t("Starting point")) : z.name || z.slug;

  const moveTo = (p: Point) => {
    const zones = places;
    const inside = zones.find((z) => zoneContains(z, p.lat, p.lng));
    setZone(inside?.slug ?? "");
    const where: play.SimAction = { kind: "move", lat: p.lat, lng: p.lng };
    whereRef.current = where;
    if (!team) return;
    setFeedback(null);
    // Say where that is, so a click just outside a small zone reads as
    // "outside", not as the simulator ignoring it.
    const need = view?.zone ?? view?.starts?.find((o) => o.zone)?.zone;
    let text: string;
    if (inside) text = t.t("Moved the team into {{zone}}.", { zone: placeName(inside) });
    else if (need) text = t.t("Moved the team to {{m}} m from {{zone}} — outside it.", { m: Math.round(metresBetween(p.lat, p.lng, need.lat, need.lng)), zone: placeName(need) });
    else text = t.t("Moved the team outside every zone.");
    void run(where, team).then((r) => {
      if (r) sayAs(text);
    });
  };

  // Test on site: the simulated team goes where the author's phone goes.
  const [follow, setFollow] = useState(false);
  const [followNote, setFollowNote] = useState<{ text: string; warn?: boolean } | null>(null);
  const moveRef = useRef(moveTo);
  moveRef.current = moveTo;
  useEffect(() => {
    if (!follow) return;
    if (typeof navigator === "undefined" || !navigator.geolocation) {
      setFollow(false);
      setFollowNote({ text: t.t("This browser can't tell where you are."), warn: true });
      return;
    }
    let last: Point | null = null;
    const id = navigator.geolocation.watchPosition(
      (p) => {
        const here = { lat: p.coords.latitude, lng: p.coords.longitude };
        setFollowNote({ text: t.t("Following you, to within about {{m}} m.", { m: Math.max(1, Math.round(p.coords.accuracy)) }) });
        // A phone standing still still reports small jumps; only a real step moves the team.
        if (last && metresBetween(last.lat, last.lng, here.lat, here.lng) < 5) return;
        last = here;
        moveRef.current(here);
      },
      (e) => {
        setFollow(false);
        setFollowNote({ text: locationError(e, t), warn: true });
      },
      { enableHighAccuracy: true, maximumAge: 5000 },
    );
    return () => navigator.geolocation.clearWatch(id);
  }, [follow, t]);

  const move = (z: string) => {
    // The starting point is not one of the draft's zones: stand at its centre.
    if (z === START_PLACE && startPlace) return moveTo({ lat: startPlace.lat, lng: startPlace.lng });
    setZone(z);
    whereRef.current = z ? { kind: "move", zone: z } : null;
    if (!team) return;
    setFeedback(null);
    void run(z ? { kind: "move", zone: z } : { kind: "move" }, team).then((r) => {
      if (r) sayAs(z ? t.t("Now standing in {{zone}}.", { zone: zoneName(graph, z) }) : t.t("Now nowhere in particular."));
    });
  };

  const elapsed = team?.now && typeof team["started_at"] === "string"
    ? (Date.parse(team.now) - Date.parse(team["started_at"] as string)) / 1000
    : 0;

  const selectedNode = selected ? nodeBySlug(graph, selected) : undefined;
  // A crew's map and distances are the chosen phone's.
  const pos = crew ? crew.players.find((p) => p.slot === asSlot)?.position : (team?.["position"] as Point | undefined);
  const insideOf = (p: Point | undefined) => (p ? places.find((z) => zoneContains(z, p.lat, p.lng)) : undefined);
  const others: OtherPin[] = (crew?.players ?? []).flatMap((p) => (p.slot !== asSlot && p.position ? [{ key: p.slot, label: p.name ?? String(p.slot), point: p.position }] : []));
  const secretRoles = (graph.roles ?? []).filter((r) => r.secret);
  const roleName = (slug: string) => graph.roles?.find((r) => r.slug === slug)?.name || slug;
  // "About N m away", as the player app says it — the same line the
  // phone shows a real team.
  const placeNote = (z: play.PlayZone) => {
    if (!pos) return tr("Put the team on the map to see how far it is.");
    const m = metresBetween(pos.lat, pos.lng, z.lat, z.lng);
    return m < 1000 ? tr("About {{m}} m away", { m: Math.max(1, Math.round(m)) }) : tr("About {{km}} km away", { km: Math.round(m / 100) / 10 });
  };
  const focus = view?.zone && !view.here ? view.zone : view?.starts?.find((o) => o.zone && !o.here)?.zone;
  // Whether the step shows a block of these kinds, which reads the simulated device.
  const sensed = (...kinds: play.PlayBlock["kind"][]) => !!view?.blocks?.some((b) => !b.locked && kinds.includes(b.kind));
  // The puzzle the team is on, for passing it without the answer.
  const current = view?.type === "puzzle" && view.node && !view.finished ? nodeBySlug(graph, view.node) : undefined;

  // A phone's roles, its secret one marked; where it stands; a mission picked.
  const phoneStatus = (p: play.SimPhone) => {
    const player = crew?.players.find((x) => x.slot === p.slot);
    const roles = (p.roles ?? []).map((r) => (secretRoles.some((s) => s.slug === r) ? `🕵 ${roleName(r)}` : roleName(r)));
    const at = insideOf(player?.position);
    const sealed = p.view.blocks?.some((b) => b.kind === "mission" && b.sealed);
    return [...roles, at ? `📍 ${placeName(at)}` : t.t("nowhere"), ...(sealed ? ["✓"] : [])].join(" · ");
  };
  const phoneLabel = (slot: number) => {
    const p = phones.find((x) => x.slot === slot);
    return p ? `${nameOf(slot)} · ${phoneStatus(p)}` : nameOf(slot);
  };

  return (
    <div className={"sim" + (bare ? " sim--bare" : "")}>
      {!bare && (
        <div className="sim__side">
          {onMode && (
            <div className="seg sim__mode" role="group" aria-label={t.t("Preview mode")}>
              <button type="button" className={mode === "step" ? "is-on" : ""} onClick={() => onMode("step")}>
                {t.t("This step")}
              </button>
              <button type="button" className={mode === "run" ? "is-on" : ""} onClick={() => onMode("run")}>
                {t.t("Play from start")}
              </button>
            </div>
          )}

          <div className="sim__device">
            <label className="field">
              <span className="field__label">{t.t("Device")}</span>
              <select
                className="field__input"
                value={device.id}
                onChange={(e) => {
                  setDeviceId(e.target.value as DeviceId);
                  keep("zonventure.preview.device", e.target.value);
                }}
              >
                {DEVICES.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.label} · {d.w}×{d.h}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span className="field__label">{t.t("Zoom")}</span>
              <select
                className="field__input"
                value={zoom}
                onChange={(e) => {
                  const z = Number(e.target.value);
                  setZoom(z);
                  keep("zonventure.preview.zoom", z);
                }}
              >
                {ZOOMS.map((z) => (
                  <option key={z} value={z}>
                    {z === 0 ? t.t("Fit ({{pct}} %)", { pct: Math.round(scale * 100) }) : `${z * 100} %`}
                  </option>
                ))}
              </select>
            </label>
          </div>

          <div className="sim__crew">
            <label className="field">
              <span className="field__label">{t.t("Phones")}</span>
              <select className="field__input" value={phoneCount} disabled={busy} onChange={(e) => setPhoneCount(Number(e.target.value))}>
                {Array.from({ length: MAX_PHONES }, (_, i) => i + 1)
                  // The whole team, or a crew the adventure takes.
                  .filter((n) => n === 1 || n === phoneCount || ((!graph.min_players || n >= graph.min_players) && (!graph.max_players || n <= graph.max_players)))
                  .map((n) => (
                    <option key={n} value={n}>
                      {n === 1 ? t.t("1 · the whole team") : t.t("{{n}} players", { n })}
                    </option>
                  ))}
              </select>
            </label>
            {phoneCount > 1 &&
              secretRoles.map((r) => (
                <label key={r.slug} className="field">
                  <span className="field__label">{t.t("{{role}} is", { role: r.name || r.slug })}</span>
                  <select
                    className="field__input"
                    value={secretPick[r.slug] ?? "random"}
                    disabled={busy}
                    onChange={(e) =>
                      setSecretPick((sp) => {
                        const next = { ...sp };
                        if (e.target.value === "random") delete next[r.slug];
                        else next[r.slug] = Number(e.target.value);
                        return next;
                      })
                    }
                  >
                    <option value="random">{phoneCount >= 3 ? t.t("Dealt at random") : t.t("No one: too few players")}</option>
                    {Array.from({ length: phoneCount }, (_, i) => i + 1).map((n) => (
                      <option key={n} value={n}>
                        {t.t("Player {{n}}", { n })}
                      </option>
                    ))}
                    <option value={0}>{t.t("No one")}</option>
                  </select>
                </label>
              ))}
          </div>

          <div className="sim__controls">
            <span className="sim__clock" title={t.t("Time since the team started")}>⏱ {formatSeconds(elapsed)}</span>
            {[60, 300, 1800].map((s) => (
              <Button key={s} type="button" size="sm" disabled={!team || busy} onClick={() => wait(s)}>
                +{s / 60} {t.t("min")}
              </Button>
            ))}
            <Button
              type="button"
              size="sm"
              disabled={!view}
              onClick={() => setReplay((n) => n + 1)}
              title={t.t("Show this step again, animations and all")}
           >
              <Clapperboard size={ICON.sm} aria-hidden="true" />
              {t.t("Show again")}
            </Button>
            {mode === "run" && (
              <Button size="sm" disabled={busy} onClick={() => setNonce((n) => n + 1)}
                aria-label={t.t("Start over")} title={t.t("Start over")}>
                ↺
              </Button>
            )}
          </div>

          <label className="field sim__where">
            <span className="field__label">{crew ? t.t("{{name}} is at", { name: nameOf(asSlot) }) : t.t("Team is at")}</span>
            <select className="field__input" value={crew ? (insideOf(pos)?.slug ?? "") : zone} disabled={!team || busy} onChange={(e) => move(e.target.value)}>
              <option value="">{t.t("Nowhere in particular")}</option>
              {places.map((z) => (
                <option key={z.slug} value={z.slug}>
                  {placeName(z)}
                </option>
              ))}
            </select>
          </label>

          <div className="sim__follow">
            <Button size="sm" className={follow ? "is-on" : ""} aria-pressed={follow} disabled={!team} onClick={() => setFollow((f) => !f)}>
              <LocateFixed size={ICON.sm} aria-hidden="true" />
              {follow ? t.t("Stop following my location") : t.t("Test here with my location")}
            </Button>
            {followNote && <span className={"field__hint" + (followNote.warn ? " zone-here__warn" : "")}>{followNote.text}</span>}
          </div>

          <SpoofMap zones={places} position={pos ?? null} focus={focus} onPick={moveTo} disabled={!team || busy || follow} others={others} />

          {sensed("compass", "level", "bearing", "noise", "shake") && (
            <DevicePanel motion={motion} onChange={setMotion} held={sensed("compass", "level", "bearing")} heard={sensed("noise")} shaken={sensed("shake")} />
          )}

          {current?.puzzle?.outputs?.length ? (
            <div className="sim__pass">
              <span className="field__label">{t.t("Get past it")}</span>
              <span className="field__hint">{t.t("Leave by an output without the answer — for a QR code you can't scan, or a place you're not at.")}</span>
              {current.puzzle.outputs.map((o) => (
                <Button key={o.port} type="button" size="sm" disabled={busy} onClick={() => void act({ kind: "force", node: current.slug, port: o.port })}>
                  {t.t("Pass: {{port}}", { port: portLabel(current, o.port, graph.roles) })}
                </Button>
              ))}
            </div>
          ) : null}

          {error && <div className="alert alert--danger">{error}</div>}

          {log.length > 0 && (
            <ol className="sim__log" aria-label={t.t("What happened")}>
              {log.map((l) => (
                <li key={l.id} className={l.tone === "warn" ? "is-warn" : ""}>
                  {l.text}
                </li>
              ))}
            </ol>
          )}
        </div>
      )}

      {bare && error && <div className="alert alert--danger">{error}</div>}

      {/* Laid out at the device's real size, then scaled: the step wraps
          and fits exactly as on the device, only smaller. The scale also
          makes the frame the containing block for the step's fixed
          overlays — confetti and sheets cover the phone, not the designer. */}
      <div className="sim__stage">
        {crew && <p className="sim__who">{phoneLabel(asSlot)}</p>}
        <div className="phone-fit" style={{ width: frameW * scale, height: frameH * scale }}>
          <div className="phone" aria-label={t.t("What the team sees")} style={{ width: frameW, height: frameH, transform: `scale(${scale})` }}>
            <div className="phone__screen">
              {view ? (
                <PlayView
                  // Each player's own phone: what one has half done — a
                  // pick not yet confirmed, an answer being typed — is not
                  // on the next player's screen.
                  key={`${replay}:${crew ? asSlot : 0}`}
                  view={view}
                  onAction={(a) => void act(a)}
                  busy={busy}
                  feedback={feedback}
                  live={false}
                  t={tr}
                  brand={brand}
                  teamName={t.t("Test team")}
                  placeNote={placeNote}
                  {...(pos ? { position: pos } : {})}
                  preview
                  instant={mode === "step"}
                  motion={motion}
                  storageKey={crew ? `sim-${slug}-${asSlot}` : `sim-${slug}`}
                  crew={!!crew}
                  {...(view.zone && !view.here ? { zoneNote: placeNote(view.zone) } : {})}
                />
              ) : (
                <p className="phone__empty">
                  {mode === "step" && selectedNode && !VISIBLE.has(selectedNode.type)
                    ? t.t("Teams never see this step. It happens the moment they reach it, and they carry on.")
                    : t.t("Select a puzzle, story or hub to see it as a team will.")}
                </p>
              )}
            </div>
            {ceremony && <StartCeremony title={title} brand={brand} onDone={() => setCeremony(false)} />}
          </div>
        </div>
        {crew && phones.length > 1 && (
          <div className="sim-crew" role="group" aria-label={t.t("The phones")}>
            {phones
              .map((p) => (
                // Not a <button>: the small screen inside has buttons of its own.
                <div
                  key={p.slot}
                  role="button"
                  tabIndex={0}
                  className={"sim-crew__phone" + (p.slot === asSlot ? " is-on" : "")}
                  aria-pressed={p.slot === asSlot}
                  onClick={() => pick(p.slot)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      pick(p.slot);
                    }
                  }}
                  title={t.t("Show {{name}}'s phone", { name: nameOf(p.slot) })}
                  aria-label={`${t.t("Show {{name}}'s phone", { name: nameOf(p.slot) })}: ${phoneStatus(p)}`}
                >
                  <div className="sim-crew__fit" style={{ width: frameW * MINI, height: frameH * MINI }} aria-hidden="true" inert>
                    <div className="phone" style={{ width: frameW, height: frameH, transform: `scale(${MINI})` }}>
                      <div className="phone__screen">
                        <PlayView view={p.view} onAction={() => {}} live={false} t={tr} brand={brand} teamName={t.t("Test team")} preview instant motion={motion} storageKey={`sim-${slug}-${p.slot}`} crew />
                      </div>
                    </div>
                  </div>
                  <span className="sim-crew__name">
                    {seen[p.slot] !== undefined && seen[p.slot] !== signature(p.view) && <span className="sim-crew__dot" title={t.t("Changed since you looked")} />}
                    {nameOf(p.slot)}
                  </span>
                  <span className="sim-crew__status">{phoneStatus(p)}</span>
                </div>
              ))}
          </div>
        )}
      </div>
    </div>
  );
}
