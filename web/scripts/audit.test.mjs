import assert from "node:assert/strict";
import { test } from "node:test";

import { problems } from "./audit.mjs";

const id = "GHSA-aaaa-bbbb-cccc";
const day = 24 * 60 * 60 * 1000;
const now = new Date("2026-11-18T00:00:00Z");

function report(...advisories) {
  const vulnerabilities = {};
  for (const [name, ghsa, severity = "high"] of advisories) {
    vulnerabilities[name] = {
      name,
      severity,
      via: [{ name, severity, url: `https://github.com/advisories/${ghsa}` }],
    };
  }
  // A package that is affected only through another one carries names.
  vulnerabilities.parent = { name: "parent", severity: "high", via: ["glob"] };
  return { vulnerabilities };
}

const exception = { id, package: "glob", reason: "no fix yet", added: "2026-11-18", expires: "2026-12-02" };

function input(over = {}) {
  return {
    full: report(["glob", id]),
    prod: report(),
    exceptions: [exception],
    installed: { glob: "4.0.7" },
    times: { glob: { "4.0.7": "2026-08-24T00:00:00Z" } },
    now,
    ...over,
  };
}

test("an excepted advisory passes", () => {
  assert.deepEqual(problems(input()), []);
});

test("an advisory without an exception fails", () => {
  const got = problems(input({ exceptions: [] }));
  assert.equal(got.length, 1);
  assert.match(got[0], /GHSA-aaaa-bbbb-cccc/);
});

test("moderate and low advisories are ignored", () => {
  assert.deepEqual(problems(input({ full: report(["glob", id, "moderate"]), exceptions: [] })), []);
});

test("critical advisories need an exception too", () => {
  assert.equal(problems(input({ full: report(["glob", id, "critical"]), exceptions: [] })).length, 1);
});

test("an expired exception fails", () => {
  const got = problems(input({ now: new Date("2026-12-03T00:00:00Z") }));
  assert.match(got.join("\n"), /expired/);
});

test("an exception longer than 30 days fails", () => {
  const got = problems(input({ exceptions: [{ ...exception, expires: "2027-01-01" }] }));
  assert.match(got.join("\n"), /30 days/);
});

test("an exception without a reason fails", () => {
  const got = problems(input({ exceptions: [{ ...exception, reason: "" }] }));
  assert.match(got.join("\n"), /reason/);
});

test("an advisory in a production dependency cannot be excepted", () => {
  const got = problems(input({ prod: report(["glob", id]) }));
  assert.match(got.join("\n"), /production/);
});

test("a newer version at least 7 days old ends the exception", () => {
  const times = { glob: { "4.0.7": "2026-08-24T00:00:00Z", "4.0.8": new Date(now - 8 * day).toISOString() } };
  assert.match(problems(input({ times })).join("\n"), /4\.0\.8/);
});

test("a newer version younger than 7 days does not end it yet", () => {
  const times = { glob: { "4.0.7": "2026-08-24T00:00:00Z", "4.0.8": new Date(now - 2 * day).toISOString() } };
  assert.deepEqual(problems(input({ times })), []);
});

test("prereleases and npm's bookkeeping keys are not versions", () => {
  const times = {
    glob: {
      created: "2014-01-01T00:00:00Z",
      modified: new Date(now - 30 * day).toISOString(),
      "4.0.7": "2026-08-24T00:00:00Z",
      "5.0.0-beta.1": new Date(now - 20 * day).toISOString(),
    },
  };
  assert.deepEqual(problems(input({ times })), []);
});

test("an unused exception fails so it gets removed", () => {
  const got = problems(input({ full: report() }));
  assert.match(got.join("\n"), /no longer/);
});
