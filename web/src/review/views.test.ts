import { describe, expect, it } from "vitest";
import type { Comment, Revision, Thread } from "./model";
import { anchorView, parseView, viewKey, viewQuery, viewThreads, type DiffView } from "./views";

const commit = (sha: string, subject = sha) => ({ sha, subject, author_name: "Bob", authored_at: "2026-11-13T00:00:00Z" });
const rev = (number: number, base: string, shas: string[]): Revision => ({
  number,
  base_sha: base,
  head_sha: shas.at(-1) ?? base,
  commits_omitted: false,
  pushed_by: "u",
  pushed_at: "2026-11-13T00:00:00Z",
  commits: shas.map((s) => commit(s)),
});
const r1 = rev(1, "b0", ["c1", "c2", "c3"]);
const r2 = rev(2, "b0", ["d1", "d2"]);
const params = (q: string) => new URLSearchParams(q);

describe("parseView", () => {
  it.each<[string, Revision, DiffView]>([
    ["", r1, { kind: "revision" }],
    ["view=commit&commit=c2", r1, { kind: "commit", sha: "c2" }],
    ["view=commit&commit=zz", r1, { kind: "revision" }],
    ["view=range&first=c2&last=c3", r1, { kind: "range", first: "c2", last: "c3" }],
    ["view=range&first=c1&last=c1", r1, { kind: "range", first: "c1", last: "c1" }],
    ["view=range&first=c3&last=c1", r1, { kind: "revision" }],
    ["view=range&first=c1", r1, { kind: "revision" }],
    ["view=pair&base=1", r2, { kind: "pair", base: 1 }],
    ["view=pair&base=2", r2, { kind: "revision" }],
    ["view=pair&base=3", r2, { kind: "revision" }],
    ["view=pair&base=x", r2, { kind: "revision" }],
    ["view=other", r1, { kind: "revision" }],
  ])("%s", (q, r, want) => expect(parseView(params(q), r, [r1, r2])).toEqual(want));
});

describe("viewQuery", () => {
  it.each<[DiffView, Revision, string]>([
    [{ kind: "revision" }, r1, "rev=1"],
    [{ kind: "commit", sha: "c2" }, r1, "commit=c2"],
    [{ kind: "range", first: "c2", last: "c3" }, r1, "from=c1&to=c3"],
    [{ kind: "range", first: "c1", last: "c2" }, r1, "from=b0&to=c2"],
    [{ kind: "pair", base: 1 }, r2, "from=1&to=2"],
  ])("%j", (v, r, want) => expect(viewQuery(v, r)).toBe(want));
});

describe("anchorView", () => {
  it.each<[DiffView, string | null]>([
    [{ kind: "revision" }, "revision"],
    [{ kind: "commit", sha: "c2" }, "commit"],
    [{ kind: "range", first: "c1", last: "c2" }, "range"],
    [{ kind: "pair", base: 1 }, null],
  ])("%j", (v, want) => expect(anchorView(v)).toBe(want));
});

describe("viewKey", () => {
  it("is the revision alone for the revision view and adds the span otherwise", () => {
    expect(viewKey({ kind: "revision" }, 3, "a", "b")).toBe("3");
    expect(viewKey({ kind: "commit", sha: "b" }, 3, "a", "b")).toBe("3.a.b");
  });
});

const root = (id: string, over: Partial<Comment>): Thread => ({
  root: {
    id,
    thread_root_id: "",
    author_id: "u",
    anchor: null,
    resolved: false,
    version: 1,
    body: "",
    deleted: false,
    attachments: [],
    created_at: "2026-11-13T00:00:00Z",
    ...over,
  },
  replies: [],
});
const anchor = (revision: number, view: string, from: string, to: string) => ({ revision, view, from, to, path: "a.c", side: "new", start_line: 1, end_line: 1 });

describe("viewThreads", () => {
  const onRevision = root("rv", { anchor: anchor(2, "revision", "b0", "d2") });
  const onCommit = root("cm", { anchor: anchor(2, "commit", "d1", "d2") });
  const ported = root("pt", { anchor: anchor(1, "commit", "c1", "c2"), ported: { path: "a.c", side: "new", start_line: 4, end_line: 4, file_level: false, dropped: false } });
  const oldStay = root("old", { anchor: anchor(1, "revision", "b0", "c3") });
  const general = root("gen", {});
  const all = [onRevision, onCommit, ported, oldStay, general];
  const ids = (ts: Thread[]) => ts.map((t) => t.root.id);

  it("shows threads written on the revision view and those ported to it", () => {
    expect(ids(viewThreads(all, { kind: "revision" }, 2, "b0", "d2"))).toEqual(["rv", "pt"]);
  });
  it("shows only threads written on the same commit", () => {
    expect(ids(viewThreads(all, { kind: "commit", sha: "d2" }, 2, "d1", "d2"))).toEqual(["cm"]);
  });
  it("shows nothing inline when comparing revisions", () => {
    expect(viewThreads(all, { kind: "pair", base: 1 }, 2, "c3", "d2")).toEqual([]);
  });
});
