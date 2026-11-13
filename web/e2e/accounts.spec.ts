import { expect, test } from "@playwright/test";
import { contextFor, state, signIn, violations, watchCSP } from "./state";

// While client certificates are off, a member joins without any program: the
// administrator creates the account, the member signs in with the temporary
// password and changes it (security.md, Turning client certificates off).
test("an administrator creates an account and the member changes its password", async ({ browser }, info) => {
  const s = state(info);
  test.skip(s.client_certs, "accounts are created directly only while client certificates are off");
  const adminCtx = await contextFor(browser, s, "admin");
  await watchCSP(adminCtx);
  const admin = await adminCtx.newPage();
  await signIn(admin, s, "admin");
  await admin.goto(`${s.url}/admin`);
  const form = admin.getByRole("form", { name: "Create account" });
  await form.getByRole("textbox", { name: "Username" }).fill("dave");
  await form.getByRole("textbox", { name: "Display name" }).fill("Dave");
  await form.getByRole("button", { name: "Create account" }).click();
  const temp = (await admin.locator(".onetime__value").innerText()).trim();
  expect(temp.split("-")).toHaveLength(6);
  expect(await violations(admin)).toEqual([]);

  const daveCtx = await browser.newContext();
  await watchCSP(daveCtx);
  const dave = await daveCtx.newPage();
  await dave.goto(`${s.url}/login`);
  await dave.getByLabel("Username").fill("dave");
  await dave.getByLabel("Password").fill(temp);
  await dave.getByRole("button", { name: "Sign in" }).click();
  await expect(dave.getByRole("link", { name: "Signed in as Dave" })).toBeVisible();
  await dave.goto(`${s.url}/account`);
  const next = "a much longer passphrase for dave";
  await dave.getByLabel("Current password").fill(temp);
  await dave.getByLabel("New password", { exact: true }).fill(next);
  await dave.getByLabel("New password again").fill(next);
  await dave.getByRole("button", { name: "Change password" }).click();
  await expect(dave.getByText("Your password was changed.")).toBeVisible();
  await dave.getByLabel("Username").fill("dave");
  await dave.getByLabel("Password").fill(next);
  await dave.getByRole("button", { name: "Sign in" }).click();
  await expect(dave.getByRole("link", { name: "Signed in as Dave" })).toBeVisible();
  expect(await violations(dave)).toEqual([]);
  await adminCtx.close();
  await daveCtx.close();
});
