import * as decoding from "lib0/decoding";
import * as encoding from "lib0/encoding";
import * as awarenessProtocol from "y-protocols/awareness";
import * as syncProtocol from "y-protocols/sync";
import type * as Y from "yjs";

/** Frame types of the collaboration endpoint (internal/collab). */
export const MESSAGE_SYNC = 0;
export const MESSAGE_AWARENESS = 1;
export const MESSAGE_RELOAD = 2;

interface SocketLike {
  binaryType: string;
  onopen: (() => void) | null;
  onmessage: ((e: { data: unknown }) => void) | null;
  onclose: ((e: { code: number }) => void) | null;
  send(data: Uint8Array): void;
  close(): void;
}

export interface CollabOptions {
  url: string;
  doc: Y.Doc;
  awareness: awarenessProtocol.Awareness;
  WebSocket: new (url: string) => SocketLike;
  /** The server rebuilt the document (an erasure): rejoin from scratch. */
  onReload: () => void;
  /** The connection opened. */
  onOpen?: () => void;
  /** The connection closed for another reason. */
  onClose?: (code: number) => void;
}

/**
 * connectDoc joins one collaborative document with the y-websocket framing
 * the server speaks: sync step 1 on open, step 2 in answer to the server's
 * step 1, updates both ways, awareness both ways, and a reload frame when
 * the server rebuilt the document. Malformed frames are ignored. It returns
 * a function that leaves the document.
 */
export function connectDoc(opts: CollabOptions): () => void {
  const { doc, awareness } = opts;
  const socket = new opts.WebSocket(opts.url);
  socket.binaryType = "arraybuffer";
  let open = false;

  const send = (write: (e: encoding.Encoder) => void) => {
    if (!open) return;
    const e = encoding.createEncoder();
    write(e);
    socket.send(encoding.toUint8Array(e));
  };

  const onUpdate = (update: Uint8Array, origin: unknown) => {
    if (origin === socket) return; // came from the server
    send((e) => {
      encoding.writeVarUint(e, MESSAGE_SYNC);
      syncProtocol.writeUpdate(e, update);
    });
  };
  const onAwareness = ({ added, updated, removed }: { added: number[]; updated: number[]; removed: number[] }, origin: unknown) => {
    if (origin === socket) return;
    const changed = [...added, ...updated, ...removed];
    send((e) => {
      encoding.writeVarUint(e, MESSAGE_AWARENESS);
      encoding.writeVarUint8Array(e, awarenessProtocol.encodeAwarenessUpdate(awareness, changed));
    });
  };

  socket.onopen = () => {
    open = true;
    opts.onOpen?.();
    send((e) => {
      encoding.writeVarUint(e, MESSAGE_SYNC);
      syncProtocol.writeSyncStep1(e, doc);
    });
    if (awareness.getLocalState() !== null) {
      send((e) => {
        encoding.writeVarUint(e, MESSAGE_AWARENESS);
        encoding.writeVarUint8Array(e, awarenessProtocol.encodeAwarenessUpdate(awareness, [awareness.clientID]));
      });
    }
  };

  socket.onmessage = (event) => {
    if (!(event.data instanceof ArrayBuffer)) return;
    const bytes = new Uint8Array(event.data);
    if (bytes.length === 0) return;
    try {
      const d = decoding.createDecoder(bytes);
      switch (decoding.readVarUint(d)) {
        case MESSAGE_SYNC: {
          const reply = encoding.createEncoder();
          encoding.writeVarUint(reply, MESSAGE_SYNC);
          syncProtocol.readSyncMessage(d, reply, doc, socket);
          if (encoding.length(reply) > 1) socket.send(encoding.toUint8Array(reply));
          break;
        }
        case MESSAGE_AWARENESS:
          awarenessProtocol.applyAwarenessUpdate(awareness, decoding.readVarUint8Array(d), socket);
          break;
        case MESSAGE_RELOAD:
          leave();
          opts.onReload();
          break;
      }
    } catch {
      // A malformed frame from the server: ignore it rather than tear down
      // the editor; the next sync repairs any gap.
    }
  };

  socket.onclose = (e) => {
    open = false;
    opts.onClose?.(e.code);
  };

  doc.on("update", onUpdate);
  awareness.on("update", onAwareness);

  function leave() {
    doc.off("update", onUpdate);
    awareness.off("update", onAwareness);
    awarenessProtocol.removeAwarenessStates(awareness, [awareness.clientID], "leave");
    open = false;
    socket.close();
  }
  return leave;
}
