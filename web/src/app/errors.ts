import { ApiError } from "../api/client";
import type { MessageKey, Values } from "../i18n/i18n";

type T = (key: MessageKey, values?: Values) => string;

const byCode: Readonly<Record<string, MessageKey>> = {
  network: "error.network",
  forbidden: "error.forbidden",
  not_found: "error.notFound",
  export_required: "error.exportRequired",
};

/**
 * errorMessage turns a failed request into a sentence for the screen. A
 * refused input or a conflict carries the server's own reason.
 */
export function errorMessage(err: unknown, t: T): string {
  if (!(err instanceof ApiError)) return t("error.generic");
  // Refusals carry the server's reason, which names what to fix.
  if (err.code === "invalid" || err.code === "conflict") return t("error.invalid", { reason: err.message });
  const key = byCode[err.code];
  return key ? t(key) : t("error.generic");
}
