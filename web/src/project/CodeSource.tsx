import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { keys } from "../api/keys";
import { useApi, useT } from "../app/context";
import { errorMessage } from "../app/errors";
import { Button } from "../ui/Button";
import { ConfirmButton } from "../ui/ConfirmButton";

/** ProjectInfo is GET /api/v1/projects/{project}. */
export interface ProjectInfo {
  name: string;
  description: string;
  clone_url: string;
  source_path: string;
}

/** SyncResult is POST /api/v1/projects/{project}/sync, by branch name. */
export interface SyncResult {
  created: string[];
  updated: string[];
  rewritten: string[];
  unchanged: string[];
}

const projectPath = (project: string) => `/api/v1/projects/${encodeURIComponent(project)}`;

export function useProjectInfo(project: string) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.project(project), "info"],
    queryFn: ({ signal }) => api.request<ProjectInfo>("GET", projectPath(project), undefined, { signal }),
  });
}

/** useBranches lists the project's branch refs, sorted. */
export function useBranches(project: string) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.project(project), "branches"],
    queryFn: ({ signal }) => api.request<{ branches: Record<string, string> }>("GET", `${projectPath(project)}/branches`, undefined, { signal }),
    select: (r) => Object.keys(r.branches).sort(),
  });
}

/**
 * CodeSource says where the project's code comes from: the repository on
 * the server's machine with a sync button (decisions.md D15), and, while
 * the project has no branches, how to push them.
 */
export function CodeSource({ project }: { project: string }) {
  const t = useT();
  const api = useApi();
  const qc = useQueryClient();
  const info = useProjectInfo(project);
  const branches = useBranches(project);
  const sync = useMutation({
    mutationFn: (overwrite: boolean) => api.request<SyncResult>("POST", `${projectPath(project)}/sync`, { overwrite }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.project(project) }),
  });
  if (!info.data) return null;
  const empty = branches.isSuccess && branches.data.length === 0;
  const r = sync.data;
  return (
    <section className="code-source" aria-labelledby="code-source-title">
      <h2 id="code-source-title" className="visually-hidden">
        {t("source.title")}
      </h2>
      {info.data.source_path ? (
        <div className="code-source__row">
          <span className="muted">{t("source.from")}</span>
          <code className="code-source__path">{info.data.source_path}</code>
          <Button onPress={() => sync.mutate(false)} isDisabled={sync.isPending}>
            {sync.isPending ? t("source.syncing") : t("source.sync")}
          </Button>
        </div>
      ) : null}
      {r ? (
        <div className="code-source__result" role="status">
          <p>{syncSummary(r, t)}</p>
          {r.rewritten.length > 0 ? (
            <div className="code-source__rewritten">
              <p>{t("source.rewritten", { names: r.rewritten.join(", ") })}</p>
              <ConfirmButton label={t("source.overwrite")} prompt={t("source.overwritePrompt", { names: r.rewritten.join(", ") })} onConfirm={() => sync.mutate(true)} />
            </div>
          ) : null}
        </div>
      ) : null}
      {sync.error ? (
        <p className="form-error" role="alert">
          {errorMessage(sync.error, t)}
        </p>
      ) : null}
      {empty ? (
        <div className="code-source__empty">
          <p>{info.data.source_path ? t("source.emptyWithSource") : t("source.empty")}</p>
          <pre className="code-source__commands">
            <code>{`git remote add garret ${info.data.clone_url}\ngit push garret <branch>`}</code>
          </pre>
          <p className="muted">{t("source.tokenHint")}</p>
        </div>
      ) : null}
    </section>
  );
}

type T = ReturnType<typeof useT>;

/** syncSummary is one sentence on what a sync did. */
export function syncSummary(r: SyncResult, t: T): string {
  const parts: string[] = [];
  if (r.created.length > 0) parts.push(t("source.created", { names: r.created.join(", ") }));
  if (r.updated.length > 0) parts.push(t("source.updated", { names: r.updated.join(", ") }));
  if (parts.length === 0) return t("source.nothingNew");
  return parts.join(" ");
}
