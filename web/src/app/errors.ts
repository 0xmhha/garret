import { ApiError } from "../api/client";
import type { MessageKey, Values } from "../i18n/i18n";

type T = (key: MessageKey, values?: Values) => string;

const byCode: Readonly<Record<string, MessageKey>> = {
  network: "error.network",
  forbidden: "error.forbidden",
  not_found: "error.notFound",
  conflict: "error.conflict",
  export_required: "error.exportRequired",
};

/**
 * errorMessage turns a failed request into a sentence for the screen. A
 * refused input carries the server's own reason, which names the field.
 */
export function errorMessage(err: unknown, t: T): string {
  if (!(err instanceof ApiError)) return t("error.generic");
  if (err.code === "invalid") return t("error.invalid", { reason: err.message });
  const key = byCode[err.code];
  return key ? t(key) : t("error.generic");
}
