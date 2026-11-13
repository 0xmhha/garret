import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { keys } from "../api/keys";
import { useApi, useT } from "../app/context";
import "./Projects.css";

interface Project {
  id: string;
  name: string;
  description: string;
  clone_url: string;
}

/** Projects lists every project; members read all of them. */
export function Projects() {
  const api = useApi();
  const t = useT();
  const q = useQuery({
    queryKey: keys.projects(),
    queryFn: ({ signal }) => api.request<{ projects: Project[] }>("GET", "/api/v1/projects", undefined, { signal }),
  });

  return (
    <main className="page">
      <h1 className="page__title">{t("projects.title")}</h1>
      {q.isPending ? <p className="muted">{t("app.loading")}</p> : null}
      {q.isError ? <p role="alert" className="form-error">{t("projects.loadFailed")}</p> : null}
      {q.data && q.data.projects.length === 0 ? <p className="muted">{t("projects.empty")}</p> : null}
      {q.data && q.data.projects.length > 0 ? (
        <ul className="project-list">
          {q.data.projects.map((p) => (
            <li key={p.id} className="project-list__item">
              <Link className="project-list__name" to={`/p/${encodeURIComponent(p.name)}`}>
                {p.name}
              </Link>
              {p.description ? <p className="muted">{p.description}</p> : null}
              <code className="project-list__clone">{p.clone_url}</code>
            </li>
          ))}
        </ul>
      ) : null}
    </main>
  );
}
