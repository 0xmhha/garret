import { useState } from "react";
import { Link } from "react-router";
import { eraseHref } from "../admin/model";
import { useLocale, useT } from "../app/context";
import { useMe, type User } from "../app/session";
import { formatRelative } from "../i18n/i18n";
import { Button } from "../ui/Button";
import { Markdown } from "../ui/Markdown";
import { Composer } from "./Composer";
import type { Comment, Thread } from "./model";

interface Props {
  thread: Thread;
  users: Map<string, User> | undefined;
  now: Date;
  onReply: (rootId: string, body: string) => Promise<unknown>;
  onResolve: (rootId: string, resolved: boolean) => void;
}

/** ThreadView shows a thread, its replies, and reply and resolve actions. */
export function ThreadView({ thread, users, now, onReply, onResolve }: Props) {
  const t = useT();
  const [replying, setReplying] = useState(false);
  const resolved = thread.root.resolved;
  return (
    <article className={`thread${resolved ? " thread--resolved" : ""}`} aria-label={t("thread.label", { author: name(users, thread.root.author_id) })}>
      {[thread.root, ...thread.replies].map((c) => (
        <CommentView key={c.id} comment={c} users={users} now={now} />
      ))}
      <div className="thread__actions">
        {replying ? (
          <Composer
            label={t("thread.reply")}
            submitLabel={t("thread.reply")}
            onCancel={() => setReplying(false)}
            onSubmit={async (body) => {
              await onReply(thread.root.id, body);
              setReplying(false);
            }}
          />
        ) : (
          <>
            <Button variant="quiet" onPress={() => setReplying(true)}>
              {t("thread.reply")}
            </Button>
            <Button variant="quiet" onPress={() => onResolve(thread.root.id, !resolved)}>
              {resolved ? t("thread.reopen") : t("thread.resolve")}
            </Button>
            {resolved ? <span className="thread__badge">{t("thread.resolved")}</span> : null}
          </>
        )}
      </div>
    </article>
  );
}

function CommentView({ comment, users, now }: { comment: Comment; users: Map<string, User> | undefined; now: Date }) {
  const t = useT();
  const locale = useLocale();
  const me = useMe();
  return (
    <div className="comment">
      <div className="comment__meta">
        <span className="comment__author">{name(users, comment.author_id)}</span>
        <time dateTime={comment.created_at}>{formatRelative(locale, new Date(comment.created_at), now)}</time>
        {comment.version > 1 ? <span className="muted">{t("comment.edited")}</span> : null}
        {me.data?.role === "admin" && !comment.deleted ? (
          <Link className="comment__erase" to={eraseHref("comment", comment.id)}>
            {t("admin.eraseLink")}
          </Link>
        ) : null}
      </div>
      {comment.deleted ? <p className="muted">{t("comment.deleted")}</p> : <Markdown source={comment.body} />}
    </div>
  );
}

export function name(users: Map<string, User> | undefined, id: string): string {
  return users?.get(id)?.display_name ?? id.slice(0, 8);
}
