import { keys, type QueryKey } from "../api/keys";

/** An event from /api/v1/ws (internal/realtime.Event). */
export interface ServerEvent {
  readonly type: string;
  readonly project: string;
  readonly review?: number;
  readonly id?: string;
}

/**
 * parseEvent reads one message. Anything that is not the server's shape is
 * ignored rather than trusted: only the type and identifiers are used, and
 * only to decide what to fetch again.
 */
export function parseEvent(raw: string): ServerEvent | null {
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof v !== "object" || v === null || Array.isArray(v)) return null;
  const o = v as Record<string, unknown>;
  if (typeof o["type"] !== "string" || typeof o["project"] !== "string") return null;
  if (o["review"] !== undefined && typeof o["review"] !== "number") return null;
  if (o["id"] !== undefined && typeof o["id"] !== "string") return null;
  return {
    type: o["type"],
    project: o["project"],
    ...(typeof o["review"] === "number" ? { review: o["review"] } : {}),
    ...(typeof o["id"] === "string" ? { id: o["id"] } : {}),
  };
}

/** invalidationsFor names the queries an event makes stale. */
export function invalidationsFor(ev: ServerEvent): QueryKey[] {
  const { type, project, review } = ev;
  if (type === "notification.created") return [keys.inbox()];
  if (type === "discussion.created" || (type.startsWith("comment.") && review === undefined)) {
    return [keys.discussions(project)];
  }
  if (review !== undefined) {
    switch (type) {
      case "comment.created":
      case "comment.updated":
      case "comment.resolved":
        return [keys.review(project, review)];
      case "revision.created":
      case "review.state_changed":
      case "verdict.recorded":
        return [keys.review(project, review), keys.reviews(project)];
      case "review.created":
        return [keys.reviews(project)];
    }
  }
  // An event type this build does not know yet: refetch the whole project.
  return [keys.project(project)];
}

interface SocketLike {
  onmessage: ((e: { data: string }) => void) | null;
  onclose: ((e: { code: number }) => void) | null;
  onopen: (() => void) | null;
  close(): void;
}

export interface EventsOptions {
  url: string;
  WebSocket: new (url: string) => SocketLike;
  onEvent: (ev: ServerEvent) => void;
  /** Called once when the server ends the stream for good (logout, disable). */
  onEnded: () => void;
}

const firstDelay = 1000;
const maxDelay = 30000;
// The server closes with 1008 when the user's subscription is closed: the
// session is over and reconnecting would only fail.
const endedByServer = 1008;

/**
 * connectEvents keeps one event socket open, reconnecting with doubling
 * delays up to 30 seconds; a successful open resets the delay. It returns a
 * function that closes the socket for good.
 */
export function connectEvents(opts: EventsOptions): () => void {
  let delay = firstDelay;
  let stopped = false;
  let socket: SocketLike | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const open = () => {
    const s = new opts.WebSocket(opts.url);
    socket = s;
    s.onopen = () => {
      delay = firstDelay;
    };
    s.onmessage = (e) => {
      const ev = parseEvent(String(e.data));
      if (ev) opts.onEvent(ev);
    };
    s.onclose = (e) => {
      if (stopped) return;
      if (e.code === endedByServer) {
        stopped = true;
        opts.onEnded();
        return;
      }
      timer = setTimeout(open, delay);
      delay = Math.min(delay * 2, maxDelay);
    };
  };
  open();

  return () => {
    stopped = true;
    clearTimeout(timer);
    socket?.close();
  };
}
