// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

it("creates the garret-markdown policy once and returns TrustedHTML from it", async () => {
  const created: string[] = [];
  class Trusted {
    constructor(readonly html: string) {}
    toString() {
      return this.html;
    }
  }
  vi.stubGlobal("trustedTypes", {
    createPolicy: (name: string, rules: { createHTML: (s: string) => string; createScriptURL: (s: string) => string }) => {
      created.push(name);
      return { createHTML: (s: string) => new Trusted(rules.createHTML(s)), createScriptURL: rules.createScriptURL };
    },
    isHTML: (v: unknown) => v instanceof Trusted,
    emptyHTML: new Trusted(""),
  });
  const { renderMarkdown } = await import("./markdown");
  const out = renderMarkdown("**x** <script>alert(1)</script>");
  renderMarkdown("again");
  expect(created).toEqual(["garret-markdown"]);
  expect(out).toBeInstanceOf(Trusted);
  expect(String(out)).toContain("<strong>x</strong>");
  expect(String(out)).not.toContain("<script>");
});
