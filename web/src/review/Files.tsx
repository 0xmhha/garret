import { Fragment, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { useT } from "../app/context";
import type { MessageKey } from "../i18n/i18n";
import type { User } from "../app/session";
import { Button } from "../ui/Button";
import { IconCheck, IconComment } from "../ui/icons";
import { Composer } from "./Composer";
import { fileThreads, lineKey, reviewedStore, type DiffFile, type DiffLine, type Revision, type Thread } from "./model";
import { useDiff, useFileDiff } from "./queries";
import { ThreadView } from "./Thread";
import { anchorView, unresolvedByFile, viewKey, viewQuery, viewThreads, type DiffView } from "./views";

/** LineAnchor is where a new code comment goes: the view's commits and one line. */
export interface LineAnchor {
  view: "revision" | "commit" | "range";
  from: string;
  to: string;
  path: string;
  side: string;
  line: number;
}

interface Props {
  project: string;
  number: number;
  revision: Revision;
  view: DiffView;
  threads: Thread[];
  users: Map<string, User> | undefined;
  now: Date;
  onLineComment: (anchor: LineAnchor, body: string, attachments: string[]) => Promise<unknown>;
  onReply: (rootId: string, body: string, attachments: string[]) => Promise<unknown>;
  onResolve: (rootId: string, resolved: boolean) => void;
}

const pathOf = (f: DiffFile) => f.new_path || f.old_path;

/** Files shows the revision's changed files and the selected file's diff. */
export function Files(props: Props) {
  const { project, number, revision, view } = props;
  const t = useT();
  const [params, setParams] = useSearchParams();
  const diff = useDiff(project, number, viewQuery(view, revision));
  const files = diff.data?.files ?? [];
  const selected = files.find((f) => pathOf(f) === params.get("file")) ?? files[0];
  const from = diff.data?.from ?? "";
  const to = diff.data?.to ?? "";
  const scope = viewKey(view, revision.number, from, to);
  const store = useMemo(() => reviewedStore(window.localStorage, project, number, scope), [project, number, scope]);
  const [reviewed, setReviewed] = useState(() => store.all());
  // A new view has its own reviewed marks.
  useEffect(() => setReviewed(store.all()), [store]);
  const threads = useMemo(() => viewThreads(props.threads, view, revision.number, from, to), [props.threads, view, revision.number, from, to]);
  const openByFile = useMemo(() => unresolvedByFile(threads), [threads]);

  const select = (f: DiffFile) => {
    const next = new URLSearchParams(params);
    next.set("file", pathOf(f));
    setParams(next, { replace: true });
  };
  const toggleReviewed = (path: string) => {
    store.set(path, !reviewed.has(path));
    setReviewed(store.all());
  };
  const step = (delta: number) => {
    if (!selected) return;
    const i = files.indexOf(selected) + delta;
    const f = files[i];
    if (f) select(f);
  };

  return (
    <div
      className="files"
      onKeyDown={(e) => {
        if (e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLInputElement) return;
        if (e.key === "]") step(1);
        if (e.key === "[") step(-1);
      }}
    >
      <nav className="files__list" aria-label={t("files.list")}>
        <div className="files__panel">
          <p className="files__progress">{t("files.progress", { done: files.filter((f) => reviewed.has(pathOf(f))).length, total: files.length })}</p>
          <ul>
            {files.map((f) => {
              const p = pathOf(f);
              const done = reviewed.has(p);
              return (
                <li key={p} className={`files__item${f === selected ? " is-selected" : ""}${done ? " is-reviewed" : ""}`}>
                  <button type="button" className="files__name" aria-current={f === selected ? "true" : undefined} onClick={() => select(f)} title={p}>
                    <span className={`status status--${f.status.toLowerCase()}`} aria-label={t(`files.status.${statusKey(f.status)}` as MessageKey)}>
                      {f.status.slice(0, 1)}
                    </span>
                    <span className="files__path">{p}</span>
                    {done ? <IconCheck className="files__check" aria-label={t("files.reviewedMark")} /> : null}
                  </button>
                  {openByFile.get(p) ? (
                    <span className="files__open" role="img" aria-label={t("files.openThreads", { count: openByFile.get(p) ?? 0 })}>
                      <IconComment /> {openByFile.get(p)}
                    </span>
                  ) : null}
                  <span className="files__counts" aria-label={t("files.counts", { added: f.added, deleted: f.deleted })}>
                    <span className="add">+{f.added}</span> <span className="del">−{f.deleted}</span>
                  </span>
                </li>
              );
            })}
          </ul>
          <p className="files__hint">{t("files.keys")}</p>
        </div>
      </nav>
      <section className="files__diff" aria-label={selected ? pathOf(selected) : t("files.none")}>
        {diff.isPending ? <p className="muted pad">{t("app.loading")}</p> : null}
        {diff.data?.bases_differ ? <p className="notice">{t("files.basesDiffer")}</p> : null}
        {view.kind === "pair" ? <p className="notice notice--info">{t("files.pairNoComments")}</p> : null}
        {selected && diff.data ? (
          <FileDiff {...props} threads={threads} from={from} to={to} file={selected} reviewed={reviewed.has(pathOf(selected))} onToggleReviewed={() => toggleReviewed(pathOf(selected))} />
        ) : diff.isSuccess ? (
          <p className="muted pad">{t("files.none")}</p>
        ) : null}
      </section>
    </div>
  );
}

function statusKey(status: string): string {
  const s = status.slice(0, 1).toUpperCase();
  return ({ A: "added", D: "deleted", R: "renamed", C: "copied" } as Record<string, string>)[s] ?? "modified";
}

interface FileProps extends Props {
  from: string;
  to: string;
  file: DiffFile;
  reviewed: boolean;
  onToggleReviewed: () => void;
}

function FileDiff({ project, number, view, from, to, file, threads, users, now, reviewed, onToggleReviewed, onLineComment, onReply, onResolve }: FileProps) {
  const t = useT();
  const path = pathOf(file);
  const kind = anchorView(view);
  const q = useFileDiff(project, number, from, to, file.status.startsWith("A") ? "" : file.old_path, file.status.startsWith("D") ? "" : file.new_path);
  const placed = useMemo(() => fileThreads(threads, path), [threads, path]);
  const [commenting, setCommenting] = useState<string | null>(null);

  return (
    <div className="filediff">
      <header className="filediff__head">
        <h2 className="filediff__path">{file.old_path && file.old_path !== file.new_path && file.new_path ? `${file.old_path} → ${file.new_path}` : path}</h2>
        <label className="filediff__reviewed">
          <input type="checkbox" checked={reviewed} onChange={onToggleReviewed} />
          {t("files.reviewed")}
        </label>
      </header>
      {placed.fileLevel.map((th) => (
        <ThreadView key={th.root.id} thread={th} users={users} now={now} onReply={onReply} onResolve={onResolve} project={project} />
      ))}
      {file.binary ? <p className="muted pad">{t("files.binary")}</p> : null}
      {q.isError ? <p className="muted pad">{t("files.tooLarge")}</p> : null}
      {q.data ? (
        <div className="diff-scroll">
        <table className="diff" aria-label={t("files.diffOf", { path })}>
          <colgroup>
            <col className="diff__col-no" />
            <col className="diff__col-no" />
            <col className="diff__col-gutter" />
            <col />
          </colgroup>
          <tbody>
            {q.data.hunks.map((h, hi) => (
              <Fragment key={hi}>
                <tr className="diff__hunk">
                  <td colSpan={4}>
                    @@ −{h.old_start},{h.old_lines} +{h.new_start},{h.new_lines} @@ {h.section}
                  </td>
                </tr>
                {h.lines.map((l, li) => {
                  const side = l.kind === "deleted" ? "old" : "new";
                  const no = (side === "old" ? l.old : l.new) ?? 0;
                  const key = lineKey(side, no);
                  const here = placed.byLine.get(key) ?? [];
                  return (
                    <Fragment key={li}>
                      <DiffRow line={l} onComment={kind ? () => setCommenting(key) : null} commentLabel={t("files.commentOn", { line: no })} />
                      {here.length > 0 || commenting === key ? (
                        <tr className="diff__threads">
                          <td colSpan={4}>
                            {here.map((th) => (
                              <ThreadView key={th.root.id} thread={th} users={users} now={now} onReply={onReply} onResolve={onResolve} project={project} />
                            ))}
                            {commenting === key && kind ? (
                              <Composer
                                project={project}
                                label={t("files.commentOn", { line: no })}
                                submitLabel={t("conversation.comment")}
                                onCancel={() => setCommenting(null)}
                                onSubmit={async (body, attachments) => {
                                  await onLineComment({ view: kind, from, to, path, side, line: no }, body, attachments);
                                  setCommenting(null);
                                }}
                              />
                            ) : null}
                          </td>
                        </tr>
                      ) : null}
                    </Fragment>
                  );
                })}
              </Fragment>
            ))}
          </tbody>
        </table>
        </div>
      ) : null}
      {q.data ? (
        <div className="filediff__foot">
          <Button variant={reviewed ? "default" : "primary"} onPress={onToggleReviewed}>
            <IconCheck /> {reviewed ? t("files.unmark") : t("files.markReviewed")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function DiffRow({ line, onComment, commentLabel }: { line: DiffLine; onComment: (() => void) | null; commentLabel: string }) {
  const sign = line.kind === "added" ? "+" : line.kind === "deleted" ? "−" : " ";
  return (
    <tr className={`diff__line diff__line--${line.kind}`}>
      <td className="diff__no">{line.old ?? ""}</td>
      <td className="diff__no">{line.new ?? ""}</td>
      <td className="diff__gutter">
        {onComment ? (
          <button type="button" className="diff__add-comment" aria-label={commentLabel} onClick={onComment}>
            <IconComment />
          </button>
        ) : null}
      </td>
      <td className="diff__code">
        <span className="diff__sign" aria-hidden="true">{sign}</span>
        <code>{line.text}</code>
      </td>
    </tr>
  );
}
