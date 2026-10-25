import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { play } from "@zonventure/api";
import { I18n } from "@zonventure/i18n";
import { PlayView, type Feedback } from "@zonventure/play-ui";
import "@zonventure/play-ui/play-ui.css";
import playSv from "../../../play/src/locales/sv.json";
import { useT } from "../i18n.tsx";
import { DEMO_ANSWER_EVENT } from "./blocks/Animation.tsx";
import "./phone.css";
import { DEVICES } from "./Simulator.tsx";

const PHONE_CATALOGS = { en: {}, sv: playSv as Record<string, string> };
const DEVICE = DEVICES[1];
const BEZEL = 8;
const SCALE = 0.55;
const nowhere = () => {};

/** The player app's words in the console's language, for a phone preview. */
export function usePhoneT() {
  const t = useT();
  const lang = t.current() in PHONE_CATALOGS ? t.current() : "en";
  return useMemo(() => new I18n(PHONE_CATALOGS, lang).t, [lang]);
}

/**
 * A look on a phone without an adventure: one made-up puzzle — a title,
 * a story, a note and the answer bar — in the player's own PlayView, so
 * an organisation's theme is judged as teams will see it. Try a right or
 * wrong answer acts out the celebration, as in the designer's preview.
 */
export function SamplePreview({ brand }: { brand: play.Brand }) {
  const t = useT();
  const tr = usePhoneT();
  const [feedback, setFeedback] = useState<Feedback | null>(null);
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
  const view = useMemo<play.View>(
    () => ({
      node: "sample",
      type: "puzzle",
      blocks: [
        { id: "title", kind: "title", text: t.t("Erik's stolen elephants") },
        { id: "story", kind: "text", text: t.t("Hi! Someone has stolen my elephants. Can you help me find them?") },
        { id: "clue", kind: "note", text: t.t("The thief left a note: count the lamp posts along the path.") },
      ],
      input: { kind: "text" },
      score: 40,
      penalty_score: 0,
      penalty_seconds: 0,
      now: new Date().toISOString(),
    }),
    [t],
  );
  return (
    <PhoneFrame>
      <PlayView view={view} onAction={nowhere} feedback={feedback} live={false} t={tr} brand={brand} preview instant storageKey="theme-sample" />
    </PhoneFrame>
  );
}

/** The designer's phone frame at preview size, for a screen without the simulator. */
export function PhoneFrame({ children }: { children: ReactNode }) {
  const t = useT();
  const w = DEVICE.w + 2 * BEZEL;
  const h = DEVICE.h + 2 * BEZEL;
  return (
    <div className="phone-fit" style={{ width: w * SCALE, height: h * SCALE }}>
      <div className="phone" aria-label={t.t("What the team sees")} style={{ width: w, height: h, transform: `scale(${SCALE})` }}>
        <div className="phone__screen">{children}</div>
      </div>
    </div>
  );
}
