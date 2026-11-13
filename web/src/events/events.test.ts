import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { keys } from "../api/keys";
import { connectEvents, invalidationsFor, parseEvent } from "./events";

describe("parseEvent", () => {
  it("accepts the server's shape", () => {
    expect(parseEvent('{"type":"comment.created","project":"libfoo","review":12,"id":"C1"}')).toEqual({
      type: "comment.created", project: "libfoo", review: 12, id: "C1",
    });
  });
  it.each(["not json", "null", "[]", '{"type":1}', '{"type":"x","project":3}', '{"type":"x","project":"p","review":"12"}'])("ignores %s", (raw) => {
    expect(parseEvent(raw)).toBeNull();
  });
});

describe("invalidationsFor", () => {
  it.each([
    [{ type: "comment.created", project: "libfoo", review: 12 }, [keys.review("libfoo", 12)]],
    [{ type: "comment.updated", project: "libfoo", review: 12 }, [keys.review("libfoo", 12)]],
    [{ type: "revision.created", project: "libfoo", review: 12 }, [keys.review("libfoo", 12), keys.reviews("libfoo")]],
    [{ type: "review.created", project: "libfoo", review: 13 }, [keys.reviews("libfoo")]],
    [{ type: "review.state_changed", project: "libfoo", review: 12 }, [keys.review("libfoo", 12), keys.reviews("libfoo")]],
    [{ type: "verdict.recorded", project: "libfoo", review: 12 }, [keys.review("libfoo", 12), keys.reviews("libfoo")]],
    [{ type: "comment.created", project: "libfoo", id: "D1" }, [keys.discussions("libfoo")]],
    [{ type: "discussion.created", project: "libfoo", id: "D1" }, [keys.discussions("libfoo")]],
    [{ type: "notification.created", project: "" }, [keys.inbox()]],
    [{ type: "something.new", project: "libfoo" }, [keys.project("libfoo")]],
  ] as const)("%j", (ev, want) => {
    expect(invalidationsFor(ev)).toEqual(want);
  });
});

class FakeSocket {
  static all: FakeSocket[] = [];
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onopen: (() => void) | null = null;
  closed = false;
  constructor(readonly url: string) {
    FakeSocket.all.push(this);
  }
  close() {
    this.closed = true;
  }
}

describe("connectEvents", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => vi.useRealTimers());

  const start = (onEvent = vi.fn(), onEnded = vi.fn()) => {
    const stop = connectEvents({ url: "wss://h/api/v1/ws", WebSocket: FakeSocket as never, onEvent, onEnded });
    return { stop, onEvent, onEnded };
  };

  it("delivers parsed events and drops malformed ones", () => {
    const { onEvent } = start();
    const s = FakeSocket.all[0]!;
    s.onmessage?.({ data: '{"type":"review.created","project":"p","review":1}' });
    s.onmessage?.({ data: "garbage" });
    expect(onEvent).toHaveBeenCalledTimes(1);
  });

  it("reconnects with growing backoff, capped, and resets after a good connection", () => {
    start();
    for (const wait of [1000, 2000, 4000, 8000, 16000, 30000, 30000]) {
      const before = FakeSocket.all.length;
      FakeSocket.all.at(-1)!.onclose?.({ code: 1006 });
      vi.advanceTimersByTime(wait - 1);
      expect(FakeSocket.all.length).toBe(before);
      vi.advanceTimersByTime(1);
      expect(FakeSocket.all.length).toBe(before + 1);
    }
    FakeSocket.all.at(-1)!.onopen?.();
    FakeSocket.all.at(-1)!.onclose?.({ code: 1006 });
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.all.length).toBe(9);
  });

  it("stops for good when the server ends the stream (1008)", () => {
    const { onEnded } = start();
    FakeSocket.all[0]!.onclose?.({ code: 1008 });
    vi.advanceTimersByTime(60000);
    expect(FakeSocket.all).toHaveLength(1);
    expect(onEnded).toHaveBeenCalledOnce();
  });

  it("closes and never reconnects after stop", () => {
    const { stop } = start();
    stop();
    expect(FakeSocket.all[0]!.closed).toBe(true);
    FakeSocket.all[0]!.onclose?.({ code: 1006 });
    vi.advanceTimersByTime(60000);
    expect(FakeSocket.all).toHaveLength(1);
  });
});
