import { test, describe } from "node:test";
import assert from "node:assert/strict";
import {
  I18n,
  interpolate,
  flagFor,
  pickInitialLocale,
  DEFAULT_LOCALE,
} from "./index.ts";

const catalogs = {
  // English is empty on purpose: the key is the source string.
  en: {},
  sv: {
    "Log in": "Logga in",
    "Puzzle {{i}} of {{n}}": "Pussel {{i}} av {{n}}",
    "error.invalid_credentials": "Fel användarnamn eller lösenord",
  },
};

describe("interpolate", () => {
  test("substitutes named placeholders", () => {
    assert.equal(interpolate("{{a}} and {{b}}", { a: "x", b: "y" }), "x and y");
  });

  test("substitutes repeats", () => {
    assert.equal(interpolate("{{n}}/{{n}}", { n: 2 }), "2/2");
  });

  // Leaving an unknown placeholder visible beats rendering "undefined":
  // it points straight at the missing variable.
  test("leaves unknown placeholders intact", () => {
    assert.equal(interpolate("{{a}} {{b}}", { a: "x" }), "x {{b}}");
  });

  test("passes through a template with no placeholders", () => {
    assert.equal(interpolate("plain", { a: "x" }), "plain");
  });
});

describe("t", () => {
  test("translates a known key", () => {
    assert.equal(new I18n(catalogs, "sv").t("Log in"), "Logga in");
  });

  // The whole point of English-source keys: a gap degrades to readable
  // English, never to a broken-looking identifier.
  test("falls back to the key itself when untranslated", () => {
    assert.equal(new I18n(catalogs, "sv").t("Not translated"), "Not translated");
  });

  test("returns the source string under en", () => {
    assert.equal(new I18n(catalogs, "en").t("Log in"), "Log in");
  });

  test("interpolates into a translation", () => {
    assert.equal(
      new I18n(catalogs, "sv").t("Puzzle {{i}} of {{n}}", { i: 1, n: 3 }),
      "Pussel 1 av 3",
    );
  });
});

describe("tError", () => {
  test("prefers the translated code", () => {
    assert.equal(
      new I18n(catalogs, "sv").tError("invalid_credentials", "Bad login"),
      "Fel användarnamn eller lösenord",
    );
  });

  // Handlers that do not emit a code yet must keep working — that is
  // the documented incremental upgrade path, not a bug.
  test("falls back when no code is supplied", () => {
    assert.equal(new I18n(catalogs, "sv").tError(undefined, "Bad login"), "Bad login");
  });

  test("falls back when the code has no translation", () => {
    assert.equal(new I18n(catalogs, "sv").tError("unknown_code", "Bad login"), "Bad login");
  });
});

describe("setLocale", () => {
  test("swaps the catalog and notifies subscribers", () => {
    const i18n = new I18n(catalogs, "en");
    let calls = 0;
    i18n.subscribe(() => calls++);

    i18n.setLocale("sv");
    assert.equal(i18n.current(), "sv");
    assert.equal(i18n.t("Log in"), "Logga in");
    assert.equal(calls, 1);
  });

  test("ignores an unavailable locale", () => {
    const i18n = new I18n(catalogs, "en");
    i18n.setLocale("de");
    assert.equal(i18n.current(), "en");
  });

  test("does not notify when the locale is unchanged", () => {
    const i18n = new I18n(catalogs, "sv");
    let calls = 0;
    i18n.subscribe(() => calls++);
    i18n.setLocale("sv");
    assert.equal(calls, 0);
  });

  test("unsubscribe stops notifications", () => {
    const i18n = new I18n(catalogs, "en");
    let calls = 0;
    const off = i18n.subscribe(() => calls++);
    off();
    i18n.setLocale("sv");
    assert.equal(calls, 0);
  });
});

describe("setVariant", () => {
  const withTour = {
    en: { "tour|The game is over": "The walk is over" },
    sv: { "The game is over": "Spelet är slut", "tour|The game is over": "Rundan är slut", "Log in": "Logga in" },
  };

  test("a variant's entry wins, in every locale, and notifies subscribers", () => {
    const i18n = new I18n(withTour, "en");
    let calls = 0;
    i18n.subscribe(() => calls++);
    i18n.setVariant("tour");
    assert.equal(i18n.variant(), "tour");
    assert.equal(i18n.t("The game is over"), "The walk is over");
    i18n.setLocale("sv");
    assert.equal(i18n.t("The game is over"), "Rundan är slut");
    assert.equal(calls, 2);
  });

  test("a key the variant does not change reads as usual", () => {
    const i18n = new I18n(withTour, "sv");
    i18n.setVariant("tour");
    assert.equal(i18n.t("Log in"), "Logga in");
    i18n.setVariant(null);
    assert.equal(i18n.t("The game is over"), "Spelet är slut");
  });
});

