import { useState } from "react";
import { adaptable, type BlockOverride, type HostedEvent } from "./api.ts";
import { useHosted } from "./context.tsx";

/** Where the event is played: cards anywhere, or places in a park. */
export function WhereSection({ party }: { party: HostedEvent }) {
  const { i18n, api, run, busy } = useHosted();
  const t = i18n.t;
  const editable = adaptable(party);
  const choice = (mode: "qr" | "park", title: string, body: string, ok: boolean) => (
    <label className={"hosted-choice" + (party.mode === mode ? " is-on" : "")}>
      <input type="radio" name="mode" checked={party.mode === mode} disabled={!editable || busy || !ok} onChange={() => void run(() => api.setMode(mode))} />
      <span>
        <strong>{title}</strong>
        <small>{ok ? body : t("This adventure can't be played this way.")}</small>
      </span>
    </label>
  );
  return (
    <div className="hosted-choices" role="radiogroup" aria-label={t("Where are you playing?")}>
      {choice("qr", t("At home or in the garden"), t("You print cards and hide them; the teams scan them. No GPS needed."), party.cards_ok)}
      {choice("park", t("In a park"), t("You put each place where you want it, standing there with your phone; the teams walk to them."), party.park_ok)}
    </div>
  );
}

/**
 * The host's words: the values the adventure asks for, and the texts,
 * questions and answers its author let them rewrite.
 */
export function PersonaliseSection({ party }: { party: HostedEvent }) {
  const { i18n, api, run, busy } = useHosted();
  const t = i18n.t;
  const editable = adaptable(party);
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries(party.values.map((v) => [v.slug, v.value])));
  const [blocks, setBlocks] = useState<Record<string, BlockOverride>>(() =>
    Object.fromEntries(party.editables.filter((e) => e.kind).map((e) => [e.key, e.mine ?? { text: e.text ?? "", options: e.options ?? [], answer: e.answer ?? "" }])),
  );
  const [answers, setAnswers] = useState<Record<string, string>>(() =>
    Object.fromEntries(party.editables.filter((e) => !e.kind).map((e) => [e.key, (e.my_accepts ?? e.accepts ?? []).join(", ")])),
  );
  const [saved, setSaved] = useState(false);
  const setBlock = (key: string, b: BlockOverride) => {
    setSaved(false);
    setBlocks((all) => ({ ...all, [key]: b }));
  };

  const save = () =>
    run(async () => {
      await api.personalise({
        values,
        blocks,
        answers: Object.fromEntries(Object.entries(answers).map(([k, v]) => [k, v.split(",").map((s) => s.trim()).filter(Boolean)])),
      });
      setSaved(true);
    });

  return (
    <form
      className="hosted-form"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      {party.values.map((v) => (
        <label className="zv-field" key={v.slug}>
          <span>{v.label}</span>
          <input
            value={values[v.slug] ?? ""}
            maxLength={80}
            placeholder={v.default}
            disabled={!editable}
            onChange={(e) => {
              setSaved(false);
              setValues((all) => ({ ...all, [v.slug]: e.target.value }));
            }}
          />
        </label>
      ))}
      {party.editables.map((e) => {
        if (!e.kind) {
          return (
            <label className="zv-field" key={e.key}>
              <span>{t("Accepted answers in “{{step}}”", { step: e.step })}</span>
              <input
                value={answers[e.key] ?? ""}
                disabled={!editable}
                onChange={(ev) => {
                  setSaved(false);
                  setAnswers((all) => ({ ...all, [e.key]: ev.target.value }));
                }}
              />
              <small className="zv-muted">{t("Separate several with commas.")}</small>
            </label>
          );
        }
        const b = blocks[e.key] ?? {};
        if (e.kind === "text") {
          return (
            <label className="zv-field" key={e.key}>
              <span>{t("Text in “{{step}}”", { step: e.step })}</span>
              <textarea rows={3} value={b.text ?? ""} disabled={!editable} onChange={(ev) => setBlock(e.key, { ...b, text: ev.target.value })} />
            </label>
          );
        }
        const opts = b.options?.length === 3 ? b.options : ["", "", ""];
        return (
          <fieldset className="hosted-quiz" key={e.key} disabled={!editable}>
            <legend>{t("Question in “{{step}}”", { step: e.step })}</legend>
            <input value={b.text ?? ""} aria-label={t("Question")} onChange={(ev) => setBlock(e.key, { ...b, text: ev.target.value })} />
            {(["1", "X", "2"] as const).map((c, i) => (
              <label className="hosted-quiz__opt" key={c}>
                <input type="radio" name={"right-" + e.key} checked={b.answer === c} onChange={() => setBlock(e.key, { ...b, options: opts, answer: c })} aria-label={t("Right answer")} />
                <span className="hosted-quiz__mark">{t(["A", "B", "C"][i]!)}</span>
                <input
                  value={opts[i] ?? ""}
                  aria-label={t("Option {{c}}", { c })}
                  onChange={(ev) => setBlock(e.key, { ...b, options: opts.map((o, j) => (j === i ? ev.target.value : o)) })}
                />
              </label>
            ))}
            <small className="zv-muted">{t("Tick the right answer.")}</small>
          </fieldset>
        );
      })}
      {editable && (
        <div className="hosted-actions">
          <button className="zv-btn zv-btn--primary" disabled={busy}>
            {t("Save")}
          </button>
          {saved && <span className="zv-muted">{t("Saved. The next game uses your words.")}</span>}
        </div>
      )}
    </form>
  );
}

