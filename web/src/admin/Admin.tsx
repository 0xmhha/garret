import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Form } from "react-aria-components";
import { useSearchParams } from "react-router";
import { keys } from "../api/keys";
import { invitationStatus, type Invitation } from "../account/model";
import { useApi, useLocale, useT } from "../app/context";
import { errorMessage } from "../app/errors";
import { useMe, type User } from "../app/session";
import { formatDate } from "../i18n/i18n";
import { Button } from "../ui/Button";
import { ConfirmButton } from "../ui/ConfirmButton";
import { OneTimeValue } from "../ui/OneTimeValue";
import { TextField } from "../ui/TextField";
import { hex, zipComplete, type EraseScope } from "./model";
import "../account/settings.css";

interface Project {
  id: string;
  name: string;
  description: string;
}

interface AdminUser extends User {
  disabled: boolean;
  created_at: string;
}

/** Admin is the administrator's screen: projects, invitations, users and erasure. */
export function Admin() {
  const t = useT();
  const me = useMe();
  if (me.isPending) return <main className="page settings"><p className="muted">{t("app.loading")}</p></main>;
  if (me.data?.role !== "admin") {
    return (
      <main className="page settings">
        <h1 className="page__title">{t("admin.title")}</h1>
        <p role="alert">{t("admin.forbidden")}</p>
      </main>
    );
  }
  return (
    <main className="page settings">
      <h1 className="page__title">{t("admin.title")}</h1>
      <Projects />
      <Invitations />
      <Users self={me.data.id} />
      <Erasure />
    </main>
  );
}

