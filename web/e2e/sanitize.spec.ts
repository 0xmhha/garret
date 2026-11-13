import { expect, test } from "@playwright/test";
import { dangerousHref, dangerousLinks, droppedImages, forbiddenElements, markupAttacks } from "../src/markdown/cases";
import { apiAs, contextFor, signIn, state, violations, watchCSP } from "./state";

// The sanitizer cases of src/markdown/markdown.test.ts, rendered by the real
// interface in a real browser: each input is posted as a comment and the
// review page must show it without anything that runs or loads elsewhere.
test("comments render without script, dangerous links or foreign images", async ({ browser }, info) => {
  const s = state(info);
  const marker = `sanitize-${info.project.name}`;
  const inputs = [...markupAttacks.map(([, input]) => input), ...dangerousLinks, ...droppedImages, "<b>not bold</b>"];
  const api = await apiAs(s, "admin");
  try {
    for (const input of inputs) await api.post(`/api/v1/projects/${s.project}/reviews/${s.review}/comments`, { body: `${marker}\n\n${input}` });
  } finally {
    await api.dispose();
  }

  const ctx = await contextFor(browser, s, "admin");
  await watchCSP(ctx);
  const page = await ctx.newPage();
  const dialogs: string[] = [];
  page.on("dialog", (d) => {
    dialogs.push(d.message());
    void d.dismiss();
  });
  await signIn(page, s, "admin");
  await page.goto(`${s.url}/p/${s.project}/r/${s.review}`);
  const bodies = page.locator(".md").filter({ hasText: marker });
  await expect(bodies).toHaveCount(inputs.length);

  expect(await bodies.locator(forbiddenElements).count()).toBe(0);
  const hrefs = await bodies.locator("a").evaluateAll((as) => as.map((a) => a.getAttribute("href") ?? ""));
  for (const href of hrefs) expect(href).not.toMatch(dangerousHref);
  expect(await bodies.locator("img").count()).toBe(0);
  await expect(bodies.filter({ hasText: "<b>not bold</b>" })).toHaveCount(1);
  expect(await bodies.locator("b").count()).toBe(0);
  expect(dialogs).toEqual([]);
  expect(await violations(page)).toEqual([]);
  await ctx.close();
});
