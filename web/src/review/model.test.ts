import { describe, expect, it } from "vitest";
import { buildThreads, fileThreads, reviewedStore, type Comment } from "./model";
import { memoryStorage } from "../test/render";

const c = (over: Partial<Comment>): Comment => ({
  id: "c", thread_root_id: "c", author_id: "u1", anchor: null, resolved: false, version: 1, body: "x",
  deleted: false, attachments: [], created_at: "2026-11-13T00:00:00Z", ...over,
});

describe("buildThreads", () => {
  it("groups replies under their root in time order and keeps roots in time order", () => {
    const threads = buildThreads([
      c({ id: "r2", thread_root_id: "r2", created_at: "2026-11-13T02:00:00Z" }),
      c({ id: "a2", thread_root_id: "r1", created_at: "2026-11-13T03:00:00Z" }),
      c({ id: "r1", thread_root_id: "r1", created_at: "2026-11-13T01:00:00Z" }),
      c({ id: "a1", thread_root_id: "r1", created_at: "2026-11-13T02:30:00Z" }),
    ]);
    expect(threads.map((t) => [t.root.id, t.replies.map((r) => r.id)])).toEqual([["r1", ["a1", "a2"]], ["r2", []]]);
  });

  it("treats an empty thread_root_id as a root, as the server sends it", () => {
    const threads = buildThreads([c({ id: "r", thread_root_id: "" }), c({ id: "a", thread_root_id: "r", created_at: "2026-11-13T01:00:00Z" })]);
    expect(threads.map((t) => [t.root.id, t.replies.map((x) => x.id)])).toEqual([["r", ["a"]]]);
  });

  it("drops replies whose root is missing rather than showing them alone", () => {
    expect(buildThreads([c({ id: "a", thread_root_id: "gone" })])).toEqual([]);
  });
});

describe("fileThreads", () => {
  const anchor = { revision: 1, view: "revision", from: "a", to: "b", path: "src/parse.c", side: "new", start_line: 10, end_line: 12 };
  const threads = buildThreads([
    c({ id: "t1", thread_root_id: "t1", anchor }),
    c({ id: "t2", thread_root_id: "t2", anchor, ported: { path: "src/parse.c", side: "new", start_line: 14, end_line: 16, file_level: false, dropped: false } }),
    c({ id: "t3", thread_root_id: "t3", anchor, ported: { path: "src/parse.c", side: "new", start_line: 0, end_line: 0, file_level: true, dropped: false } }),
    c({ id: "t4", thread_root_id: "t4", anchor, ported: { path: "src/parse.c", side: "new", start_line: 0, end_line: 0, file_level: false, dropped: true } }),
    c({ id: "t5", thread_root_id: "t5", anchor: { ...anchor, path: "src/other.c" } }),
    c({ id: "t6", thread_root_id: "t6" }),
  ]);

  it("places threads on the line they end at, using the ported position when present", () => {
    const f = fileThreads(threads, "src/parse.c");
    expect([...f.byLine.entries()].map(([k, v]) => [k, v.map((t) => t.root.id)])).toEqual([["new:12", ["t1"]], ["new:16", ["t2"]]]);
    expect(f.fileLevel.map((t) => t.root.id)).toEqual(["t3"]);
  });

  it("leaves out threads whose lines no longer exist and threads on other files", () => {
    const ids = [...fileThreads(threads, "src/parse.c").byLine.values()].flat().map((t) => t.root.id);
    expect(ids).not.toContain("t4");
    expect(ids).not.toContain("t5");
    expect(ids).not.toContain("t6");
  });
});

describe("reviewedStore", () => {
  it("remembers reviewed files per review revision and path", () => {
    const s = reviewedStore(memoryStorage(), "libfoo", 12, 3);
    expect(s.has("src/parse.c")).toBe(false);
    s.set("src/parse.c", true);
    expect(s.has("src/parse.c")).toBe(true);
    expect(reviewedStore(s.storage, "libfoo", 12, 4).has("src/parse.c")).toBe(false);
    s.set("src/parse.c", false);
    expect(s.has("src/parse.c")).toBe(false);
  });

  it("survives a storage that throws", () => {
    const broken = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); }, removeItem: () => {} };
    const s = reviewedStore(broken, "p", 1, 1);
    expect(() => s.set("a", true)).not.toThrow();
    expect(s.has("a")).toBe(false);
  });
});
