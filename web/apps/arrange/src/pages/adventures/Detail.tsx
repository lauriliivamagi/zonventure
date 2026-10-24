import { Suspense, lazy, useState, type ReactNode } from "react";
import { CalendarPlus, ChevronLeft, Compass, Copy, Download, ExternalLink, ListOrdered, MapPin, PartyPopper, Pencil, Play, Trash2, Workflow } from "lucide-react";
import { ApiError, request, runsEvents } from "@zonventure/api";
import { useT } from "../../i18n.tsx";
import { useSession } from "../../session.tsx";
import { useTopBarTitle } from "../../topbar.tsx";
import { CATALOG_ENABLED, href, navigate } from "../../router.ts";
import { Button, Card, ErrorBox, ICON, Loading, Page, PageHeader, Pill, Stat, Stats, useDialogs, useLoad, useToast } from "../../ui/index.ts";
import { NewEventDialog } from "../events/NewEventDialog.tsx";
import { NewQuestDialog } from "../events/NewQuestDialog.tsx";
import { HostItDialog } from "../events/HostItDialog.tsx";
import { deleteAdventure, duplicateAdventure, exportURL, firstPlaceOf, getAdventure, getGraphState, isPublisher, listAdventures, listAssignments, type AdventureDetail, type Assignment } from "./api.ts";
import { AdventureMap } from "./AdventureMap.tsx";
import { EditDetailsDialog, PickPassDialog } from "./dialogs.tsx";
import { extentKm, formatKm, pinsOf } from "./facts.ts";
import type { Schedule } from "./List.tsx";
import { Cover, LanguagePill, lookCover, puzzleCount } from "./parts.tsx";
import { MoreMenu, type MenuItem } from "../event/Menu.tsx";

const TestPlay = lazy(() => import("../../designer/TestPlay.tsx"));

/**
 * An adventure's own page: what it is, where it happens and what it is
 * made of, and — for its authors — the way into the designer and the
 * adventure's housekeeping (details, cover, copy, export, delete).
 */
export function AdventurePage({ slug }: { slug: string }) {
  const t = useT();
  // The list row says whether the org holds it through a commission; the detail payload doesn't.
  const load = useLoad(
    (signal) => Promise.all([getAdventure(slug, signal), listAssignments(signal).catch(() => [] as Assignment[]), listAdventures(signal).catch(() => [])]),
    [slug],
  );
  useTopBarTitle(load.data?.[0].title ?? null);

  if (load.error && !load.data) {
    return (
      <Page>
        <PageHeader title={t.t("Adventure")} back={{ href: href({ name: "adventures" }), label: t.t("Adventures") }} />
        <ErrorBox message={load.error} retry={load.reload} retryLabel={t.t("Retry")} />
      </Page>
    );
  }
  if (!load.data) return <Page><Loading label={t.t("Loading…")} /></Page>;
  const [a, assignments, rows] = load.data;
  return <Detail a={a} shared={!a.mine_to_edit && !!rows.find((r) => r.slug === a.slug)?.shared} passes={assignments.filter((x) => x.adventure_slug === a.slug && !x.event_id)} reload={load.reload} />;
}

