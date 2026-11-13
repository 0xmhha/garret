import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter } from "react-router";
import { createClient } from "./api/client";
import { App, newQueryClient, routes } from "./app/App";
import { pickLocale } from "./i18n/i18n";
import "./styles/tokens.css";
import "./styles/base.css";
import "./ui/ui.css";

const router = createBrowserRouter(routes);
const queryClient = newQueryClient();
const api = createClient({
  fetch: (input, init) => window.fetch(input, init),
  storage: window.localStorage,
  onUnauthenticated: () => {
    queryClient.clear();
    if (window.location.pathname !== "/login") {
      void router.navigate(`/login?next=${encodeURIComponent(window.location.pathname + window.location.search)}`);
    }
  },
});
const locale = pickLocale(navigator.languages);
document.documentElement.lang = locale;

const root = document.getElementById("root");
if (root) {
  createRoot(root).render(
    <StrictMode>
      <App api={api} locale={locale} queryClient={queryClient} router={router} />
    </StrictMode>,
  );
}
