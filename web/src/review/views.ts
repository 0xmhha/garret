import type { Revision, Thread } from "./model";

/**
 * DiffView is what the Files tab compares inside one revision
 * (architecture.md, Reviews and revisions): the whole revision, one
 * commit, a run of consecutive commits, or another revision's head
 * against this one.
 */
export type DiffView =
  | { kind: "revision" }
  | { kind: "commit"; sha: string }
  | { kind: "range"; first: string; last: string }
  | { kind: "pair"; base: number };

const whole: DiffView = { kind: "revision" };

/**
 * parseView reads the view from the URL. Anything that does not name
 * commits of rev, or an earlier revision, falls back to the whole
 * revision, so a stale link still opens something sensible.
 */
export function parseView(params: URLSearchParams, rev: Revision, revisions: readonly Revision[]): DiffView {
  const shas = rev.commits.map((c) => c.sha);
  switch (params.get("view")) {
    case "commit": {
      const sha = params.get("commit") ?? "";
      return shas.includes(sha) ? { kind: "commit", sha } : whole;
    }
    case "range": {
      const first = shas.indexOf(params.get("first") ?? "");
      const last = shas.indexOf(params.get("last") ?? "");
      return first >= 0 && last >= first ? { kind: "range", first: shas[first]!, last: shas[last]! } : whole;
    }
    case "pair": {
      const base = Number(params.get("base"));
      const known = revisions.some((r) => r.number === base);
      return Number.isInteger(base) && base < rev.number && known ? { kind: "pair", base } : whole;
    }
  }
  return whole;
}

/** viewParams writes view into params, replacing any earlier view. */
export function viewParams(params: URLSearchParams, view: DiffView): URLSearchParams {
  const next = new URLSearchParams(params);
  for (const k of ["view", "commit", "first", "last", "base", "file"]) next.delete(k);
  if (view.kind === "commit") {
    next.set("view", "commit");
    next.set("commit", view.sha);
  } else if (view.kind === "range") {
    next.set("view", "range");
    next.set("first", view.first);
    next.set("last", view.last);
  } else if (view.kind === "pair") {
    next.set("view", "pair");
    next.set("base", String(view.base));
  }
  return next;
}

/**
 * viewQuery is the query of GET .../diff for view: a range starts at the
 * parent of its first commit, which is the revision's base for the first.
 */
export function viewQuery(view: DiffView, rev: Revision): string {
  switch (view.kind) {
    case "revision":
      return `rev=${rev.number}`;
    case "commit":
      return `commit=${view.sha}`;
    case "range": {
      const order = [rev.base_sha, ...rev.commits.map((c) => c.sha)];
      const from = order[order.indexOf(view.first) - 1] ?? rev.base_sha;
      return `from=${from}&to=${view.last}`;
    }
    case "pair":
      return `from=${view.base}&to=${rev.number}`;
  }
}

/** anchorView is the anchor view a new comment gets, or null where comments are not written. */
export function anchorView(view: DiffView): "revision" | "commit" | "range" | null {
  return view.kind === "pair" ? null : view.kind;
}

/**
 * viewKey scopes per-viewer state such as reviewed files: the revision
 * alone for the whole revision, otherwise also the commits compared.
 */
export function viewKey(view: DiffView, revision: number, from: string, to: string): string {
  return view.kind === "revision" ? String(revision) : `${revision}.${from}.${to}`;
}

/**
 * viewThreads keeps the code threads that belong on this view: those
 * written on exactly these commits of this revision, and in the whole
 * revision view also the unresolved ones the server ported from earlier
 * revisions. Line numbers of other views do not match these lines.
 */
export function viewThreads(threads: readonly Thread[], view: DiffView, revision: number, from: string, to: string): Thread[] {
  const kind = anchorView(view);
  if (!kind) return [];
  return threads.filter((t) => {
    const a = t.root.anchor;
    if (!a) return false;
    if (a.revision === revision) return a.view === kind && a.from === from && a.to === to;
    return kind === "revision" && a.revision < revision && t.root.ported !== undefined;
  });
}
