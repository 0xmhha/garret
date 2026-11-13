import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { expect, it } from "vitest";

// Only the Markdown module and its tests may write HTML into the page
// (docs/specs/web.md 5). Everything else renders through React, which
// escapes text.
const allowed = new Set(["markdown/markdown.ts", "markdown/markdown.test.ts", "markdown/guard.test.ts", "ui/Markdown.tsx"]);
const writesHTML = /\b(innerHTML|outerHTML|insertAdjacentHTML|dangerouslySetInnerHTML)\b|document\.write\(/;

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? sources(join(dir, e.name)) : /\.(ts|tsx)$/.test(e.name) ? [join(dir, e.name)] : [],
  );
}

it("writes HTML only through renderMarkdown", () => {
  const root = join(import.meta.dirname, "..");
  const offenders = sources(root)
    .map((f) => relative(root, f))
    .filter((f) => !allowed.has(f) && writesHTML.test(readFileSync(join(root, f), "utf8")));
  expect(offenders).toEqual([]);
});
