import { Link } from "react-router";
import { useLocale, useT } from "../app/context";
import type { User } from "../app/session";
import { formatRelative } from "../i18n/i18n";
import type { Revision } from "./model";
import { name } from "./Thread";

/** Revisions lists every push with its commits, newest first. */
export function Revisions({ revisions, users, now }: { revisions: Revision[]; users: Map<string, User> | undefined; now: Date }) {
  const t = useT();
  const locale = useLocale();
  return (
    <ol className="revisions">
      {[...revisions].reverse().map((r) => (
        <li key={r.number} className="revisions__item">
          <div className="revisions__head">
            <h2>{t("revisions.title", { number: r.number })}</h2>
            <span className="muted">
              {t("revisions.pushed", { name: name(users, r.pushed_by) })} · <time dateTime={r.pushed_at}>{formatRelative(locale, new Date(r.pushed_at), now)}</time>
            </span>
            <Link className="revisions__open" to={`../files?rev=${r.number}`} relative="path">
              {t("revisions.openFiles")}
            </Link>
          </div>
          <ul className="commits">
            {r.commits.map((c) => (
              <li key={c.sha}>
                <code className="commits__sha">{c.sha.slice(0, 10)}</code> <span>{c.subject}</span>{" "}
                <span className="muted">{c.author_name}</span>
              </li>
            ))}
          </ul>
          {r.commits_omitted ? <p className="muted">{t("revisions.omitted")}</p> : null}
        </li>
      ))}
    </ol>
  );
}
