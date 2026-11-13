import { useEffect, useMemo, useState } from "react";
import { Menu, MenuItem, MenuTrigger, Popover } from "react-aria-components";
import { NavLink, Navigate, Route, Routes, useParams, useSearchParams } from "react-router";
import { useT } from "../app/context";
import type { MessageKey } from "../i18n/i18n";
import { useMe } from "../app/session";
import { Button } from "../ui/Button";
import { IconBranch, IconChevronDown } from "../ui/icons";
import { Conversation } from "./Conversation";
import { Files } from "./Files";
import { buildThreads, type Review } from "./model";
import { reviewPath, useComments, useReview, useReviewAction, useUsers } from "./queries";
import { Revisions } from "./Revisions";
import { name } from "./Thread";
import "./review.css";

const shortRef = (ref: string) => ref.replace(/^refs\/heads\//, "");

/** ReviewPage is one review: header, verdicts and the three tabs. */
export function ReviewPage() {
  const t = useT();
  const { project = "", number: numberParam = "" } = useParams();
  const number = Number(numberParam);
  const [params] = useSearchParams();
  const detail = useReview(project, number);
  const users = useUsers();
  const me = useMe();
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(id);
  }, []);

  const revisions = detail.data?.revisions ?? [];
  const latest = revisions.at(-1);
  const wanted = Number(params.get("rev"));
  const revision = revisions.find((r) => r.number === wanted) ?? latest;
  const comments = useComments(project, number, revision?.number ?? 0);
  const threads = useMemo(() => buildThreads(comments.data ?? []), [comments.data]);

  const comment = useReviewAction<{ body: string; anchor?: unknown; reply_to?: string }>(project, number, () => `${reviewPath(project, number)}/comments`, (v) => v);
  const resolve = useReviewAction<{ id: string; resolved: boolean }>(project, number, (v) => `/api/v1/comments/${encodeURIComponent(v.id)}/resolve`, (v) => ({ resolved: v.resolved }));
  const verdict = useReviewAction<"approve" | "request_changes">(project, number, () => `${reviewPath(project, number)}/verdicts`, (value) => ({ value }));
  const setState = useReviewAction<{ from: Review["state"]; to: Review["state"] }>(project, number, () => `${reviewPath(project, number)}/state`, (v) => v);

  if (!Number.isInteger(number) || number < 1) return <Navigate to="/" replace />;
  if (detail.isPending) return <p className="muted pad">{t("app.loading")}</p>;
  if (detail.isError || !detail.data) return <p className="form-error pad" role="alert">{t("review.loadFailed")}</p>;

  const { review, verdicts } = detail.data;
  const home = `/p/${encodeURIComponent(project)}/r/${number}`;
  const mine = me.data?.id === review.author_id;
  const open = review.state === "open";
  const onReply = (rootId: string, body: string) => comment.mutateAsync({ body, reply_to: rootId });
  const onResolve = (id: string, resolved: boolean) => resolve.mutate({ id, resolved });

  return (
    <main className="review">
      <header className="review__header">
        <div className="review__titleline">
          <h1 className="review__title">
            {review.title} <span className="review__number">#{review.number}</span>
          </h1>
          <div className="review__actions">
            {open && !mine ? (
              <>
                <Button onPress={() => verdict.mutate("request_changes")} isDisabled={verdict.isPending}>
                  {t("review.requestChanges")}
                </Button>
                <Button variant="primary" onPress={() => verdict.mutate("approve")} isDisabled={verdict.isPending}>
                  {t("review.approve")}
                </Button>
              </>
            ) : null}
            <MenuTrigger>
              <Button aria-label={t("review.more")}>
                <IconChevronDown />
              </Button>
              <Popover className="menu-popover" placement="bottom end">
                <Menu
                  className="menu"
                  onAction={(key) => setState.mutate({ from: review.state, to: key as Review["state"] })}
                  disabledKeys={open ? ["open"] : ["completed", "abandoned"]}
                >
                  <MenuItem className="menu__item" id="completed">{t("review.complete")}</MenuItem>
                  <MenuItem className="menu__item" id="abandoned">{t("review.abandon")}</MenuItem>
                  <MenuItem className="menu__item" id="open">{t("review.reopen")}</MenuItem>
                </Menu>
              </Popover>
            </MenuTrigger>
          </div>
        </div>
        <p className="review__meta">
          <span className={`state state--${review.state}`}>{t(`review.state.${review.state}` as MessageKey)}</span>
          <span>{t("review.openedBy", { author: name(users.data, review.author_id) })}</span>
          <span className="review__branches">
            <IconBranch /> <code>{shortRef(review.source_branch)}</code> → <code>{shortRef(review.target_branch)}</code>
          </span>
          {verdicts.map((v) => (
            <span key={v.user_id} className={`verdict verdict--${v.value}`}>
              {name(users.data, v.user_id)}: {v.value === "approve" ? t("review.approved") : t("review.changesRequested")}
            </span>
          ))}
        </p>
        {(verdict.isError || setState.isError) ? <p className="form-error" role="alert">{t("review.actionFailed")}</p> : null}
        <nav className="tabs" aria-label={t("review.sections")}>
          <NavLink end to={home} className="tabs__tab">{t("review.tab.conversation")}</NavLink>
          <NavLink to={`${home}/revisions`} className="tabs__tab">{t("review.tab.revisions", { count: revisions.length })}</NavLink>
          <NavLink to={`${home}/files${revision ? `?rev=${revision.number}` : ""}`} className="tabs__tab">{t("review.tab.files")}</NavLink>
        </nav>
      </header>
      <Routes>
        <Route
          index
          element={
            <Conversation
              project={project}
              number={number}
              threads={threads}
              users={users.data}
              now={now}
              onComment={(body) => comment.mutateAsync({ body })}
              onReply={onReply}
              onResolve={onResolve}
            />
          }
        />
        <Route path="revisions" element={<Revisions revisions={revisions} users={users.data} now={now} />} />
        <Route
          path="files"
          element={
            revision ? (
              <div className="files-wrap">
                <RevisionPicker revisions={revisions.map((r) => r.number)} current={revision.number} />
                <Files
                  project={project}
                  number={number}
                  revision={revision}
                  threads={threads}
                  users={users.data}
                  now={now}
                  onReply={onReply}
                  onResolve={onResolve}
                  onLineComment={(a, body) =>
                    comment.mutateAsync({
                      body,
                      anchor: { revision: revision.number, view: "revision", from: revision.base_sha, to: revision.head_sha, path: a.path, side: a.side, start_line: a.line, end_line: a.line },
                    })
                  }
                />
              </div>
            ) : null
          }
        />
      </Routes>
    </main>
  );
}

function RevisionPicker({ revisions, current }: { revisions: number[]; current: number }) {
  const t = useT();
  const [params, setParams] = useSearchParams();
  return (
    <div className="revpicker">
      <label htmlFor="revpicker">{t("files.revision")}</label>
      <select
        id="revpicker"
        value={current}
        onChange={(e) => {
          const next = new URLSearchParams(params);
          next.set("rev", e.target.value);
          next.delete("file");
          setParams(next);
        }}
      >
        {[...revisions].reverse().map((n) => (
          <option key={n} value={n}>
            {t("revisions.title", { number: n })}
          </option>
        ))}
      </select>
    </div>
  );
}
