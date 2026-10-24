import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

// Swedish is the primary market: every string the player can see — this
// app's and @zonventure/play-ui's — needs an entry in sv.json, and in
// this fork in et.json too.
const roots = [new URL(".", import.meta.url).pathname, new URL("../../../packages/play-ui/src/", import.meta.url).pathname];

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(name) && !name.includes(".test.") ? [p] : [];
  });
}

// Strings that go through t() only as a fallback tone name, never shown.
const IGNORED = new Set(["bad", "good", "info"]);

const catalog = (lang: string) => JSON.parse(readFileSync(new URL(`./locales/${lang}.json`, import.meta.url), "utf8")) as Record<string, string>;

function missingIn(lang: string): string[] {
  const cat = catalog(lang);
  const missing = new Set<string>();
  for (const file of roots.flatMap(sources)) {
    const src = readFileSync(file, "utf8");
    // t("…"), and tRef.current("…") where a callback reads the latest t.
    for (const m of src.matchAll(/(?:\bt|\btRef\.current)\(\s*"((?:[^"\\]|\\.)*)"/g)) {
      const key = JSON.parse(`"${m[1]}"`) as string;
      if (!(key in cat) && !IGNORED.has(key)) missing.add(key);
    }
  }
  return [...missing].sort();
}

test("every player string has a Swedish translation", () => {
  assert.deepEqual(missingIn("sv"), []);
});

// This fork's guests are in Estonia: Estonian covers the same strings.
test("every player string has an Estonian translation", () => {
  assert.deepEqual(missingIn("et"), []);
});

test("Estonian keeps every placeholder of its English", () => {
  const names = (s: string) => [...s.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]).sort();
  const en = catalog("en");
  const wrong = Object.entries(catalog("et"))
    .filter(([k, v]) => names(v).join() !== names(k.startsWith("tour|") ? (en[k] ?? k) : k).join())
    .map(([k]) => k);
  assert.deepEqual(wrong, []);
});

test("a walk's words change existing strings, in English, Swedish and Estonian alike", () => {
  const sv = catalog("sv");
  const et = catalog("et");
  const en = catalog("en");
  assert.deepEqual(Object.keys(en).filter((k) => !k.startsWith("tour|")), [], "English holds only a walk's words");
  for (const k of [...Object.keys(en), ...Object.keys(sv), ...Object.keys(et)].filter((k) => k.startsWith("tour|"))) {
    const base = k.slice("tour|".length);
    assert.ok(base in sv, `${k} changes a string the app does not have`);
    assert.ok(k in en, `${k} has no English`);
    assert.ok(k in et, `${k} has no Estonian`);
  }
});
