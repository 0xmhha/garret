import { describe, expect, it } from "vitest";
import { eraseHref, hex, zipComplete } from "./model";

// An empty zip is only the end-of-central-directory record.
const eocd = (comment = 0) => {
  const b = new Uint8Array(22 + comment);
  b.set([0x50, 0x4b, 0x05, 0x06]);
  b[20] = comment & 0xff;
  b[21] = comment >> 8;
  return b;
};
const cat = (...parts: Uint8Array[]) => {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
};

describe("zipComplete", () => {
  it.each([
    ["an empty archive", eocd(), true],
    ["entries before the record", cat(new Uint8Array(100).fill(7), eocd()), true],
    ["a record with a comment", cat(new Uint8Array(10), eocd(5)), true],
    ["a cut-off archive", new Uint8Array(100).fill(7), false],
    ["the record cut short", eocd().slice(0, 20), false],
    ["a comment longer than what follows", cat(new Uint8Array(10), eocd(5).slice(0, 24)), false],
    ["nothing", new Uint8Array(0), false],
  ])("%s -> %s", (_, bytes, want) => expect(zipComplete(bytes)).toBe(want));
});

describe("hex", () => {
  it("formats bytes as lowercase hex", () => expect(hex(new Uint8Array([0, 15, 255]).buffer)).toBe("000fff"));
});

describe("eraseHref", () => {
  it("encodes the ID", () => expect(eraseHref("collab_document", "a/b c")).toBe("/admin?erase=collab_document&id=a%2Fb%20c#erase"));
});
