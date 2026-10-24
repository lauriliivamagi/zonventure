import {
  createContext,
  useContext,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { I18n } from "@zonventure/i18n";
import en from "./locales/en.json";
import sv from "./locales/sv.json";
import et from "./locales/et.json";

/**
 * Catalogs. English holds only a walk's words ("tour|" keys), because
 * the key is the English source string. Swedish is the primary market,
 * so every player-facing string — this app's and @zonventure/play-ui's
 * — has a translation here. This fork's guests are in Estonia, so
 * Estonian has every one too.
 */
const catalogs = { en, sv, et };

const instance = new I18n(catalogs);
const I18nContext = createContext(instance);

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
    () => `${i18n.current()}|${i18n.variant() ?? ""}`,
    () => `${i18n.current()}|${i18n.variant() ?? ""}`,
  );
  return i18n;
}
