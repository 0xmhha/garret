import { useMemo } from "react";
import { useLocale, useT } from "../app/context";
import type { User } from "../app/session";
import { formatRelative } from "../i18n/i18n";
import { Composer } from "./Composer";
import type { Thread } from "./model";
import { useHistory, type HistoryEvent } from "./queries";
import { name, ThreadView } from "./Thread";

interface Props {
  project: string;
  number: number;
  threads: Thread[];
  users: Map<string, User> | undefined;
  now: Date;
  onComment: (body: string) => Promise<unknown>;
  onReply: (rootId: string, body: string) => Promise<unknown>;
  onResolve: (rootId: string, resolved: boolean) => void;
}

/** Conversation shows the review-wide threads, then the review's history. */
export function Conversation({ project, number, threads, users, now, onComment, onReply, onResolve }: Props) {
  const t = useT();
  const locale = useLocale();
  const history = useHistory(project, number);
  const general = useMemo(() => threads.filter((th) => th.root.anchor === null), [threads]);

  return (
    <div className="conversation">
      <section aria-labelledby="conv-threads">
        <h2 id="conv-threads" className="section-title">{t("conversation.threads")}</h2>
        {general.length === 0 ? <p className="muted">{t("conversation.empty")}</p> : null}
        {general.map((th) => (
          <ThreadView key={th.root.id} thread={th} users={users} now={now} onReply={onReply} onResolve={onResolve} />
        ))}
        <Composer label={t("conversation.newComment")} submitLabel={t("conversation.comment")} onSubmit={onComment} />
      </section>
      <section aria-labelledby="conv-history">
        <h2 id="conv-history" className="section-title">{t("conversation.history")}</h2>
        <ol className="timeline">
          {(history.data?.events ?? []).map((e) => (
            <li key={e.id} className="timeline__item">
              <span className="timeline__actor">{name(users, e.actor_id)}</span> {describe(e, t)}
              <time className="muted" dateTime={e.at}>
                {" "}
                {formatRelative(locale, new Date(e.at), now)}
              </time>
            </li>
          ))}
        </ol>
      </section>
    </div>
  );
}

function describe(e: HistoryEvent, t: ReturnType<typeof useT>): string {
  switch (e.action) {
    case "review.created":
      return t("history.created");
    case "revision.created":
      return t("history.revision", { number: Number(e.data["number"] ?? 0) });
    case "verdict.recorded":
      return e.data["value"] === "approve" ? t("history.approved") : t("history.changesRequested");
    case "review.state_changed":
      return t("history.state", { state: String(e.data["to"] ?? "") });
    case "comment.created":
      return t("history.commented");
    case "comment.resolved":
      return e.data["resolved"] ? t("history.resolved") : t("history.reopened");
    case "comment.edited":
      return t("history.edited");
    case "comment.deleted":
      return t("history.deletedComment");
    default:
      return e.action;
  }
}