function Projects() {
  const t = useT();
  const api = useApi();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const list = useQuery({
    queryKey: keys.projects(),
    queryFn: ({ signal }) => api.request<{ projects: Project[] }>("GET", "/api/v1/projects", undefined, { signal }),
    select: (r) => r.projects,
  });
  const create = useMutation({
    mutationFn: () => api.request("POST", "/api/v1/projects", { name: name.trim(), description: description.trim() }),
    onSuccess: () => {
      setName("");
      setDescription("");
      void qc.invalidateQueries({ queryKey: keys.projects() });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <section className="settings__section" aria-labelledby="projects-title">
      <h2 id="projects-title">{t("admin.projects")}</h2>
      <p className="muted">{t("admin.projectsLede")}</p>
      <Form className="stack-form" onSubmit={onSubmit}>
        <TextField label={t("admin.projectName")} value={name} onChange={setName} isRequired description={t("admin.projectNameHint")} />
        <TextField label={t("admin.projectDescription")} value={description} onChange={setDescription} />
        <div>
          <Button type="submit" isDisabled={create.isPending || name.trim() === ""}>
            {t("admin.projectCreate")}
          </Button>
        </div>
        {create.error ? <p className="form-error" role="alert">{errorMessage(create.error, t)}</p> : null}
      </Form>
      {list.isError ? <p className="form-error" role="alert">{errorMessage(list.error, t)}</p> : null}
      {list.data && list.data.length > 0 ? (
        <ul className="list settings__list">
          {list.data.map((p) => (
            <ProjectRow key={p.id} project={p} />
          ))}
        </ul>
      ) : null}
    </section>
  );
}

type ExportResult = { name: string; digest: string };

function ProjectRow({ project }: { project: Project }) {
  const t = useT();
  const api = useApi();
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState("");
  const [purging, setPurging] = useState(false);
  const path = `/api/v1/admin/projects/${encodeURIComponent(project.name)}`;
  const exp = useMutation({
    mutationFn: async (): Promise<ExportResult> => {
      const { blob, name } = await api.download(`${path}/export`);
      const bytes = new Uint8Array(await blob.arrayBuffer());
      if (!zipComplete(bytes)) throw new IncompleteExport();
      const digest = hex(await crypto.subtle.digest("SHA-256", bytes));
      save(blob, name || `${project.name}-export.zip`);
      return { name: name || `${project.name}-export.zip`, digest };
    },
  });
  const purge = useMutation({
    mutationFn: () => api.request("POST", `${path}/purge`, { confirm_name: confirm }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.projects() }),
  });

  return (
    <li className="list__item project-admin">
      <div className="project-admin__head">
        <span className="list__title">{project.name}</span>
        <div className="project-admin__actions">
          <Button isDisabled={exp.isPending} onPress={() => exp.mutate()}>
            {exp.isPending ? t("admin.exporting") : t("admin.export")}
          </Button>
          <Button variant="danger" aria-expanded={purging} onPress={() => setPurging(!purging)}>
            {t("admin.purge")}
          </Button>
        </div>
      </div>
      {project.description ? <p className="muted">{project.description}</p> : null}
      {exp.data ? (
        <p className="project-admin__done" role="status">
          {t("admin.exported", { name: exp.data.name })} <span className="muted">SHA-256</span> <code className="digest">{exp.data.digest}</code>
        </p>
      ) : null}
      {exp.error ? <p className="form-error" role="alert">{exp.error instanceof IncompleteExport ? t("admin.exportIncomplete") : errorMessage(exp.error, t)}</p> : null}
      {purging ? (
        <Form
          className="danger-zone"
          onSubmit={(e) => {
            e.preventDefault();
            purge.mutate();
          }}
        >
          <p>{t("admin.purgeWarning", { name: project.name })}</p>
          <TextField label={t("admin.purgeConfirm", { name: project.name })} value={confirm} onChange={setConfirm} autoComplete="off" />
          <div>
            <Button type="submit" variant="danger" isDisabled={confirm !== project.name || purge.isPending}>
              {t("admin.purgeSubmit", { name: project.name })}
            </Button>
          </div>
          {purge.error ? <p className="form-error" role="alert">{errorMessage(purge.error, t)}</p> : null}
        </Form>
      ) : null}
    </li>
  );
}

class IncompleteExport extends Error {}

// save hands the archive to the browser as a download.
function save(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  // The click starts the download synchronously; the URL is not needed after.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

const roles = ["member", "admin"] as const;

function Invitations() {
  const t = useT();
  const locale = useLocale();
  const api = useApi();
  const qc = useQueryClient();
  const [role, setRole] = useState<(typeof roles)[number]>("member");
  const list = useQuery({
    queryKey: keys.invitations(),
    queryFn: ({ signal }) => api.request<{ invitations: Invitation[] }>("GET", "/api/v1/admin/invitations", undefined, { signal }),
    select: (r) => r.invitations,
  });
  const users = useQuery({
    queryKey: keys.adminUsers(),
    queryFn: ({ signal }) => api.request<{ users: AdminUser[] }>("GET", "/api/v1/admin/users", undefined, { signal }),
    select: (r) => new Map(r.users.map((u) => [u.id, u])),
  });
  const refresh = () => qc.invalidateQueries({ queryKey: keys.invitations() });
  const create = useMutation({
    mutationFn: () => api.request<{ invitation: Invitation; link: string; code: string }>("POST", "/api/v1/admin/invitations", { role }),
    onSuccess: () => void refresh(),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api.request("DELETE", `/api/v1/admin/invitations/${encodeURIComponent(id)}`),
    onSettled: refresh,
  });
  const now = new Date();

  return (
    <section className="settings__section" aria-labelledby="invitations-title">
      <h2 id="invitations-title">{t("admin.invitations")}</h2>
      <p className="muted">{t("admin.invitationsLede")}</p>
      <Form
        className="inline-form"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <label className="field">
          <span className="field__label">{t("admin.role")}</span>
          <select className="field__input field__select" value={role} onChange={(e) => setRole(e.target.value as (typeof roles)[number])}>
            {roles.map((r) => (
              <option key={r} value={r}>
                {t(`role.${r}`)}
              </option>
            ))}
          </select>
        </label>
        <Button type="submit" isDisabled={create.isPending}>
          {t("admin.invite")}
        </Button>
      </Form>
      {create.data ? (
        <div className="reveal" role="status">
          <p>{t("admin.invited")}</p>
          <OneTimeValue label={t("admin.inviteLink")} value={`garret enroll '${create.data.link}'`} />
          <OneTimeValue label={t("admin.inviteCode")} value={create.data.code} />
          <Button variant="quiet" onPress={() => create.reset()}>
            {t("app.done")}
          </Button>
        </div>
      ) : null}
      {create.error ? <p className="form-error" role="alert">{errorMessage(create.error, t)}</p> : null}
      {revoke.error ? <p className="form-error" role="alert">{errorMessage(revoke.error, t)}</p> : null}
      {list.isError ? <p className="form-error" role="alert">{errorMessage(list.error, t)}</p> : null}
      {list.data && list.data.length > 0 ? (
        <table className="table">
          <thead>
            <tr>
              <th scope="col">{t("admin.role")}</th>
              <th scope="col">{t("table.created")}</th>
              <th scope="col">{t("admin.expires")}</th>
              <th scope="col">{t("table.status")}</th>
              <th scope="col">
                <span className="visually-hidden">{t("table.actions")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {list.data.map((inv) => {
              const status = invitationStatus(inv, now);
              const by = inv.used_by ? (users.data?.get(inv.used_by)?.display_name ?? inv.used_by.slice(0, 8)) : "";
              return (
                <tr key={inv.id}>
                  <td>
                    <span className="table__strong">{t(`role.${inv.role}`)}</span>
                  </td>
                  <td>
                    <time dateTime={inv.created_at}>{formatDate(locale, new Date(inv.created_at))}</time>
                  </td>
                  <td>
                    <time dateTime={inv.expires_at}>{formatDate(locale, new Date(inv.expires_at))}</time>
                  </td>
                  <td>
                    <span className={`pill pill--${status}`}>{t(`status.${status}`)}</span>
                    {by ? <span className="table__sub">{t("admin.usedBy", { name: by })}</span> : null}
                  </td>
                  <td className="table__action">
                    {status === "pending" ? (
                      <ConfirmButton label={t("app.revoke")} prompt={t("admin.inviteRevokePrompt")} isDisabled={revoke.isPending} onConfirm={() => revoke.mutate(inv.id)} />
                    ) : null}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
    </section>
  );
}

function Users({ self }: { self: string }) {
  const t = useT();
  const locale = useLocale();
  const api = useApi();
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: keys.adminUsers(),
    queryFn: ({ signal }) => api.request<{ users: AdminUser[] }>("GET", "/api/v1/admin/users", undefined, { signal }),
    select: (r) => r.users,
  });
  const disable = useMutation({
    mutationFn: (id: string) => api.request("POST", `/api/v1/admin/users/${encodeURIComponent(id)}/disable`),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: keys.adminUsers() });
      void qc.invalidateQueries({ queryKey: keys.users() });
    },
  });

  return (
    <section className="settings__section" aria-labelledby="users-title">
      <h2 id="users-title">{t("admin.users")}</h2>
      <p className="muted">{t("admin.usersLede")}</p>
      {disable.error ? <p className="form-error" role="alert">{errorMessage(disable.error, t)}</p> : null}
      {list.isError ? <p className="form-error" role="alert">{errorMessage(list.error, t)}</p> : null}
      {list.data && list.data.length > 0 ? (
        <table className="table">
          <thead>
            <tr>
              <th scope="col">{t("admin.user")}</th>
              <th scope="col">{t("admin.role")}</th>
              <th scope="col">{t("admin.joined")}</th>
              <th scope="col">{t("table.status")}</th>
              <th scope="col">
                <span className="visually-hidden">{t("table.actions")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {list.data.map((u) => {
              const status = u.disabled ? "disabled" : "active";
              return (
                <tr key={u.id}>
                  <td>
                    <span className="table__strong">{u.display_name}</span>
                    <span className="table__sub">{u.username}</span>
                  </td>
                  <td>{t(`role.${u.role}`)}</td>
                  <td>
                    <time dateTime={u.created_at}>{formatDate(locale, new Date(u.created_at))}</time>
                  </td>
                  <td>
                    <span className={`pill pill--${status}`}>{t(`status.${status}`)}</span>
                  </td>
                  <td className="table__action">
                    {!u.disabled && u.id !== self ? (
                      <ConfirmButton
                        label={t("admin.disable")}
                        aria-label={t("admin.disableLabel", { name: u.display_name })}
                        prompt={t("admin.disablePrompt", { name: u.display_name })}
                        isDisabled={disable.isPending}
                        onConfirm={() => disable.mutate(u.id)}
                      />
                    ) : null}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
    </section>
  );
}

const scopes: readonly EraseScope[] = ["comment", "attachment", "collab_document"];
type Scope = EraseScope;

function isScope(s: string | null): s is Scope {
  return scopes.includes(s as Scope);
}

/** Erasure removes a comment, an attachment or a document's history for good. */
function Erasure() {
  const t = useT();
  const api = useApi();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const initialScope = params.get("erase");
  const [scope, setScope] = useState<Scope>(isScope(initialScope) ? initialScope : "comment");
  const [id, setId] = useState(params.get("id") ?? "");
  const [reason, setReason] = useState("");
  const [cleaned, setCleaned] = useState("");
  const erase = useMutation({
    mutationFn: () =>
      api.request("POST", "/api/v1/admin/erasures", { scope, id: id.trim(), reason: reason.trim(), cleaned_text: scope === "collab_document" ? cleaned : "" }),
    onSuccess: () => {
      setId("");
      setReason("");
      setCleaned("");
      // Erased content can be on any screen; everything is refetched.
      void qc.invalidateQueries();
    },
  });
  const ready = id.trim() !== "" && reason.trim() !== "";
  const section = useRef<HTMLElement>(null);
  // Arriving from an "Erase" link on a comment or document: bring the form into view.
  useEffect(() => {
    if (isScope(initialScope)) section.current?.scrollIntoView({ block: "start" });
  }, [initialScope]);

  return (
    <section ref={section} className="settings__section" id="erase" aria-labelledby="erase-title">
      <h2 id="erase-title">{t("admin.erase")}</h2>
      <p className="muted">{t("admin.eraseLede")}</p>
      <Form className="stack-form danger-zone" onSubmit={(e) => e.preventDefault()}>
        <label className="field">
          <span className="field__label">{t("admin.eraseScope")}</span>
          <select className="field__input field__select" value={scope} onChange={(e) => isScope(e.target.value) && setScope(e.target.value)}>
            {scopes.map((s) => (
              <option key={s} value={s}>
                {t(`admin.scope.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <TextField label={t("admin.eraseId")} value={id} onChange={setId} isRequired autoComplete="off" spellCheck="false" />
        <TextField label={t("admin.eraseReason")} value={reason} onChange={setReason} isRequired description={t("admin.eraseReasonHint")} />
        {scope === "collab_document" ? (
          <label className="field">
            <span className="field__label">{t("admin.eraseCleaned")}</span>
            <textarea className="field__input field__textarea" value={cleaned} onChange={(e) => setCleaned(e.target.value)} rows={5} />
            <span className="field__hint">{t("admin.eraseCleanedHint")}</span>
          </label>
        ) : null}
        <div>
          <ConfirmButton label={t("admin.eraseSubmit")} prompt={t("admin.erasePrompt")} isDisabled={!ready || erase.isPending} onConfirm={() => erase.mutate()} />
        </div>
        {erase.isSuccess ? <p role="status">{t("admin.erased")}</p> : null}
        {erase.error ? <p className="form-error" role="alert">{errorMessage(erase.error, t)}</p> : null}
      </Form>
    </section>
  );
}
