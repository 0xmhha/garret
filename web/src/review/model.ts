import type { Storage } from "../api/client";

/** Types of the review API (docs/design/api.md, Reviews and Comments). */
export interface Review {
  id: string;
  number: number;
  title: string;
  source_branch: string;
  target_branch: string;
  state: "open" | "completed" | "abandoned";
  author_id: string;
  description_doc_id: string;
  created_at: string;
  updated_at: string;
}

export interface Commit {
  sha: string;
  subject: string;
  author_name: string;
  authored_at: string;
}

export interface Revision {
  number: number;
  base_sha: string;
  head_sha: string;
  commits_omitted: boolean;
  pushed_by: string;
  pushed_at: string;
  commits: Commit[];
}

export interface Verdict {
  user_id: string;
  value: "approve" | "request_changes";
  at: string;
}

export interface ReviewDetail {
  review: Review;
  revisions: Revision[];
  verdicts: Verdict[];
}

export interface DiffFile {
  status: string;
  old_path: string;
  new_path: string;
  added: number;
  deleted: number;
  binary: boolean;
  similarity: number;
}

export interface DiffSummary {
  from: string;
  to: string;
  bases_differ: boolean;
  files: DiffFile[];
}

export interface DiffLine {
  kind: "context" | "added" | "deleted";
  text: string;
  old?: number;
  new?: number;
  no_newline?: boolean;
}

export interface Hunk {
  old_start: number;
  old_lines: number;
  new_start: number;
  new_lines: number;
  section: string;
  lines: DiffLine[];
}

export interface Anchor {
  revision: number;
  view: string;
  from: string;
  to: string;
  path: string;
  side: string;
  start_line: number;
  end_line: number;
}

export interface Ported {
  path: string;
  side: string;
  start_line: number;
  end_line: number;
  file_level: boolean;
  dropped: boolean;
}

export interface Comment {
  id: string;
  thread_root_id: string;
  author_id: string;
  anchor: Anchor | null;
  ported?: Ported;
  resolved: boolean;
  version: number;
  body: string;
  deleted: boolean;
  attachments: string[];
  created_at: string;
}

/** A thread is a root comment and its replies, oldest first. */
export interface Thread {
  root: Comment;
  replies: Comment[];
}

const byTime = (a: Comment, b: Comment) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id);

/**
 * buildThreads groups comments into threads. A reply whose root is not in
 * the list is left out: showing it alone would read as a new thread.
 */
export function buildThreads(comments: readonly Comment[]): Thread[] {
  // The server sends an empty thread_root_id for a root comment.
  const isRoot = (c: Comment) => c.thread_root_id === "" || c.thread_root_id === c.id;
  const roots = comments.filter(isRoot).sort(byTime);
  const replies = new Map<string, Comment[]>();
  for (const c of comments) {
    if (isRoot(c)) continue;
    const list = replies.get(c.thread_root_id) ?? [];
    list.push(c);
    replies.set(c.thread_root_id, list);
  }
  return roots.map((root) => ({ root, replies: (replies.get(root.id) ?? []).sort(byTime) }));
}

/** Threads of one file: those on a line, keyed "side:line", and file-level ones. */
export interface FileThreads {
  byLine: Map<string, Thread[]>;
  fileLevel: Thread[];
}

/**
 * fileThreads places each thread of path at the last line it covers, using
 * the position ported to the viewed revision when the server sent one.
 * Threads whose lines were deleted (dropped) are not placed.
 */
export function fileThreads(threads: readonly Thread[], path: string): FileThreads {
  const byLine = new Map<string, Thread[]>();
  const fileLevel: Thread[] = [];
  for (const t of threads) {
    const a = t.root.anchor;
    if (!a) continue;
    const p = t.root.ported;
    const where = p ?? { path: a.path, side: a.side, start_line: a.start_line, end_line: a.end_line, file_level: false, dropped: false };
    if (where.path !== path || where.dropped) continue;
    if (where.file_level) {
      fileLevel.push(t);
      continue;
    }
    const key = lineKey(where.side, where.end_line);
    byLine.set(key, [...(byLine.get(key) ?? []), t]);
  }
  return { byLine, fileLevel };
}

/** lineKey names one line of one side, as fileThreads keys it. */
export const lineKey = (side: string, line: number) => `${side}:${line}`;

/**
 * reviewedStore remembers which files the viewer marked reviewed, per
 * revision, in the browser only (a per-viewer convenience). A storage that
 * throws (private mode) just forgets.
 */
export function reviewedStore(storage: Storage, project: string, review: number, scope: number | string) {
  const key = `garret.reviewed.${project}.${review}.${scope}`;
  const read = (): Set<string> => {
    try {
      const raw = storage.getItem(key);
      const list: unknown = raw ? JSON.parse(raw) : [];
      return new Set(Array.isArray(list) ? list.filter((x): x is string => typeof x === "string") : []);
    } catch {
      return new Set();
    }
  };
  return {
    storage,
    has: (path: string) => read().has(path),
    all: read,
    set(path: string, reviewed: boolean) {
      const s = read();
      if (reviewed) s.add(path);
      else s.delete(path);
      try {
        storage.setItem(key, JSON.stringify([...s]));
      } catch {
        // Storage unavailable: the mark lasts only for this view.
      }
    },
  };
}