describe("flagFor", () => {
  test("resolves an exact tag", () => {
    assert.equal(flagFor("sv"), "🇸🇪");
  });

  test("falls back to the primary subtag", () => {
    assert.equal(flagFor("en-US"), "🇬🇧");
  });

  test("is case-insensitive and trims", () => {
    assert.equal(flagFor("  SV "), "🇸🇪");
  });

  test("knows Estonian", () => {
    assert.equal(flagFor("et"), "🇪🇪");
  });

  test("returns empty for an unknown tag", () => {
    assert.equal(flagFor("tlh"), "");
  });

  test("returns empty for nullish input", () => {
    assert.equal(flagFor(null), "");
    assert.equal(flagFor(undefined), "");
    assert.equal(flagFor(""), "");
  });
});

describe("pickInitialLocale", () => {
  // Node exposes a navigator global, so these must pin it rather than
  // assume it is absent — otherwise the test asserts whatever locale
  // the machine running it happens to have.
  function withLanguages<T>(languages: string[] | undefined, fn: () => T): T {
    const original = Object.getOwnPropertyDescriptor(globalThis, "navigator");
    try {
      Object.defineProperty(globalThis, "navigator", {
        value: languages === undefined ? undefined : { languages },
        configurable: true,
      });
      return fn();
    } finally {
      if (original) Object.defineProperty(globalThis, "navigator", original);
      else delete (globalThis as { navigator?: unknown }).navigator;
    }
  }

  // The browser preference is a real signal and wins — this is a user
  // stating what they want to read.
  test("honours a supported browser language", () => {
    assert.equal(
      withLanguages(["en-US", "en"], () => pickInitialLocale(["en", "sv"])),
      "en",
    );
  });

  test("matches on the primary subtag", () => {
    assert.equal(
      withLanguages(["sv-SE"], () => pickInitialLocale(["en", "sv"])),
      "sv",
    );
  });

  test("prefers the first supported entry in the list", () => {
    assert.equal(
      withLanguages(["de", "sv", "en"], () => pickInitialLocale(["en", "sv"])),
      "sv",
    );
  });

  // This fork's guests are in Estonia, so an *ambiguous* visitor —
  // one whose languages we do not speak — reads English rather than
  // Swedish.
  test("defaults to English when no browser language matches", () => {
    assert.equal(DEFAULT_LOCALE, "en");
    assert.equal(
      withLanguages(["de", "fr"], () => pickInitialLocale(["en", "sv", "et"])),
      "en",
    );
  });

  test("defaults to English when there is no navigator at all", () => {
    assert.equal(
      withLanguages(undefined, () => pickInitialLocale(["en", "sv", "et"])),
      "en",
    );
  });

  test("an Estonian phone reads Estonian", () => {
    assert.equal(
      withLanguages(["et-EE", "et"], () => pickInitialLocale(["en", "sv", "et"])),
      "et",
    );
  });

  test("falls back to the first available when English is absent", () => {
    assert.equal(
      withLanguages(["de"], () => pickInitialLocale(["sv", "fr"])),
      "sv",
    );
  });
});

describe("formatting", () => {
  test("formats numbers per locale", () => {
    // Swedish groups with a space rather than a comma.
    const formatted = new I18n(catalogs, "sv").formatNumber(1234567);
    assert.match(formatted, /1[\s  ]234[\s  ]567/);
  });

  test("selects a plural form and interpolates", () => {
    const i18n = new I18n(catalogs, "en");
    const forms = { one: "{{n}} team", other: "{{n}} teams" };
    assert.equal(i18n.plural(1, forms), "1 team");
    assert.equal(i18n.plural(3, forms), "3 teams");
  });

  test("falls back to the other form when a category is missing", () => {
    assert.equal(new I18n(catalogs, "en").plural(1, { other: "{{n}} x" }), "1 x");
  });
});
