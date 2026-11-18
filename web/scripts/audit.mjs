// audit.mjs runs npm audit and fails on high or critical advisories, except
// those listed in audit-exceptions.json (docs/specs/web.md). It uses Node
// only, so the check itself adds no dependency.
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const failing = new Set(["high", "critical"]);
const day = 24 * 60 * 60 * 1000;
const maxExceptionDays = 30;
const cooldownDays = 7;
const npmTimeoutMs = 120_000;
const ghsa = /GHSA-[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}/;
const release = /^\d+\.\d+\.\d+$/;

/**
 * advisories returns the failing advisories of an npm audit report as a map
 * from advisory ID to the package it is in. Packages affected only through
 * another package carry names instead of advisories and are covered by it.
 */
function advisories(report) {
  const out = new Map();
  for (const vuln of Object.values(report.vulnerabilities ?? {})) {
    for (const via of vuln.via) {
      if (typeof via !== "object" || !failing.has(via.severity)) continue;
      const id = via.url?.match(ghsa)?.[0] ?? `npm-${via.source}`;
      out.set(id, via.name);
    }
  }
  return out;
}

/**
 * newerRelease returns a release of the package published after the
 * installed one and at least the cooldown ago, or undefined. times is the
 * output of npm view <package> time.
 */
function newerRelease(times, installed, now) {
  const since = Date.parse(times?.[installed] ?? "");
  for (const [version, at] of Object.entries(times ?? {})) {
    const published = Date.parse(at);
    if (release.test(version) && published > since && now - published >= cooldownDays * day) {
      return version;
    }
  }
  return undefined;
}

/**
 * problems returns why the audit fails, or an empty list. full and prod are
 * npm audit reports with and without development dependencies, installed
 * maps each excepted package to its installed version, and times maps it
 * to its npm view time output.
 */
export function problems({ full, prod, exceptions, installed, times, now }) {
  const out = [];
  const found = advisories(full);
  const inProd = advisories(prod);
  const excepted = new Map(exceptions.map((e) => [e.id, e]));

  for (const [id, pkg] of found) {
    if (!excepted.has(id)) out.push(`${id} in ${pkg} has no exception`);
  }
  for (const e of exceptions) {
    const added = Date.parse(e.added);
    const expires = Date.parse(e.expires);
    if (!e.reason?.trim()) out.push(`${e.id}: the exception gives no reason`);
    if (!(expires - added <= maxExceptionDays * day)) {
      out.push(`${e.id}: an exception may last at most ${maxExceptionDays} days`);
    }
    if (now > expires) out.push(`${e.id}: the exception expired on ${e.expires}; upgrade ${e.package}`);
    if (!found.has(e.id)) out.push(`${e.id}: no longer reported; remove the exception`);
    if (inProd.has(e.id)) out.push(`${e.id}: in a production dependency, which cannot be excepted`);
    const newer = newerRelease(times[e.package], installed[e.package], now);
    if (newer) out.push(`${e.id}: ${e.package} ${newer} is out; upgrade and remove the exception`);
  }
  return out;
}

function npmJSON(args, cwd) {
  const r = spawnSync("npm", args, { cwd, encoding: "utf8", timeout: npmTimeoutMs, maxBuffer: 64 << 20 });
  if (r.error) throw r.error;
  try {
    return JSON.parse(r.stdout);
  } catch {
    throw new Error(`npm ${args.join(" ")} printed no JSON: ${r.stderr.trim()}`);
  }
}

function main() {
  const web = join(dirname(fileURLToPath(import.meta.url)), "..");
  const exceptions = JSON.parse(readFileSync(join(web, "audit-exceptions.json"), "utf8"));
  const installed = {};
  const times = {};
  for (const e of exceptions) {
    installed[e.package] = JSON.parse(readFileSync(join(web, "node_modules", e.package, "package.json"), "utf8")).version;
    times[e.package] = npmJSON(["view", e.package, "time", "--json"], web);
  }
  const found = problems({
    full: npmJSON(["audit", "--json"], web),
    prod: npmJSON(["audit", "--omit=dev", "--json"], web),
    exceptions,
    installed,
    times,
    now: Date.now(),
  });
  for (const p of found) console.error(`web-audit: ${p}`);
  if (found.length > 0) process.exit(1);
  for (const e of exceptions) console.log(`web-audit: ${e.id} in ${e.package} excepted until ${e.expires}: ${e.reason}`);
  console.log("web-audit: no other high or critical advisory");
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
