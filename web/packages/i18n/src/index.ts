// i18n runtime.
//
// Ported from internal/server/web/i18n.js, keeping its conventions
// because 2,116 lines of catalog on each side are already written to
// them and the Go-side completeness test depends on them:
//
//   * The key IS the English source string. The English catalog is
//     empty, so t("Log in") returns "Log in" under en and a missing
//     Swedish key degrades to readable English rather than to a
//     visible "missing.key.name".
//   * Interpolation is {{name}}.
//   * Author-provided content — puzzle title, prompt, hints, accept
//     lists — is deliberately NOT translated. An adventure authored in
//     one language renders in that language whatever the UI locale is.
//
// One thing deliberately changed: the old setLang did
// location.reload(). Here the catalog swaps in place. Reloading
// mid-event on a phone in a forest is exactly when you least want to
// refetch anything.

export type Locale = string;
export type Catalog = Record<string, string>;
export type Vars = Record<string, string | number>;

export interface LanguageLabel {
  flag: string;
  name: string;
}

/**
 * BCP 47 → label. The flag is a regional-indicator pair, which renders
 * as a flag everywhere except Windows before 11, where the bare
 * letters show instead — acceptable, since the code is displayed
 * alongside. English uses 🇬🇧 because the product is EU-facing.
 */
export const LANGUAGE_LABELS: Record<string, LanguageLabel> = {
  en: { flag: "🇬🇧", name: "English" },
  sv: { flag: "🇸🇪", name: "Svenska" },
  et: { flag: "🇪🇪", name: "Eesti" },
  nb: { flag: "🇳🇴", name: "Norsk bokmål" },
  da: { flag: "🇩🇰", name: "Dansk" },
  fi: { flag: "🇫🇮", name: "Suomi" },
  de: { flag: "🇩🇪", name: "Deutsch" },
  fr: { flag: "🇫🇷", name: "Français" },
  es: { flag: "🇪🇸", name: "Español" },
  nl: { flag: "🇳🇱", name: "Nederlands" },
};

/**
 * Flag for an adventure's content language, or "" when unrecognised.
 * Tries the full lowercased tag first ("pt-BR" → 🇧🇷), then the primary
 * subtag ("en-US" → "en" → 🇬🇧). Authors using tags we don't list get
 * the bare code, which is fine.
 */
export function flagFor(code: string | null | undefined): string {
  if (!code) return "";
  const c = String(code).trim().toLowerCase();
  const exact = LANGUAGE_LABELS[c];
  if (exact) return exact.flag;
  const primary = c.split("-")[0];
  return (primary && LANGUAGE_LABELS[primary]?.flag) || "";
}

export const LOCALE_STORAGE_KEY = "zv-lang";

/**
 * Ambiguous or unsupported falls to English. Upstream falls to Swedish,
 * its primary market; this fork's guests are in Estonia, where an
 * untranslated screen is better read in English.
 */
export const DEFAULT_LOCALE = "en";

export class I18n {
  private catalogs: Record<Locale, Catalog>;
  private locale: Locale;
  private register: string | null = null;
  private listeners = new Set<() => void>();

  constructor(catalogs: Record<Locale, Catalog>, locale?: Locale) {
    this.catalogs = catalogs;
    this.locale = locale ?? pickInitialLocale(Object.keys(catalogs));
  }

  /** Locales the UI chrome is actually translated into. */
  available(): Locale[] {
    return Object.keys(this.catalogs);
  }

  current(): Locale {
    return this.locale;
  }

  /**
   * Translate. Unknown keys return the key itself, which is the
   * English source string — so a gap shows as untranslated English
   * rather than as a broken-looking identifier.
   */
  t = (key: string, vars?: Vars): string => {
    const catalog = this.catalogs[this.locale];
    const translated = (this.register ? catalog?.[`${this.register}|${key}`] : undefined) ?? catalog?.[key] ?? key;
    return vars ? interpolate(translated, vars) : translated;
  };

  /**
   * Translate a server error code, falling back to the server's own
   * English message. Handlers that don't emit a code yet pass through
   * unchanged — the documented incremental path.
   */
  tError = (code: string | undefined, fallback: string): string => {
    if (!code) return fallback;
    const key = "error." + code;
    const translated = this.catalogs[this.locale]?.[key];
    return translated ?? fallback;
  };

  /**
   * A way of speaking, such as "tour" for a walk rather than a game: a
   * catalog's "tour|<key>" entry then wins over the key's own. A key
   * with no such entry reads as usual, so a register only lists what
   * it changes — in every catalog, English included.
   */
  variant(): string | null {
    return this.register;
  }

  setVariant(register: string | null): void {
    if (register === this.register) return;
    this.register = register;
    for (const fn of this.listeners) fn();
  }

  setLocale(locale: Locale): void {
    if (!this.catalogs[locale] || locale === this.locale) return;
    this.locale = locale;
    try {
      localStorage.setItem(LOCALE_STORAGE_KEY, locale);
    } catch {
      /* private mode — applies for this session only */
    }
    for (const fn of this.listeners) fn();
  }

  /** Subscribe to locale changes. Returns an unsubscribe function. */
  subscribe(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  // --- Locale-aware formatting the old runtime could not do ---------

  formatNumber(n: number, opts?: Intl.NumberFormatOptions): string {
    return new Intl.NumberFormat(this.locale, opts).format(n);
  }

  formatDate(d: Date | string, opts?: Intl.DateTimeFormatOptions): string {
    const date = typeof d === "string" ? new Date(d) : d;
    return new Intl.DateTimeFormat(this.locale, opts).format(date);
  }

  /**
   * Plural selection by CLDR category. Swedish and English both have
   * only "one" and "other", but doing this properly now means adding
   * a language with more categories is a catalog change, not a code
   * change.
   */
  plural(n: number, forms: Partial<Record<Intl.LDMLPluralRule, string>>): string {
    const rule = new Intl.PluralRules(this.locale).select(n);
    const form = forms[rule] ?? forms.other ?? "";
    return interpolate(form, { n });
  }
}

/** Replace {{name}} placeholders. Unknown names are left intact. */
export function interpolate(template: string, vars: Vars): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  );
}

/**
 * Choose a starting locale: an explicit stored choice, else the best
 * browser match, else DEFAULT_LOCALE.
 */
export function pickInitialLocale(available: Locale[]): Locale {
  const has = (l: string) => available.includes(l);

  try {
    const stored = localStorage.getItem(LOCALE_STORAGE_KEY);
    if (stored && has(stored)) return stored;
  } catch {
    /* fall through to browser preference */
  }

  for (const tag of globalThis.navigator?.languages ?? []) {
    const lower = tag.toLowerCase();
    if (has(lower)) return lower;
    const primary = lower.split("-")[0];
    if (primary && has(primary)) return primary;
  }

  return has(DEFAULT_LOCALE) ? DEFAULT_LOCALE : (available[0] ?? DEFAULT_LOCALE);
}
