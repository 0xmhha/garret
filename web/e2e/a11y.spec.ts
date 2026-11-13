import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import { apiAs, contextFor, signIn, state, violations, watchCSP, type State } from "./state";

// Every screen of docs/specs/web.md 6 is checked against WCAG 2.2 AA with
// axe, signed in as the administrator so the administration screen is
// reachable. The sign-in page is checked before signing in.
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

async function check(page: Page, what: string) {
  const results = await new AxeBuilder({ page }).withTags(tags).analyze();
  const found = results.violations.map((v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
  expect(found, `axe on ${what}`).toEqual([]);
  // A run that checked nothing would also find nothing.
  expect(results.passes.length, `axe rules that ran on ${what}`).toBeGreaterThan(10);
  expect(await violations(page), `CSP on ${what}`).toEqual([]);
}

let s: State;
let discussion = "";

test.beforeAll(async ({}, info) => {
  s = state(info);
  const api = await apiAs(s, "admin");
  try {
    const d = (await api.post(`/api/v1/projects/${s.project}/discussions`, { title: `Disclosure plan (${info.project.name})` })) as { id: string };
    discussion = d.id;
  } finally {
    await api.dispose();
  }
});

test("every screen meets WCAG 2.2 AA", async ({ browser }) => {
  const ctx = await contextFor(browser, s, "admin");
  await watchCSP(ctx);
  const page = await ctx.newPage();
  await page.goto(`${s.url}/login`);
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  await check(page, "sign-in");
  await signIn(page, s, "admin");

  const review = `/p/${s.project}/r/${s.review}`;
  const screens: [string, string, string][] = [
    ["projects", "/", "Projects"],
    ["inbox", "/inbox", "Inbox"],
    ["project", `/p/${s.project}`, s.project],
    ["discussion", `/p/${s.project}/d/${discussion}`, "Disclosure plan"],
    ["review", review, "Bound the length field"],
    ["revisions", `${review}/revisions`, "Revision 1"],
    ["files", `${review}/files`, "src/len.c"],
    ["account", "/account", "Account"],
    ["administration", "/admin", "Administration"],
  ];
  for (const [what, path, heading] of screens) {
    await page.goto(`${s.url}${path}`);
    await expect(page.getByRole("heading", { name: heading }).first()).toBeVisible();
    await check(page, what);
  }
  await ctx.close();
});
