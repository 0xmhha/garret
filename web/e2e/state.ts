import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { expect, request, type APIRequestContext, type Browser, type BrowserContext, type Page, type TestInfo } from "@playwright/test";
import { stateEnv, type Mode } from "./setup";

export type Who = "admin" | "bob" | "carol";

interface User {
  username: string;
  password: string;
  cert: string;
  key: string;
}

/** State is what the Go fixture (cmd/garret/browser_fixture_test.go) wrote. */
export interface State {
  url: string;
  client_certs: boolean;
  project: string;
  review: number;
  users: Record<Who, User>;
  repo: string;
  home: string;
}

/** state reads the fixture of the project's client mode (playwright.config.ts metadata). */
export function state(info: TestInfo): State {
  const mode = info.project.metadata["clientCerts"] as Mode;
  const path = process.env[stateEnv(mode)];
  if (!path) throw new Error(`${stateEnv(mode)} is not set; run the suite with playwright test`);
  return JSON.parse(readFileSync(path, "utf8")) as State;
}

/** contextFor opens a browser context for who, presenting their client certificate when the server asks for one. */
export function contextFor(browser: Browser, s: State, who: Who): Promise<BrowserContext> {
  const u = s.users[who];
  return browser.newContext(s.client_certs ? { clientCertificates: [{ origin: s.url, certPath: u.cert, keyPath: u.key }] } : {});
}

/** signIn opens the sign-in page and signs in as who. */
export async function signIn(page: Page, s: State, who: Who): Promise<void> {
  const u = s.users[who];
  await page.goto(`${s.url}/login`);
  await page.getByLabel("Username").fill(u.username);
  await page.getByLabel("Password").fill(u.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("link", { name: "garret" })).toBeVisible();
}

/** git runs git in bob's clone, configured by the fixture to push as bob. */
export function git(s: State, ...args: string[]): string {
  return execFileSync("git", args, {
    cwd: s.repo,
    env: { HOME: s.home, PATH: process.env["PATH"] ?? "", GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null", GIT_TERMINAL_PROMPT: "0", LANG: "C" },
    encoding: "utf8",
  });
}

/** cspViolations records the page's Content-Security-Policy violations. */
export async function watchCSP(context: BrowserContext): Promise<void> {
  await context.addInitScript(() => {
    (window as unknown as { __csp: string[] }).__csp = [];
    document.addEventListener("securitypolicyviolation", (e) => (window as unknown as { __csp: string[] }).__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
}

export function violations(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { __csp: string[] }).__csp);
}

/** Api is a signed-in API session; post sends the CSRF token. */
export interface Api {
  ctx: APIRequestContext;
  post(path: string, data: unknown): Promise<unknown>;
  dispose(): Promise<void>;
}

/** apiAs signs who in through the API, presenting the client certificate when needed. */
export async function apiAs(st: State, who: Who): Promise<Api> {
  const u = st.users[who];
  const ctx = await request.newContext({
    baseURL: st.url,
    ignoreHTTPSErrors: true,
    ...(st.client_certs ? { clientCertificates: [{ origin: st.url, certPath: u.cert, keyPath: u.key }] } : {}),
    extraHTTPHeaders: { Origin: st.url },
  });
  const login = await ctx.post("/api/v1/auth/login", { data: { username: u.username, password: u.password } });
  expect(login.status()).toBe(200);
  const { csrf_token: csrf } = (await login.json()) as { csrf_token: string };
  return {
    ctx,
    async post(path, data) {
      const res = await ctx.post(path, { headers: { "X-CSRF-Token": csrf }, data });
      expect(res.status(), `POST ${path}: ${await res.text()}`).toBeLessThan(300);
      return res.status() === 204 ? undefined : res.json();
    },
    dispose: () => ctx.dispose(),
  };
}
