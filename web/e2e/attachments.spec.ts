import { expect, test } from "@playwright/test";
import { contextFor, signIn, state, violations, watchCSP } from "./state";

// A 1x1 PNG: small enough to inline, real enough for the browser to decode.
const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");

test("a comment carries an image shown inline and a log offered as a download", async ({ browser }, info) => {
  const s = state(info);
  const ctx = await contextFor(browser, s, "carol");
  await watchCSP(ctx);
  const page = await ctx.newPage();
  await signIn(page, s, "carol");
  await page.goto(`${s.url}/p/${s.project}/r/${s.review}`);
  const box = page.locator("form.composer").last();
  await box.locator('input[type="file"]').setInputFiles([
    { name: `shot-${info.project.name}.png`, mimeType: "image/png", buffer: png },
    { name: "crash.log", mimeType: "text/plain", buffer: Buffer.from("panic: index out of range\n") },
  ]);
  await expect(box.getByRole("list", { name: "Files to attach" }).getByRole("listitem")).toHaveCount(2);
  await expect(box.getByText("Uploading…")).toHaveCount(0);
  await box.locator("textarea").fill(`Proof attached (${info.project.name}).`);
  await box.getByRole("button", { name: "Comment", exact: true }).click();

  const thread = page.getByRole("article").filter({ hasText: `Proof attached (${info.project.name}).` });
  const img = thread.getByRole("img", { name: `shot-${info.project.name}.png` });
  await expect(img).toBeVisible();
  await expect.poll(() => img.evaluate((el) => (el as HTMLImageElement).naturalWidth)).toBe(1);
  const link = thread.getByRole("link", { name: /crash\.log/ });
  // The server's Content-Disposition makes this a download in every browser.
  const download = page.waitForEvent("download");
  await link.click();
  expect((await download).suggestedFilename()).toBe("crash.log");
  expect(await violations(page)).toEqual([]);
  await ctx.close();
});
