import {
  createContext,
  useContext,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { I18n, type Locale } from "@zonventure/i18n";
import { emails } from "@zonventure/api";
import { hostedSv } from "@zonventure/hosted";
import { trailSv } from "@zonventure/trail";
import classic from "../../../../internal/server/web/locales/web/sv.json";
import sv from "./locales/sv.json";
import contentSv from "./locales/content/sv.json";
import contentEt from "./locales/content/et.json";
import type { Node } from "@zonventure/graph";

/**
 * Catalogs.
 *
 * English is intentionally empty: the key is the English source
 * string, so t("Log in") returns "Log in" under en and an untranslated
 * Swedish key degrades to readable English instead of a bare
 * identifier.
 *
 * Swedish is three layers, later ones winning: the classic console's
 * catalog (same English keys, so a ported screen keeps its
 * translations), one file per ported area under locales/sv/ (so areas
 * are translated without touching each other's files), and this app's
 * own sv.json. The hosted views' and the simple creator's strings
 * (@zonventure/hosted, @zonventure/trail) come first.
 */
const areas = import.meta.glob<Record<string, string>>("./locales/sv/*.json", { eager: true, import: "default" });
const catalogs = { en: {}, sv: { ...hostedSv, ...trailSv, ...(classic as Record<string, string>), ...Object.assign({}, ...Object.values(areas)), ...sv } };

const instance = new I18n(catalogs);

/**
 * Text written into the adventure itself, such as a default name players
 * see, goes in the adventure's language, not the author's UI language.
 */
export function contentT(lang: string | undefined): I18n["t"] {
  return lang ? new I18n(contentCatalogs, lang).t : instance.t;
}
// Content words that read differently as UI ("Solved": "Lösta", a count) win here.
// The console has no Estonian, so an Estonian adventure's words are these alone.
const contentCatalogs = { en: {}, sv: { ...catalogs.sv, ...contentSv }, et: contentEt };

/** A new step's default route and group names, in the adventure's language. */
export function routesIn(n: Node, lang: string | undefined): Node {
  const t = contentT(lang);
  if (n.split?.paths) return { ...n, split: { ...n.split, paths: n.split.paths.map((p) => (p.label ? { ...p, label: t(p.label) } : p)) } };
  if (!n.puzzle?.outputs) return n;
  return { ...n, puzzle: { ...n.puzzle, outputs: n.puzzle.outputs.map((o) => (o.label ? { ...o, label: t(o.label) } : o)) } };
}
const I18nContext = createContext(instance);

/** Translate outside a component, in the active locale (a server message, say). */
export const translate = (key: string): string => instance.t(key);

/**
 * A language the signed-in person picked: the console shows it, and the
 * server writes their notifications in it. Only an explicit choice is
 * saved, never the browser's language on load.
 */
export function chooseLocale(locale: Locale): void {
  instance.setLocale(locale);
  void emails.setMyLanguage(locale).catch(() => {
    /* the next choice tells it again */
  });
}

export function I18nProvider({ children }: { children: ReactNode }) {
  return (
    <I18nContext.Provider value={instance}>{children}</I18nContext.Provider>
  );
}

/**
 * Subscribe to the active locale.
 *
 * useSyncExternalStore rather than state in a provider: the catalog
 * lives outside React, and this re-renders consumers on a locale
 * change without a reload. The old runtime called location.reload(),
 * which is the last thing you want mid-event on a bad connection.
 */
export function useT(): I18n {
  const i18n = useContext(I18nContext);
  useSyncExternalStore(
    (cb) => i18n.subscribe(cb),
    () => i18n.current(),
    () => i18n.current(),
  );
  return i18n;
}