/** Where the host is standing, as precisely as the phone can tell. */
function whereAmI(): Promise<GeolocationPosition> {
  return new Promise((resolve, reject) => {
    if (!("geolocation" in navigator)) {
      reject(new Error("no geolocation"));
      return;
    }
    navigator.geolocation.getCurrentPosition(resolve, reject, { enableHighAccuracy: true, timeout: 20_000, maximumAge: 0 });
  });
}

/** A place on Google Maps, as the player's map links open it. */
const mapURL = (lat: number, lng: number) => `https://www.google.com/maps/search/?api=1&query=${lat},${lng}`;

/**
 * Putting the adventure's places in the host's park: walk to each spot
 * and tap, or move them all at once from where the host stands.
 */
export function PlacesSection({ party }: { party: HostedEvent }) {
  const { i18n, api, run, busy } = useHosted();
  const t = i18n.t;
  const [note, setNote] = useState<string | null>(null);
  const editable = adaptable(party);
  const here = (fn: (lat: number, lng: number) => Promise<unknown>) =>
    run(async () => {
      setNote(t("Finding where you are…"));
      try {
        const pos = await whereAmI();
        await fn(pos.coords.latitude, pos.coords.longitude);
        setNote(pos.coords.accuracy > 30 ? t("Placed, but your phone was only sure to about {{m}} m. Try again in the open for a better fix.", { m: Math.round(pos.coords.accuracy) }) : null);
      } catch (e) {
        if (e instanceof GeolocationPositionError || (e instanceof Error && e.message === "no geolocation")) {
          setNote(t("Your phone didn't say where you are. Allow location for this page and try again."));
          return;
        }
        setNote(null);
        throw e;
      }
    });
  const placed = party.places.filter((p) => p.movable && p.placed).length;
  const movable = party.places.filter((p) => p.movable).length;
  return (
    <>
      <p className="zv-note">{t("Choose a park without traffic, where the children can be watched. Each place is a circle of at least 25 metres: phones aren't more exact than that.")}</p>
      {editable && (
        <>
          <p className="zv-muted">{t("Walk to each spot and tap “Put it here”. Or stand in the middle of your park and move all the places at once, keeping the adventure's layout — then check each one.")}</p>
          <button className="zv-btn" disabled={busy} onClick={() => void here((lat, lng) => api.shiftTo(lat, lng))}>
            {t("Move all places to around me")}
          </button>
        </>
      )}
      {note && (
        <p className="zv-muted" role="status">
          {note}
        </p>
      )}
      <ul className="hosted-places">
        {party.places.map((p) => (
          <li key={p.slug}>
            <div>
              <strong>{p.name}</strong>
              <span className="zv-muted">
                {!p.movable ? t("Stays where the author put it") : p.placed ? t("Placed") : t("Not placed yet")}
                {p.placed && (
                  <>
                    {" · "}
                    <a href={mapURL(p.lat, p.lng)} target="_blank" rel="noopener noreferrer">
                      {t("Map")}
                    </a>
                  </>
                )}
              </span>
            </div>
            {editable && p.movable && (
              <button className="zv-btn" disabled={busy} onClick={() => void here((lat, lng) => api.placeAt(p.slug, lat, lng))}>
                {t("Put it here")}
              </button>
            )}
          </li>
        ))}
      </ul>
      <p className="zv-muted">{t("{{placed}} of {{total}} places put.", { placed, total: movable })}</p>
    </>
  );
}
