import { Link, Navigate, Outlet, useLocation, useNavigate } from "react-router";
import { useApi, useT } from "../app/context";
import { useMe, useSignOut } from "../app/session";
import { Button } from "../ui/Button";
import { IconSignOut } from "../ui/icons";
import "./Shell.css";

/** Shell is the signed-in frame: the top bar and the current screen. */
export function Shell() {
  const api = useApi();
  const t = useT();
  const location = useLocation();
  const navigate = useNavigate();
  const me = useMe();
  const signOut = useSignOut();

  if (!api.hasSession()) {
    return <Navigate to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} replace />;
  }

  return (
    <div className="shell">
      <header className="topbar">
        <Link className="topbar__brand" to="/">
          garret
        </Link>
        <nav className="topbar__nav" aria-label={t("shell.projects")}>
          <Link to="/" aria-current={location.pathname === "/" ? "page" : undefined}>
            {t("shell.projects")}
          </Link>
        </nav>
        <div className="topbar__account">
          {me.data ? <span className="topbar__user">{t("shell.account", { name: me.data.display_name })}</span> : null}
          <Button variant="quiet" aria-label={t("shell.signOut")} onPress={() => signOut.mutate(undefined, { onSettled: () => navigate("/login") })}>
            <IconSignOut />
          </Button>
        </div>
      </header>
      <div className="shell__body">
        <Outlet />
      </div>
    </div>
  );
}
