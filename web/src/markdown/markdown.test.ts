// @vitest-environment jsdom
// DOMPurify does not sanitize under happy-dom (it reports support but keeps
// <script> and javascript: links), so the sanitizer is tested in jsdom, the
// environment DOMPurify's own tests use, and again in real browsers by the
// Playwright suite.
import { describe, expect, it } from "vitest";
import { renderMarkdown } from "./markdown";

function dom(html: string): HTMLElement {
  const div = document.createElement("div");
  div.innerHTML = html;
  return div;
}

describe("renderMarkdown keeps Markdown", () => {
  it("renders emphasis, code, lists and quotes", () => {
    const d = dom(renderMarkdown("**bold** _it_ `x < y`\n\n- one\n- two\n\n> quoted"));
    expect(d.querySelector("strong")?.textContent).toBe("bold");
    expect(d.querySelector("em")?.textContent).toBe("it");
    expect(d.querySelector("code")?.textContent).toBe("x < y");
    expect(d.querySelectorAll("li")).toHaveLength(2);
    expect(d.querySelector("blockquote")).not.toBeNull();
  });

  it("renders fenced code verbatim, including markup", () => {
    const d = dom(renderMarkdown("```c\nif (a < b) { puts(\"<script>\"); }\n```"));
    expect(d.querySelector("pre code")?.textContent).toBe('if (a < b) { puts("<script>"); }\n');
    expect(d.querySelector("script")).toBeNull();
  });

  it("renders GFM tables", () => {
    const d = dom(renderMarkdown("| a | b |\n|---|---|\n| 1 | 2 |"));
    expect(d.querySelectorAll("td")).toHaveLength(2);
  });

  it("gives links rel noopener noreferrer", () => {
    const a = dom(renderMarkdown("[advisory](https://example.org/a)")).querySelector("a");
    expect(a?.getAttribute("href")).toBe("https://example.org/a");
    expect(a?.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("keeps images that point at garret attachments", () => {
    const img = dom(renderMarkdown("![log](/api/v1/attachments/01JD0000000000000000000ATT)")).querySelector("img");
    expect(img?.getAttribute("src")).toBe("/api/v1/attachments/01JD0000000000000000000ATT");
  });
});

describe("renderMarkdown removes what could run or leak", () => {
  it.each([
    ["script tag", "<script>alert(1)</script>"],
    ["event attribute", '<img src=x onerror="alert(1)">'],
    ["iframe", '<iframe src="https://evil.example"></iframe>'],
    ["style attribute", '<p style="background:url(https://evil.example)">x</p>'],
    ["svg", "<svg><script>alert(1)</script></svg>"],
    ["form", '<form action="https://evil.example"><input name=a></form>'],
  ])("%s", (_name, input) => {
    const html = renderMarkdown(input);
    const d = dom(html);
    expect(d.querySelector("script, iframe, svg, form, input, [onerror], [style]")).toBeNull();
    expect(html).not.toMatch(/<(script|iframe|svg|form)/i);
  });

  it("shows raw HTML as text rather than markup", () => {
    expect(dom(renderMarkdown("<b>not bold</b>")).querySelector("b")).toBeNull();
    expect(dom(renderMarkdown("<b>not bold</b>")).textContent).toContain("<b>not bold</b>");
  });

  it.each([
    "[x](javascript:alert(1))",
    "[x](JAVASCRIPT:alert(1))",
    "[x](data:text/html;base64,PHNjcmlwdD4=)",
    "[x](vbscript:msgbox)",
  ])("drops the dangerous link in %s", (input) => {
    const a = dom(renderMarkdown(input)).querySelector("a");
    expect(a?.getAttribute("href") ?? "").not.toMatch(/^(javascript|data|vbscript):/i);
  });

  it.each([
    "![x](https://evil.example/pixel.png)",
    "![x](//evil.example/pixel.png)",
    "![x](data:image/png;base64,AAAA)",
    "![x](/api/v1/me)",
  ])("drops the image in %s", (input) => {
    expect(dom(renderMarkdown(input)).querySelector("img")).toBeNull();
  });
});
