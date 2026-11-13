import { pickLocale, type Locale } from "../i18n/i18n";

/** Token is a personal access token as GET /api/v1/me/tokens lists it. */
export interface Token {
  id: string;
  name: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

/** Certificate is a client certificate as GET /api/v1/me/certificates lists it. */
export interface Certificate {
  serial: string;
  device_name: string;
  issued_via: string;
  not_before: string;
  not_after: string;
  revoked_at: string | null;
  current: boolean;
}

/** Invitation is an invitation as GET /api/v1/admin/invitations lists it. */
export interface Invitation {
  id: string;
  role: "admin" | "member";
  created_at: string;
  expires_at: string;
  revoked_at: string | null;
  used_at: string | null;
  used_by: string;
}

export type TokenStatus = "active" | "revoked";
export type CertificateStatus = "current" | "active" | "expired" | "revoked";
export type InvitationStatus = "pending" | "used" | "revoked" | "expired";

/** tokenStatus is whether a token still works. */
export function tokenStatus(t: Pick<Token, "revoked_at">): TokenStatus {
  return t.revoked_at ? "revoked" : "active";
}

/**
 * certificateStatus is revoked, then expired (at or past not_after, or an
 * unreadable date), then current for the one this browser presented.
 */
export function certificateStatus(c: Pick<Certificate, "revoked_at" | "not_after" | "current">, now: Date): CertificateStatus {
  if (c.revoked_at) return "revoked";
  if (!before(now, c.not_after)) return "expired";
  return c.current ? "current" : "active";
}

/** invitationStatus is used, then revoked, then expired, else pending. */
export function invitationStatus(i: Pick<Invitation, "revoked_at" | "used_at" | "expires_at">, now: Date): InvitationStatus {
  if (i.used_at) return "used";
  if (i.revoked_at) return "revoked";
  if (!before(now, i.expires_at)) return "expired";
  return "pending";
}

// before reports whether now is strictly before the time t; an unreadable
// t counts as already past, so a bad value never shows as valid.
function before(now: Date, t: string): boolean {
  const ms = Date.parse(t);
  return Number.isFinite(ms) && now.getTime() < ms;
}

/** localeKey is where the chosen interface language is kept. */
export const localeKey = "garret.locale";

/** resolveLocale prefers the language chosen in account settings, then the browser's. */
export function resolveLocale(stored: string | null, languages: readonly string[]): Locale {
  if (stored === "en" || stored === "ko") return stored;
  return pickLocale(languages);
}
