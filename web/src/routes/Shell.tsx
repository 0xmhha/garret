import { Link, Navigate, Outlet, useLocation, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useApi, useT } from "../app/context";
import { connectEvents, invalidationsFor } from "../events/events";
import { useInbox } from "./Inbox";
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
  const qc = useQueryClient();
  const unread = useInbox(true);
  const signedIn = api.hasSession();

  // One event stream per tab: each event refetches what it made stale.
  useEffect(() => {
    if (!signedIn || typeof window.WebSocket === "undefined") return;
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    return connectEvents({
      url: `${proto}//${window.location.host}/api/v1/ws`,
      WebSocket: window.WebSocket as never,
      onEvent: (ev) => {
        for (const key of invalidationsFor(ev)) void qc.invalidateQueries({ queryKey: key });
      },
      onEnded: () => navigate("/login"),
    });
  }, [signedIn, qc, navigate]);

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
          <Link to="/inbox" aria-current={location.pathname === "/inbox" ? "page" : undefined}>
            {t("shell.inbox")}
            {unread.data && unread.data.length > 0 ? <span className="topbar__count" aria-label={t("shell.unread", { count: unread.data.length })}>{unread.data.length}</span> : null}
          </Link>
          {me.data?.role === "admin" ? (
            <Link to="/admin" aria-current={location.pathname === "/admin" ? "page" : undefined}>
              {t("shell.admin")}
            </Link>
          ) : null}
        </nav>
        <div className="topbar__account">
          {me.data ? (
            <Link className="topbar__user" to="/account" aria-current={location.pathname === "/account" ? "page" : undefined} title={t("shell.accountSettings")}>
              {t("shell.account", { name: me.data.display_name })}
            </Link>
          ) : null}
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
