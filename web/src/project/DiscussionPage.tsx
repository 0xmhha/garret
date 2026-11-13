import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { useParams } from "react-router";
import { keys } from "../api/keys";
import { useApi, useLocale, useT } from "../app/context";
import { DescriptionEditor } from "../collab/DescriptionEditor";
import { formatRelative } from "../i18n/i18n";
import { Composer } from "../review/Composer";
import { buildThreads, type Comment } from "../review/model";
import { useUsers } from "../review/queries";
import { name, ThreadView } from "../review/Thread";
import "../review/review.css";
import "./project.css";

interface Discussion {
  id: string;
  title: string;
  author_id: string;
  body_doc_id: string;
  created_at: string;
}

/** DiscussionPage is one discussion: its shared body, its comments and its history. */
export function DiscussionPage() {
  const { project = "", id = "" } = useParams();
  const t = useT();
  const locale = useLocale();
  const api = useApi();
  const qc = useQueryClient();
  const users = useUsers();
  const base = `/api/v1/projects/${encodeURIComponent(project)}/discussions/${encodeURIComponent(id)}`;
  const key = keys.discussion(project, id);
  const d = useQuery({ queryKey: key, queryFn: ({ signal }) => api.request<Discussion>("GET", base, undefined, { signal }) });
  const comments = useQuery({
    queryKey: [...key, "comments"],
    queryFn: ({ signal }) => api.request<{ comments: Comment[] }>("GET", `${base}/comments`, undefined, { signal }),
  });
  const history = useQuery({
    queryKey: [...key, "history"],
    queryFn: ({ signal }) => api.request<{ checkpoints: { up_to_seq: number; contributors: string[]; at: string }[] }>("GET", `${base}/history?limit=100`, undefined, { signal }),
  });
  const threads = useMemo(() => buildThreads(comments.data?.comments ?? []), [comments.data]);
  const post = useMutation({
    mutationFn: (v: { body: string; reply_to?: string }) => api.request("POST", `${base}/comments`, v),
    onSuccess: () => qc.invalidateQueries({ queryKey: key }),
  });
  const resolve = useMutation({
    mutationFn: (v: { id: string; resolved: boolean }) => api.request("POST", `/api/v1/comments/${encodeURIComponent(v.id)}/resolve`, { resolved: v.resolved }),
    onSuccess: () => qc.invalidateQueries({ queryKey: key }),
  });
  const now = new Date();

  if (d.isPending) return <p className="muted pad">{t("app.loading")}</p>;
  if (d.isError || !d.data) return <p className="form-error pad" role="alert">{t("discussion.loadFailed")}</p>;

  return (
    <main className="page discussion">
      <h1 className="page__title">{d.data.title}</h1>
      <p className="muted discussion__meta">{t("discussion.startedBy", { author: name(users.data, d.data.author_id) })}</p>
      <div className="discussion__grid">
        <div>
          <DescriptionEditor project={project} doc={d.data.body_doc_id} label={t("discussion.body")} />
          <h2 className="section-title section-title--spaced">{t("conversation.threads")}</h2>
          {threads.map((th) => (
            <ThreadView key={th.root.id} thread={th} users={users.data} now={now} onReply={(root, body) => post.mutateAsync({ body, reply_to: root })} onResolve={(rid, r) => resolve.mutate({ id: rid, resolved: r })} />
          ))}
          <Composer label={t("conversation.newComment")} submitLabel={t("conversation.comment")} onSubmit={(body) => post.mutateAsync({ body })} />
        </div>
        <aside aria-labelledby="discussion-history">
          <h2 id="discussion-history" className="section-title">{t("conversation.history")}</h2>
          {(history.data?.checkpoints ?? []).length === 0 ? <p className="muted">{t("discussion.noHistory")}</p> : null}
          <ol className="timeline">
            {[...(history.data?.checkpoints ?? [])].reverse().map((c) => (
              <li key={c.up_to_seq} className="timeline__item">
                {t("discussion.checkpoint", { names: c.contributors.map((u) => name(users.data, u)).join(", ") })}{" "}
                <time className="muted" dateTime={c.at}>{formatRelative(locale, new Date(c.at), now)}</time>
              </li>
            ))}
          </ol>
        </aside>
      </div>
    </main>
  );
}