function Detail({ a, shared, passes, reload }: { a: AdventureDetail; shared: boolean; passes: Assignment[]; reload: () => void }) {
  const t = useT();
  const toast = useToast();
  const dialogs = useDialogs();
  const { state } = useSession();
  const user = state.status === "authenticated" ? state.user : null;
  const [schedule, setSchedule] = useState<Schedule | null>(null);
  const [selling, setSelling] = useState(false);
  const [hosting, setHosting] = useState(false);
  const [testing, setTesting] = useState(false);
  const [picking, setPicking] = useState(false);
  const [editing, setEditing] = useState(false);
  const [cover, setCover] = useState(a.avatar_url);
  const [copying, setCopying] = useState(false);

  const g = a.graph ?? null;
  const pins = pinsOf(a);
  const zoneNames = new Map((g?.zones ?? []).map((z) => [z.slug, z.name || z.slug]));
  // Made in the designer but not published: no graph to describe.
  const designed = !g;
  const accent = a.colors?.accent;
  // The org's own adventure, whoever may edit it: scheduled through the buy-then-schedule flow.
  const own = a.mine_to_edit || (a.org_id != null && a.org_id === user?.org_id);

  const puzzles = g ? g.puzzle_count : 0;
  const places = g ? new Set(g.steps.map((s) => s.zone).filter((z) => z && zoneNames.has(z))).size : 0;
  const km = extentKm(pins);

  const start = a.start_lat != null && a.start_lng != null
    ? { lat: a.start_lat, lng: a.start_lng, label: a.start_label || "", radius_m: a.start_radius_m }
    : null;
  const firstPlace = g ? firstPlaceOf(g) : null;

  const openSchedule = () => {
    if (passes.length === 1) setSchedule({ assignmentId: passes[0]!.id });
    else if (passes.length > 1) setPicking(true);
    else if (own) setSchedule({ slug: a.slug, ownsIt: true });
    else setSchedule({ slug: a.slug, ownsIt: !shared });
  };

  const duplicate = async () => {
    if (copying) return;
    setCopying(true);
    try {
      const r = await duplicateAdventure(a.slug, t.t("{{name}} (copy)", { name: a.title }));
      toast.ok(t.t("Duplicated. This is the copy — rename it under Edit details."));
      location.hash = href({ name: "adventure", slug: r.slug });
    } catch (e) {
      toast.err(e);
    } finally {
      setCopying(false);
    }
  };

  const remove = async () => {
    const pw = await dialogs.password({
      title: t.t("Delete adventure “{{title}}”?", { title: a.title }),
      message: t.t("This removes the adventure and all its puzzles. Enter your password to confirm."),
      confirmLabel: t.t("Delete adventure"),
      danger: true,
    });
    if (!pw) return;
    try {
      await deleteAdventure(a.slug, pw);
      toast.ok(t.t("Deleted"));
      location.hash = href({ name: "adventures" });
    } catch (e) {
      toast.err(e);
    }
  };

  const mapped = !designed && pins.length > 0;
  const openInMaps = start && (
    <a className="btn adv-btn sm" href={`https://www.google.com/maps/search/?api=1&query=${start.lat},${start.lng}`} target="_blank" rel="noopener" title={t.t("Open the starting point in maps")}>
      <ExternalLink size={ICON.sm} aria-hidden />
      {t.t("Open in maps")}
    </a>
  );
  const startLine = start && (
    <div className="adv-start">
      <MapPin size={ICON.sm} aria-hidden />
      <span>
        <strong>{mapped ? t.t("Start: {{place}}", { place: start.label || t.t("Start here") }) : start.label || t.t("Start here")}</strong>{" "}
        <span className="cs-mono cs-sub">{start.lat.toFixed(5)}, {start.lng.toFixed(5)}</span>
      </span>
    </div>
  );

  const pills: ReactNode[] = [];
  if (CATALOG_ENABLED && a.mine_to_edit && isPublisher(user)) {
    pills.push(
      <Pill key="listed" tone={a.published ? "good" : "neutral"} title={a.published ? t.t("Every organization finds it in the Catalog.") : t.t("Other organizations can't find it until the operator lists it.")}>
        {a.published ? t.t("In the Catalog") : t.t("Not in the Catalog")}
      </Pill>,
    );
  }
  if (shared) pills.push(<Pill key="shared" tone="accent" title={t.t("Commissioned for your organization")}>{t.t("Shared")}</Pill>);
  if (passes.length) pills.push(<Pill key="passes" tone="accent">{t.plural(passes.length, { one: t.t("{{n}} event pass ready"), other: t.t("{{n}} event passes ready") })}</Pill>);
  for (const code of a.graph?.languages ?? (a.language ? [a.language] : [])) pills.push(<LanguagePill key={`lang-${code}`} code={code} />);
  if ((a.running_event_count ?? 0) > 0)
    pills.push(<Pill key="run" tone="warn">{t.plural(a.running_event_count!, { one: t.t("1 event running now"), other: t.t("{{n}} events running now") })}</Pill>);

  const more: MenuItem[] = [];
  if (a.mine_to_edit) {
    more.push(
      { label: t.t("Edit details"), icon: <Pencil size={ICON.sm} aria-hidden />, title: t.t("Title, short name, description, intro, start and cover"), onClick: () => setEditing(true) },
      { label: t.t("Duplicate"), icon: <Copy size={ICON.sm} aria-hidden />, title: t.t("Make an independent copy in your organization"), onClick: () => void duplicate() },
      { label: t.t("Export"), icon: <Download size={ICON.sm} aria-hidden />, href: exportURL(a.slug), download: `${a.slug}.zvadv.zip`, title: t.t("Download this adventure as a portable .zip, its pictures and sounds included") },
      { separator: true },
      { label: t.t("Delete adventure"), icon: <Trash2 size={ICON.sm} aria-hidden />, danger: true, title: t.t("Delete the adventure and all its puzzles. An adventure that has had events can’t be deleted."), onClick: () => void remove() },
    );
  }

  // A quiz trail is edited as cards; the designer takes it over for good.
  const toDesigner = async () => {
    const ok = await dialogs.confirm({
      title: t.t("Open in the designer?"),
      message: t.t("The designer can do much more, but you can't go back to the simple editor: its cards are left behind."),
      confirmLabel: t.t("Open in the designer"),
      danger: true,
    });
    if (!ok) return;
    try {
      await request(`/api/admin/adventures/${encodeURIComponent(a.slug)}/cards/designer`, { method: "POST" });
      navigate({ name: "designer", slug: a.slug });
    } catch (e) {
      toast.err(e);
    }
  };
  if (a.mine_to_edit && a.trail) {
    more.unshift(
      { label: t.t("Open in the designer"), icon: <Workflow size={ICON.sm} aria-hidden />, title: t.t("Hand the trail over to the full designer, for good"), onClick: () => void toDesigner() },
      { separator: true },
    );
  }

  const actions = (
    <>
      {a.mine_to_edit &&
        (a.trail ? (
          <a className="btn adv-btn primary" href={href({ name: "trail", slug: a.slug })} title={t.t("Edit the party adventure's cards")}>
            <ListOrdered size={ICON.md} aria-hidden />
            {t.t("Edit cards")}
          </a>
        ) : (
          <a className="btn adv-btn primary" href={href({ name: "designer", slug: a.slug })} title={t.t("Design the adventure as a flow of steps")}>
            <Workflow size={ICON.md} aria-hidden />
            {t.t("Open designer")}
          </a>
        ))}
      {user && runsEvents(user) && !designed && (
        <Button variant={a.mine_to_edit ? "secondary" : "primary"} icon={<CalendarPlus size={ICON.md} aria-hidden />} onClick={openSchedule}>
          {t.t("Schedule event")}
        </Button>
      )}
      {a.mine_to_edit && (
        <Button icon={<Play size={ICON.md} aria-hidden />} title={t.t("Play the draft from the start, as a team")} onClick={() => setTesting(true)}>
          {t.t("Test play")}
        </Button>
      )}
      {(a.mine_to_edit || shared) && !designed && !!user && runsEvents(user) && (
        <Button icon={<Compass size={ICON.md} aria-hidden />} title={t.t("New self-guided adventure")} onClick={() => setSelling(true)}>
          {t.t("Self-guided adventure")}
        </Button>
      )}
      {(a.mine_to_edit || shared) && !designed && !!user && runsEvents(user) && (
        <Button icon={<PartyPopper size={ICON.md} aria-hidden />} title={t.t("Run it yourself, game after game, from a host page")} onClick={() => setHosting(true)}>
          {t.t("Host it")}
        </Button>
      )}
      {more.length > 0 && <MoreMenu label={t.t("More")} items={more} />}
    </>
  );

  return (
    <Page>
      <header className="adv-hero">
        <a className="cs-back" href={href({ name: "adventures" })}>
          <ChevronLeft size={16} aria-hidden />
          {t.t("Adventures")}
        </a>
        <div className="adv-hero__row">
          {a.mine_to_edit ? (
            <button type="button" className="adv-hero__cover" onClick={() => setEditing(true)} title={t.t("Change the cover picture")} aria-label={t.t("Change the cover picture")}>
              <Cover title={a.title} src={cover} photo={lookCover(a.theme, a.cover)} size={96} accent={accent} />
            </button>
          ) : (
            <Cover title={a.title} src={cover} photo={lookCover(a.theme, a.cover)} size={96} accent={accent} />
          )}
          <div className="adv-hero__text">
            <h1 className="cs-title">{a.title}</h1>
            <p className="adv-hero__meta">
              {a.author && <span>{a.author}</span>}
              <span className="cs-mono cs-small">{a.slug}</span>
            </p>
            {pills.length > 0 && <div className="adv-hero__pills">{pills}</div>}
          </div>
        </div>
        <div className="adv-hero__actions">{actions}</div>
      </header>

      {!designed && (
        <Stats>
          <Stat value={String(puzzles)} label={t.plural(puzzles, { one: t.t("puzzle"), other: t.t("puzzles") })} />
          <Stat value={String(places)} label={t.plural(places, { one: t.t("place to reach"), other: t.t("places to reach") })} />
          {km > 0 && <Stat value={formatKm(km, t.current())} label={t.t("across")} />}
        </Stats>
      )}

      {(a.description || a.intro_text?.trim() || a.outro_text?.trim()) && (
        <Card title={t.t("About")}>
          {a.description && <p className="adv-pre">{a.description}</p>}
          {a.intro_text?.trim() && (
            <div>
              <h3 className="adv-h3">{t.t("Intro shown to players")}</h3>
              <Prose html={a.intro_html} text={a.intro_text} />
            </div>
          )}
          {a.outro_text?.trim() && (
            <div>
              <h3 className="adv-h3">{t.t("Outro on completion")}</h3>
              <Prose html={a.outro_html} text={a.outro_text} />
            </div>
          )}
        </Card>
      )}

      {designed && <DesignedSteps slug={a.slug} canRead={a.mine_to_edit} />}

      {mapped ? (
        <Card title={t.t("Map")} subtitle={t.t("Where the steps happen: the zones teams must reach.")} actions={start && openInMaps}>
          {start && startLine}
          <AdventureMap pins={pins} accent={accent} label={t.t("Map")} />
        </Card>
      ) : start && (
        <Card title={t.t("Start here")} actions={openInMaps}>
          {startLine}
          <AdventureMap pins={[{ title: start.label || t.t("Start here"), lat: start.lat, lng: start.lng, ...(start.radius_m ? { radius_m: start.radius_m } : {}), gated: false }]} accent={accent} label={t.t("Start here")} height={200} />
        </Card>
      )}

      {g && (
        <Card title={t.t("Steps")} flush>
          {g.steps.length === 0 ? (
            <p className="cs-card__note cs-muted">{t.t("Nothing designed yet.")}</p>
          ) : (
            <ol className="adv-steps">
              {g.steps.map((s, i) => (
                <li key={s.slug + i} className="adv-step">
                  <Pill tone={s.type === "puzzle" ? "accent" : "neutral"}>{stepKind(t, s.type)}</Pill>
                  {s.title && <strong>{s.title}</strong>}
                  {s.zone && zoneNames.has(s.zone) && (
                    <span className="cs-muted adv-step__zone">
                      <MapPin size={ICON.xs} aria-hidden />
                      {zoneNames.get(s.zone)}
                    </span>
                  )}
                </li>
              ))}
            </ol>
          )}
          {g.has_quiz && <p className="cs-card__note cs-muted">{t.t("Includes a quiz walk.")}</p>}
        </Card>
      )}


      {editing && (
        <EditDetailsDialog
          slug={a.slug}
          firstPlace={firstPlace ? { lat: firstPlace.lat, lng: firstPlace.lng } : null}
          onClose={() => setEditing(false)}
          onCover={(url) => setCover(url || undefined)}
          onSaved={(next) => {
            setEditing(false);
            if (next !== a.slug) location.hash = href({ name: "adventure", slug: next });
            else reload();
          }}
        />
      )}
      {picking && (
        <PickPassDialog
          title={a.title}
          passes={passes}
          onClose={() => setPicking(false)}
          onPick={(id) => {
            setPicking(false);
            setSchedule({ assignmentId: id });
          }}
        />
      )}
      {schedule && <NewEventDialog {...schedule} onClose={() => setSchedule(null)} />}
      {testing && (
        <Suspense fallback={null}>
          <TestPlay slug={a.slug} title={a.title} onClose={() => setTesting(false)} />
        </Suspense>
      )}
      {selling && <NewQuestDialog slug={a.slug} onClose={() => setSelling(false)} onCreated={(id) => navigate({ name: "event", id })} />}
      {hosting && <HostItDialog slug={a.slug} title={a.title || a.slug} onClose={() => setHosting(false)} />}
    </Page>
  );
}

