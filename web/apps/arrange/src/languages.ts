// The languages adventures are written and translated in, for the
// designer's translations and an event's store listing alike.

/** Each language in its own words. */
export const LANGUAGE_NAMES: Record<string, string> = {
  sv: "Svenska",
  en: "English",
  et: "Eesti",
  de: "Deutsch",
  fi: "Suomi",
  no: "Norsk",
  da: "Dansk",
  fr: "Français",
  es: "Español",
};
export const languageName = (l: string) => LANGUAGE_NAMES[l] ?? l.toUpperCase();
