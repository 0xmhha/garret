import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";
import en from "./en.json";

// docs/specs/web.md 7: every key in the catalogs is used. A key counts as
// used when application code names it in a string, or when it starts with
// a prefix that code completes at run time, as in t(`status.${status}`).
const srcDir = join(__dirname, "..");

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

it("uses every catalog key", () => {
  const code = sources(srcDir).map((p) => readFileSync(p, "utf8")).join("\n");
  const prefixes = [...code.matchAll(/`([a-z][\w.]*\.)\$\{/g)].map((m) => m[1]!);
  const unused = Object.keys(en).filter((k) => !code.includes(`"${k}"`) && !prefixes.some((p) => k.startsWith(p)));
  expect(unused).toEqual([]);
});
