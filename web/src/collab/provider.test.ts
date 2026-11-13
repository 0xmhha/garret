import * as decoding from "lib0/decoding";
import * as encoding from "lib0/encoding";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as awarenessProtocol from "y-protocols/awareness";
import * as syncProtocol from "y-protocols/sync";
import * as Y from "yjs";
import { connectDoc, MESSAGE_AWARENESS, MESSAGE_RELOAD, MESSAGE_SYNC } from "./provider";

class FakeSocket {
  static last: FakeSocket;
  binaryType = "";
  sent: Uint8Array[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  closed = false;
  constructor(readonly url: string) {
    FakeSocket.last = this;
  }
  send(d: Uint8Array) {
    this.sent.push(d);
  }
  close() {
    this.closed = true;
  }
  receive(bytes: Uint8Array) {
    this.onmessage?.({ data: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) });
  }
}

function frame(write: (e: encoding.Encoder) => void): Uint8Array {
  const e = encoding.createEncoder();
  write(e);
  return encoding.toUint8Array(e);
}

const kind = (msg: Uint8Array) => [msg[0], msg[1]];

describe("connectDoc", () => {
  let doc: Y.Doc;
  let awareness: awarenessProtocol.Awareness;
  let onReload: ReturnType<typeof vi.fn<() => void>>;
  beforeEach(() => {
    doc = new Y.Doc();
    awareness = new awarenessProtocol.Awareness(doc);
    onReload = vi.fn<() => void>();
    connectDoc({ url: "wss://h/x", doc, awareness, WebSocket: FakeSocket as never, onReload });
    FakeSocket.last.onopen?.();
  });

  it("reports the open connection", () => {
    const onOpen = vi.fn();
    connectDoc({ url: "wss://h/y", doc: new Y.Doc(), awareness: new awarenessProtocol.Awareness(new Y.Doc()), WebSocket: FakeSocket as never, onReload: () => {}, onOpen });
    FakeSocket.last.onopen?.();
    expect(onOpen).toHaveBeenCalledOnce();
  });

  it("starts with sync step 1", () => {
    expect(kind(FakeSocket.last.sent[0]!)).toEqual([MESSAGE_SYNC, syncProtocol.messageYjsSyncStep1]);
  });

  it("applies the server's state and does not echo it back", () => {
    const server = new Y.Doc();
    server.getText("body").insert(0, "hello");
    const sentBefore = FakeSocket.last.sent.length;
    FakeSocket.last.receive(frame((e) => {
      encoding.writeVarUint(e, MESSAGE_SYNC);
      syncProtocol.writeSyncStep2(e, server, Y.encodeStateVector(doc));
    }));
    expect(doc.getText("body").toString()).toBe("hello");
    expect(FakeSocket.last.sent.slice(sentBefore).filter((m) => m[1] === syncProtocol.messageYjsUpdate)).toHaveLength(0);
  });

  it("answers the server's step 1 with step 2", () => {
    doc.getText("body").insert(0, "local");
    const sentBefore = FakeSocket.last.sent.length;
    FakeSocket.last.receive(frame((e) => {
      encoding.writeVarUint(e, MESSAGE_SYNC);
      syncProtocol.writeSyncStep1(e, new Y.Doc());
    }));
    const reply = FakeSocket.last.sent.slice(sentBefore).find((m) => m[1] === syncProtocol.messageYjsSyncStep2);
    expect(reply).toBeDefined();
  });

  it("sends local edits as sync updates", () => {
    const sentBefore = FakeSocket.last.sent.length;
    doc.getText("body").insert(0, "x");
    const update = FakeSocket.last.sent.slice(sentBefore).find((m) => m[0] === MESSAGE_SYNC && m[1] === syncProtocol.messageYjsUpdate);
    expect(update).toBeDefined();
    const d = decoding.createDecoder(update!);
    decoding.readVarUint(d);
    decoding.readVarUint(d);
    const other = new Y.Doc();
    Y.applyUpdate(other, decoding.readVarUint8Array(d));
    expect(other.getText("body").toString()).toBe("x");
  });

  it("sends awareness changes", () => {
    const sentBefore = FakeSocket.last.sent.length;
    awareness.setLocalStateField("cursor", { anchor: 1 });
    expect(FakeSocket.last.sent.slice(sentBefore).some((m) => m[0] === MESSAGE_AWARENESS)).toBe(true);
  });

  it("closes and asks for a reload when the server rebuilt the document", () => {
    FakeSocket.last.receive(new Uint8Array([MESSAGE_RELOAD]));
    expect(onReload).toHaveBeenCalledOnce();
    expect(FakeSocket.last.closed).toBe(true);
  });

  it("ignores text frames and malformed binary frames", () => {
    expect(() => FakeSocket.last.onmessage?.({ data: "text" })).not.toThrow();
    expect(() => FakeSocket.last.receive(new Uint8Array([MESSAGE_SYNC, 9, 9, 9]))).not.toThrow();
    expect(() => FakeSocket.last.receive(new Uint8Array([]))).not.toThrow();
  });
});
