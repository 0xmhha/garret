const eocdSignature = [0x50, 0x4b, 0x05, 0x06];
const eocdSize = 22;
const maxComment = 0xffff;

/**
 * zipComplete reports whether bytes end with a zip end-of-central-directory
 * record whose comment runs exactly to the end. An export cut short by a
 * server error lacks it: the browser cannot read the digest trailer, so
 * this is how the interface tells a complete archive from a broken one.
 */
export function zipComplete(bytes: Uint8Array): boolean {
  const lowest = Math.max(0, bytes.length - eocdSize - maxComment);
  for (let at = bytes.length - eocdSize; at >= lowest; at--) {
    if (eocdSignature.every((b, i) => bytes[at + i] === b)) {
      const comment = (bytes[at + 20] ?? 0) | ((bytes[at + 21] ?? 0) << 8);
      if (at + eocdSize + comment === bytes.length) return true;
    }
  }
  return false;
}

/** hex formats a digest as lowercase hexadecimal. */
export function hex(digest: ArrayBuffer): string {
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** EraseScope is what an erasure removes (POST /api/v1/admin/erasures). */
export type EraseScope = "comment" | "attachment" | "collab_document";

/** eraseHref opens the erasure form on the administration screen, filled in. */
export function eraseHref(scope: EraseScope, id: string): string {
  return `/admin?erase=${scope}&id=${encodeURIComponent(id)}#erase`;
}
