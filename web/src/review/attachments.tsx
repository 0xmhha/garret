import { useQueries } from "@tanstack/react-query";
import { useCallback, useRef, useState } from "react";
import { FileTrigger } from "react-aria-components";
import { keys } from "../api/keys";
import { useApi, useLocale, useT } from "../app/context";
import { errorMessage } from "../app/errors";
import { formatSize } from "../i18n/i18n";
import { Button } from "../ui/Button";
import { IconClose, IconPaperclip } from "../ui/icons";

/** maxAttachmentBytes is the server's upload limit (PRODUCT.md, 25 MiB). */
export const maxAttachmentBytes = 25 * 1024 * 1024;

/** Attachment is GET /api/v1/attachments/{id}/meta. */
export interface Attachment {
  id: string;
  filename: string;
  media_type: string;
  size: number;
  inline: boolean;
  created_at: string;
}

/** Pending is a file chosen in a comment box: uploading, uploaded, or refused. */
export interface Pending {
  key: number;
  name: string;
  size: number;
  state: "uploading" | "done" | "failed";
  id?: string;
  error?: string;
}

const fileURL = (id: string) => `/api/v1/attachments/${encodeURIComponent(id)}`;

/**
 * useUploads uploads chosen files to project at once and tracks them until
 * the comment is sent. Removing an uploaded file deletes it on the server.
 */
export function useUploads(project: string) {
  const api = useApi();
  const t = useT();
  const [pending, setPending] = useState<Pending[]>([]);
  const next = useRef(0);
  const update = (key: number, change: Partial<Pending>) => setPending((list) => list.map((p) => (p.key === key ? { ...p, ...change } : p)));

  const add = (files: Iterable<File>) => {
    for (const file of files) {
      const key = ++next.current;
      if (file.size > maxAttachmentBytes) {
        setPending((list) => [...list, { key, name: file.name, size: file.size, state: "failed", error: t("attach.tooLarge") }]);
        continue;
      }
      setPending((list) => [...list, { key, name: file.name, size: file.size, state: "uploading" }]);
      api.upload<Attachment>(`/api/v1/projects/${encodeURIComponent(project)}/attachments`, file, file.name).then(
        (a) => update(key, { state: "done", id: a.id }),
        (err: unknown) => update(key, { state: "failed", error: errorMessage(err, t) }),
      );
    }
  };
  const forget = (p: Pending) => {
    if (p.id) void api.request("DELETE", fileURL(p.id)).catch(() => undefined); // best effort: an orphan is harmless
  };
  // The server calls stay out of state updaters, which StrictMode runs twice.
  const remove = (key: number) => {
    const p = pending.find((x) => x.key === key);
    if (p) forget(p);
    setPending((list) => list.filter((x) => x.key !== key));
  };
  /** discard deletes every uploaded file, for a comment box that is cancelled. */
  const discard = () => {
    pending.forEach(forget);
    setPending([]);
  };
  const clear = useCallback(() => setPending([]), []);
  const ids = pending.flatMap((p) => (p.state === "done" && p.id ? [p.id] : []));
  const busy = pending.some((p) => p.state === "uploading");
  return { pending, add, remove, discard, clear, ids, busy };
}

/** AttachButton opens the file chooser. */
export function AttachButton({ onFiles, isDisabled }: { onFiles: (files: File[]) => void; isDisabled?: boolean }) {
  const t = useT();
  return (
    <FileTrigger allowsMultiple onSelect={(list) => list && onFiles([...list])}>
      <Button variant="quiet" isDisabled={isDisabled ?? false}>
        <IconPaperclip /> {t("attach.button")}
      </Button>
    </FileTrigger>
  );
}

/** PendingList shows the chosen files with their state and a remove button each. */
export function PendingList({ pending, onRemove }: { pending: Pending[]; onRemove: (key: number) => void }) {
  const t = useT();
  const locale = useLocale();
  if (pending.length === 0) return null;
  return (
    <ul className="pending" aria-label={t("attach.chosen")}>
      {pending.map((p) => (
        <li key={p.key} className={`pending__item pending__item--${p.state}`}>
          <span className="pending__name">{p.name}</span>
          <span className="pending__meta">{p.state === "failed" ? p.error : p.state === "uploading" ? t("attach.uploading") : formatSize(locale, p.size)}</span>
          <Button variant="quiet" aria-label={t("attach.remove", { name: p.name })} onPress={() => onRemove(p.key)}>
            <IconClose />
          </Button>
        </li>
      ))}
    </ul>
  );
}

/**
 * AttachmentList shows a comment's files: images the server serves inline
 * as images, everything else as a download link, each with name and size.
 */
export function AttachmentList({ ids }: { ids: readonly string[] }) {
  const api = useApi();
  const t = useT();
  const locale = useLocale();
  const metas = useQueries({
    queries: ids.map((id) => ({
      queryKey: keys.attachment(id),
      queryFn: ({ signal }: { signal: AbortSignal }) => api.request<Attachment>("GET", `${fileURL(id)}/meta`, undefined, { signal }),
      staleTime: Infinity, // an attachment never changes, it only disappears
      retry: false,
    })),
  });
  if (ids.length === 0) return null;
  return (
    <ul className="attachments" aria-label={t("attach.files")}>
      {ids.map((id, i) => {
        const q = metas[i];
        const a = q?.data;
        if (q?.isError) {
          return (
            <li key={id} className="attachments__item muted">
              {t("attach.gone")}
            </li>
          );
        }
        if (!a) return <li key={id} className="attachments__item muted">{t("app.loading")}</li>;
        const size = formatSize(locale, a.size);
        return (
          <li key={id} className="attachments__item">
            {a.inline ? (
              <a className="attachments__image" href={fileURL(a.id)} target="_blank" rel="noopener noreferrer">
                <img src={fileURL(a.id)} alt={a.filename} loading="lazy" />
                <span className="attachments__caption">
                  {a.filename} · {size}
                </span>
              </a>
            ) : (
              // No download attribute: the server's Content-Disposition names
              // the file, and WebKit ignores same-origin download links.
              <a className="attachments__file" href={fileURL(a.id)}>
                <IconPaperclip /> <span className="attachments__name">{a.filename}</span> <span className="muted">{size}</span>
              </a>
            )}
          </li>
        );
      })}
    </ul>
  );
}
