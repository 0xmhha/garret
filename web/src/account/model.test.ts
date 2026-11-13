import { describe, expect, it } from "vitest";
import { certificateStatus, invitationStatus, resolveLocale, tokenStatus } from "./model";

const now = new Date("2026-11-13T12:00:00Z");

describe("tokenStatus", () => {
  it.each([
    [{ revoked_at: null }, "active"],
    [{ revoked_at: "2026-11-12T00:00:00Z" }, "revoked"],
  ] as const)("%j -> %s", (tok, want) => expect(tokenStatus(tok)).toBe(want));
});

describe("certificateStatus", () => {
  const base = { revoked_at: null, not_after: "2026-11-20T00:00:00Z", current: false };
  it.each([
    [base, "active"],
    [{ ...base, current: true }, "current"],
    [{ ...base, not_after: "2026-11-13T12:00:00Z" }, "expired"],
    [{ ...base, revoked_at: "2026-11-01T00:00:00Z", current: true }, "revoked"],
    [{ ...base, not_after: "not a date" }, "expired"],
  ] as const)("%j -> %s", (c, want) => expect(certificateStatus(c, now)).toBe(want));
});

describe("invitationStatus", () => {
  const base = { revoked_at: null, used_at: null, expires_at: "2026-11-14T00:00:00Z" };
  it.each([
    [base, "pending"],
    [{ ...base, used_at: "2026-11-13T01:00:00Z" }, "used"],
    [{ ...base, revoked_at: "2026-11-13T01:00:00Z" }, "revoked"],
    [{ ...base, expires_at: "2026-11-13T11:59:59Z" }, "expired"],
    [{ ...base, used_at: "2026-11-12T00:00:00Z", expires_at: "2026-11-12T01:00:00Z" }, "used"],
  ] as const)("%j -> %s", (inv, want) => expect(invitationStatus(inv, now)).toBe(want));
});

describe("resolveLocale", () => {
  it.each([
    ["ko", ["en-US"], "ko"],
    ["en", ["ko-KR"], "en"],
    [null, ["ko-KR"], "ko"],
    ["fr", ["en-GB"], "en"],
    [null, [], "en"],
  ] as const)("stored %s, browser %j -> %s", (stored, langs, want) => expect(resolveLocale(stored, langs)).toBe(want));
});
