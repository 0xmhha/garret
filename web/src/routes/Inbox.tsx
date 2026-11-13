import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { keys } from "../api/keys";
import { useApi, useLocale, useT } from "../app/context";
import { formatRelative, type MessageKey } from "../i18n/i18n";
import { Button } from "../ui/Button";
import "../review/review.css";
import "./Inbox.css";

export interface Note {
  id: string;
  kind: string;
  subject_kind: string;
  created_at: string;
  read_at: string | null;
  project: string;
  review?: number;
  discussion?: string;
}

export function useInbox(unread = false) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.inbox(), unread],
    queryFn: ({ signal }) => api.request<{ notifications: Note[] }>("GET", `/api/v1/inbox?limit=100${unread ? "&unread=true" : ""}`, undefined, { signal }),
    select: (r) => r.notifications,
  });
}

/** link is where a notification leads, or "" when its subject is gone. */
export function link(n: Note): string {
  const p = `/p/${encodeURIComponent(n.project)}`;
  if (n.review) return `${p}/r/${n.review}`;
  if (n.discussion) return `${p}/d/${encodeURIComponent(n.discussion)}`;
  return "";
}

/** Inbox lists notifications, newest first, and marks them read. */
export function Inbox() {
  const t = useT();
  const locale = useLocale();
  const api = useApi();
  const qc = useQueryClient();
  const notes = useInbox();
  const read = useMutation({
    mutationFn: (ids: string[]) => api.request("POST", "/api/v1/inbox/read", { ids }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.inbox() }),
  });
  const unread = (notes.data ?? []).filter((n) => !n.read_at).map((n) => n.id);
  const now = new Date();
  return (
    <main className="page">
      <div className="inbox__head">
        <h1 className="page__title">{t("inbox.title")}</h1>
        <Button isDisabled={unread.length === 0 || read.isPending} onPress={() => read.mutate(unread)}>{t("inbox.markAll")}</Button>
      </div>
      {notes.data?.length === 0 ? <p className="muted">{t("inbox.empty")}</p> : null}
      <ul className="list">
        {(notes.data ?? []).map((n) => {
          const to = link(n);
          const text = t(`inbox.kind.${n.kind}` as MessageKey);
          return (
            <li key={n.id} className={`list__item inbox__item${n.read_at ? "" : " is-unread"}`}>
              {to ? <Link to={to} className="list__title" onClick={() => !n.read_at && read.mutate([n.id])}>{text}</Link> : <span className="list__title">{text}</span>}
              <span className="list__meta">
                {n.project}
                {n.review ? ` · #${n.review}` : ""} · <time dateTime={n.created_at}>{formatRelative(locale, new Date(n.created_at), now)}</time>
              </span>
            </li>
          );
        })}
      </ul>
    </main>
  );
}
