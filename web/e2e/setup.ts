import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const startTimeout = 120_000;
const stopTimeout = 30_000;

/** The two client modes of docs/design/testing.md; each gets its own server. */
export const modes = ["on", "off"] as const;
export type Mode = (typeof modes)[number];

/** stateEnv names the variable that holds the state file of mode's server. */
export const stateEnv = (mode: Mode) => `GARRET_E2E_STATE_${mode.toUpperCase()}`;

/**
 * globalSetup builds the Go fixture with the embedded interface, starts one
 * server per client mode, and waits for the state files they write once
 * steps 1 to 3 are done. The returned teardown closes the fixtures'
 * standard input, which stops them.
 */
export default async function globalSetup(): Promise<() => Promise<void>> {
  const dir = mkdtempSync(join(tmpdir(), "garret-e2e-"));
  const bin = join(dir, "fixture.test");
  const children: ChildProcess[] = [];
  const stop = async () => {
    for (const c of children) c.stdin?.end();
    await Promise.all(children.map(exited));
    rmSync(dir, { recursive: true, force: true });
  };
  try {
    execFileSync("go", ["test", "-c", "-tags", "webui", "-o", bin, "./cmd/garret"], { cwd: repoRoot, stdio: "inherit" });
    await Promise.all(
      modes.map(async (mode) => {
        const state = join(dir, `state-${mode}.json`);
        const child = spawn(bin, ["-test.run", "^TestBrowserFixture$", "-test.timeout", "60m"], {
          cwd: join(repoRoot, "cmd", "garret"),
          env: { ...process.env, GARRET_E2E_STATE: state, GARRET_E2E_CLIENT_CERTS: mode },
          stdio: ["pipe", "inherit", "inherit"],
        });
        children.push(child);
        await waitForFile(state, child);
        process.env[stateEnv(mode)] = state;
      }),
    );
  } catch (err) {
    for (const c of children) c.kill();
    await stop();
    throw err;
  }
  return stop;
}

function waitForFile(path: string, child: ChildProcess): Promise<void> {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + startTimeout;
    const onExit = (code: number | null) => reject(new Error(`the fixture exited (${code}) before it was ready`));
    child.once("exit", onExit);
    const poll = () => {
      if (existsSync(path)) {
        child.off("exit", onExit);
        resolve();
      } else if (Date.now() > deadline) {
        child.off("exit", onExit);
        reject(new Error("the fixture was not ready in time"));
      } else {
        setTimeout(poll, 200);
      }
    };
    poll();
  });
}

function exited(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      child.kill();
      resolve();
    }, stopTimeout);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}