function stepKind(t: ReturnType<typeof useT>, type: string): string {
  switch (type) {
    case "puzzle": return t.t("Puzzle");
    case "story": return t.t("Story");
    case "hub": return t.t("Choice");
    case "split": return t.t("Split up");
    case "end": return t.t("End");
    default: return type;
  }
}

/** Server-rendered markdown (the same goldmark pipeline as puzzle prompts), else the raw text. */
function Prose({ html, text }: { html?: string | undefined; text?: string | undefined }) {
  return html ? <div className="adv-prose" dangerouslySetInnerHTML={{ __html: html }} /> : <p className="adv-pre">{text}</p>;
}

/** An adventure made in the designer, not yet published: how much is there, and whether players can play it yet. */
function DesignedSteps({ slug, canRead }: { slug: string; canRead: boolean }) {
  const t = useT();
  const load = useLoad(
    (signal) =>
      canRead
        ? getGraphState(slug, signal).catch((e: unknown) => {
            if (e instanceof ApiError && e.code === "graph_not_found") return null;
            throw e;
          })
        : Promise.resolve(null),
    [slug, canRead],
  );
  let body: ReactNode;
  if (load.loading && !load.data) body = <Loading label={t.t("Loading…")} />;
  else if (load.error) body = <ErrorBox message={load.error} retry={load.reload} retryLabel={t.t("Retry")} />;
  else {
    const nodes = load.data?.envelope?.graph?.nodes ?? [];
    const puzzles = nodes.filter((n) => n.type === "puzzle").length;
    const steps = nodes.filter((n) => n.type !== "start" && n.type !== "end").length;
    const pub = load.data?.published;
    body = (
      <>
        <p>{steps ? t.plural(steps, { one: t.t("{{n}} step"), other: t.t("{{n}} steps") }) + " · " + puzzleCount(t, puzzles) : t.t("Nothing designed yet.")}</p>
        <p className="cs-muted">{pub ? t.t("Published (version {{n}}). New events use this version; ones already running keep theirs.", { n: pub.version }) : t.t("Not published yet, so players can't play it. Publish it from the designer.")}</p>
      </>
    );
  }
  return <Card title={t.t("Steps")}>{body}</Card>;
}

