import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

// Every string the console shows needs Swedish: from the classic
// console's catalog, an area file under locales/sv/, or sv.json — the
// same three layers i18n.tsx merges.
const src = new URL(".", import.meta.url).pathname;

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return name === "locales" ? [] : sources(p);
    return /\.tsx?$/.test(name) && !name.includes(".test.") ? [p] : [];
  });
}

function catalog(): Record<string, string> {
  const read = (p: string) => JSON.parse(readFileSync(p, "utf8")) as Record<string, string>;
  const areas = join(src, "locales/sv");
  return Object.assign(
    read(new URL("../../../../internal/server/web/locales/web/sv.json", import.meta.url).pathname),
    ...readdirSync(areas).filter((f) => f.endsWith(".json")).map((f) => read(join(areas, f))),
    read(join(src, "locales/sv.json")),
  );
}

test("every console string has a Swedish translation", () => {
  const sv = catalog();
  const missing = new Set<string>();
  for (const file of sources(src)) {
    for (const m of readFileSync(file, "utf8").matchAll(/\bt\.t\(\s*"((?:[^"\\]|\\.)*)"/g)) {
      const key = JSON.parse(`"${m[1]}"`) as string;
      if (!(key in sv)) missing.add(key);
    }
  }
  assert.deepEqual([...missing].sort(), []);
});

// Words the console writes into an Estonian adventure — default team and
// player names, route labels — are what players read, so this fork has
// them in Estonian although the console itself is not.
test("every content word has an Estonian translation", () => {
  const read = (p: string) => JSON.parse(readFileSync(join(src, p), "utf8")) as Record<string, string>;
  const et = read("locales/content/et.json");
  const words = new Set(Object.keys(read("locales/content/sv.json")));
  for (const file of sources(src)) {
    for (const m of readFileSync(file, "utf8").matchAll(/(?:\bct|contentT\([^)]*\))\(\s*"((?:[^"\\]|\\.)*)"/g)) {
      words.add(JSON.parse(`"${m[1]}"`) as string);
    }
  }
  assert.deepEqual([...words].filter((w) => !(w in et)).sort(), []);
});

// The designer's simulated phone borrows the player app's catalogs, so an
// adventure in any language the app speaks reads there as on a real phone.
test("the simulated phone speaks every language of the player app", () => {
  const sim = readFileSync(join(src, "designer/Simulator.tsx"), "utf8");
  const phone = /const PHONE_CATALOGS = \{([^}]*)\}/.exec(sim)?.[1] ?? "";
  const langs = readdirSync(new URL("../../play/src/locales/", import.meta.url).pathname)
    .filter((f) => f.endsWith(".json"))
    .map((f) => f.slice(0, -".json".length));
  assert.deepEqual(langs.filter((l) => !new RegExp(`\\b${l}:`).test(phone)), []);
});

// What the simulated phone shows speaks the phone's language, not the
// console's: the simulator's own words on it — the line in the place
// card before the team is on the map — included.
test("the simulated phone's place note speaks the phone's language", () => {
  const sim = readFileSync(join(src, "designer/Simulator.tsx"), "utf8");
  const note = /const placeNote = [\s\S]*?\n  };/.exec(sim)?.[0] ?? "";
  assert.ok(note.includes("tr("), "placeNote not found");
  assert.ok(!/\bt\.t\(/.test(note), "placeNote uses the console's language");
});

test("every word a simulated phone says has every phone language", () => {
  const sim = ["designer/Simulator.tsx", "designer/SamplePreview.tsx"].map((f) => readFileSync(join(src, f), "utf8")).join("\n");
  const play = new URL("../../play/src/locales/", import.meta.url).pathname;
  const read = (p: string) => (statSync(p, { throwIfNoEntry: false }) ? (JSON.parse(readFileSync(p, "utf8")) as Record<string, string>) : {});
  const keys = [...sim.matchAll(/\btr\(\s*"((?:[^"\\]|\\.)*)"/g)].map((m) => JSON.parse(`"${m[1]}"`) as string);
  const missing: string[] = [];
  for (const lang of ["sv", "et"]) {
    const words = { ...read(join(play, `${lang}.json`)), ...read(join(src, `locales/phone/${lang}.json`)) };
    for (const k of keys) if (!(k in words)) missing.push(`${lang}: ${k}`);
  }
  assert.deepEqual(missing, []);
});
