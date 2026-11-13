import { expect, test } from "@playwright/test";
import { contextFor, signIn, state, violations, watchCSP } from "./state";

// An administrator registers a repository on the server's machine as a
// project (decisions.md D15), and a review opens from its branches without
// any push.
test("a project starts from a repository on the server and a review opens from it", async ({ browser }, info) => {
  const s = state(info);
  const name = `fromsrc-${info.project.name}`;
  const ctx = await contextFor(browser, s, "admin");
  await watchCSP(ctx);
  const page = await ctx.newPage();
  await signIn(page, s, "admin");
  await page.goto(`${s.url}/admin`);
  const form = page.getByRole("textbox", { name: "Repository on this server (optional)" }).locator("xpath=ancestor::form[1]");
  await form.getByRole("textbox", { name: "Name", exact: true }).fill(name);
  await form.getByRole("textbox", { name: "Repository on this server (optional)" }).fill(s.repo);
  await form.getByRole("button", { name: "Check" }).click();
  await expect(page.getByText(/Branches found: .*\bfix\b/)).toBeVisible();
  await form.getByRole("button", { name: "Create project" }).click();
  await expect(page.getByRole("listitem").filter({ hasText: name })).toBeVisible();

  await page.goto(`${s.url}/p/${name}`);
  await expect(page.getByText(s.repo)).toBeVisible();
  await page.getByRole("button", { name: "Sync from source" }).click();
  await expect(page.getByText("Nothing new in the source.")).toBeVisible();
  await page.getByRole("button", { name: "New review" }).click();
  await page.getByLabel("Branch to review").selectOption("refs/heads/fix");
  await page.getByLabel("Into").selectOption("refs/heads/main");
  await page.getByRole("textbox", { name: "Title" }).fill("Imported fix");
  await page.getByRole("button", { name: "Open review" }).click();
  await expect(page.getByRole("heading", { name: /Imported fix/ })).toBeVisible();
  await expect(page.getByRole("link", { name: "Revisions 1", exact: true })).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await ctx.close();
});
