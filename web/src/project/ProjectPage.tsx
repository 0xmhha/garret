import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Form } from "react-aria-components";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { keys } from "../api/keys";
import { useApi, useLocale, useT } from "../app/context";
import { formatRelative, type MessageKey } from "../i18n/i18n";
import type { Review } from "../review/model";
import { useUsers } from "../review/queries";
import { name } from "../review/Thread";
import { Button } from "../ui/Button";
import { TextField } from "../ui/TextField";
import { defaultTarget } from "./branches";
import { CodeSource, useBranches } from "./CodeSource";
import "../review/review.css";
import "./project.css";

interface Discussion {
  id: string;
  title: string;
  author_id: string;
  body_doc_id: string;
  updated_at: string;
}

const states = ["open", "completed", "abandoned"] as const;
const shortRef = (r: string) => r.replace(/^refs\/heads\//, "");

/** ProjectPage lists a project's reviews and discussions and opens new ones. */
export function ProjectPage() {
  const { project = "" } = useParams();
  const t = useT();
  const locale = useLocale();
  const api = useApi();
  const users = useUsers();
  const [params, setParams] = useSearchParams();
  const state = (params.get("state") ?? "open") as (typeof states)[number] | "all";
  const base = `/api/v1/projects/${encodeURIComponent(project)}`;
  const reviews = useQuery({
    queryKey: [...keys.reviews(project), state],
    queryFn: ({ signal }) => api.request<{ reviews: Review[] }>("GET", `${base}/reviews${state === "all" ? "" : `?state=${state}`}`, undefined, { signal }),
  });
  const discussions = useQuery({
    queryKey: keys.discussions(project),
    queryFn: ({ signal }) => api.request<{ discussions: Discussion[] }>("GET", `${base}/discussions`, undefined, { signal }),
  });
  const now = new Date();
  const [creating, setCreating] = useState<"review" | "discussion" | null>(null);

  return (
    <main className="page project">
      <h1 className="page__title">{project}</h1>
      <CodeSource project={project} />
      <div className="project__grid">
        <section aria-labelledby="reviews-title">
          <div className="project__head">
            <h2 id="reviews-title" className="section-title">{t("project.reviews")}</h2>
            <nav className="filter" aria-label={t("project.filter")}>
              {[...states, "all" as const].map((s) => (
                <button key={s} type="button" aria-pressed={state === s} onClick={() => setParams(s === "open" ? {} : { state: s })}>
                  {t(`project.state.${s}` as MessageKey)}
                </button>
              ))}
            </nav>
            <Button aria-expanded={creating === "review"} onPress={() => setCreating(creating === "review" ? null : "review")}>
              {t("project.newReview")}
            </Button>
          </div>
          {creating === "review" ? <NewReview project={project} onCancel={() => setCreating(null)} /> : null}
          {reviews.data?.reviews.length === 0 ? <p className="muted">{state === "open" ? t("project.noOpenReviews") : t("project.noReviews")}</p> : null}
          <ul className="list">
            {(reviews.data?.reviews ?? []).map((r) => (
              <li key={r.id} className="list__item">
                <Link to={`/p/${encodeURIComponent(project)}/r/${r.number}`} className="list__title">
                  {r.title} <span className="muted">#{r.number}</span>
                </Link>
                <span className="list__meta">
                  <span className={`state state--${r.state}`}>{t(`review.state.${r.state}` as MessageKey)}</span>
                  <code>{shortRef(r.source_branch)}</code> → <code>{shortRef(r.target_branch)}</code> · {name(users.data, r.author_id)} ·{" "}
                  <time dateTime={r.updated_at}>{formatRelative(locale, new Date(r.updated_at), now)}</time>
                </span>
              </li>
            ))}
          </ul>
        </section>
        <section aria-labelledby="discussions-title">
          <div className="project__head">
            <h2 id="discussions-title" className="section-title">{t("project.discussions")}</h2>
            <Button aria-expanded={creating === "discussion"} onPress={() => setCreating(creating === "discussion" ? null : "discussion")}>
              {t("project.newDiscussion")}
            </Button>
          </div>
          {creating === "discussion" ? <NewDiscussion project={project} onCancel={() => setCreating(null)} /> : null}
          {discussions.data?.discussions.length === 0 ? <p className="muted">{t("project.noDiscussions")}</p> : null}
          <ul className="list">
            {(discussions.data?.discussions ?? []).map((d) => (
              <li key={d.id} className="list__item">
                <Link to={`/p/${encodeURIComponent(project)}/d/${d.id}`} className="list__title">{d.title}</Link>
                <span className="list__meta">{name(users.data, d.author_id)} · <time dateTime={d.updated_at}>{formatRelative(locale, new Date(d.updated_at), now)}</time></span>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </main>
  );
}

function NewReview({ project, onCancel }: { project: string; onCancel: () => void }) {
  const t = useT();
  const api = useApi();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const base = `/api/v1/projects/${encodeURIComponent(project)}`;
  const branches = useBranches(project);
  const [source, setSource] = useState("");
  const [chosenTarget, setTarget] = useState("");
  const [title, setTitle] = useState("");
  const open = useMutation({
    mutationFn: () => api.request<Review>("POST", `${base}/reviews`, { source_branch: source, target_branch: target, title }),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: keys.reviews(project) });
      navigate(`/p/${encodeURIComponent(project)}/r/${r.number}`);
    },
  });
  const list = branches.data ?? [];
  // Until someone picks one, the target is the usual base branch that exists.
  const target = chosenTarget || defaultTarget(list);
  return (
    <div className="newform">
      <Form className="newform__body" aria-label={t("project.newReview")} onSubmit={(e) => { e.preventDefault(); open.mutate(); }}>
        <label className="field">
          <span className="field__label">{t("project.source")}</span>
          <select className="field__input" value={source} onChange={(e) => setSource(e.target.value)} required>
            <option value="" disabled>{t("project.chooseBranch")}</option>
            {list.map((b) => <option key={b} value={b}>{shortRef(b)}</option>)}
          </select>
        </label>
        <label className="field">
          <span className="field__label">{t("project.target")}</span>
          <select className="field__input" value={target} onChange={(e) => setTarget(e.target.value)} required>
            {list.map((b) => <option key={b} value={b}>{shortRef(b)}</option>)}
          </select>
        </label>
        <TextField label={t("project.title")} value={title} onChange={setTitle} isRequired maxLength={200} />
        {open.isError ? <p className="form-error" role="alert">{t("project.openFailed")}</p> : null}
        <div className="newform__actions">
          <Button variant="quiet" onPress={onCancel}>{t("app.cancel")}</Button>
          <Button type="submit" variant="primary" isDisabled={open.isPending || !source || !title.trim()}>{t("project.open")}</Button>
        </div>
      </Form>
    </div>
  );
}

function NewDiscussion({ project, onCancel }: { project: string; onCancel: () => void }) {
  const t = useT();
  const api = useApi();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [title, setTitle] = useState("");
  const create = useMutation({
    mutationFn: () => api.request<Discussion>("POST", `/api/v1/projects/${encodeURIComponent(project)}/discussions`, { title }),
    onSuccess: (d) => {
      void qc.invalidateQueries({ queryKey: keys.discussions(project) });
      navigate(`/p/${encodeURIComponent(project)}/d/${d.id}`);
    },
  });
  return (
    <div className="newform">
      <Form className="newform__body" aria-label={t("project.newDiscussion")} onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
        <TextField label={t("project.title")} value={title} onChange={setTitle} isRequired maxLength={200} />
        {create.isError ? <p className="form-error" role="alert">{t("project.createFailed")}</p> : null}
        <div className="newform__actions">
          <Button variant="quiet" onPress={onCancel}>{t("app.cancel")}</Button>
          <Button type="submit" variant="primary" isDisabled={create.isPending || !title.trim()}>{t("project.create")}</Button>
        </div>
      </Form>
    </div>
  );
}
