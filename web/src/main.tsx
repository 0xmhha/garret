import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter } from "react-router";
import { createClient } from "./api/client";
import { App, newQueryClient, routes } from "./app/App";
import { localeKey, resolveLocale } from "./account/model";
import type { Locale } from "./i18n/i18n";
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
function storedLocale(): string | null {
  try {
    return window.localStorage.getItem(localeKey);
  } catch {
    return null; // Storage blocked: follow the browser.
  }
}

function keepLocale(next: Locale) {
  document.documentElement.lang = next;
  try {
    window.localStorage.setItem(localeKey, next);
  } catch {
    // Storage blocked: the choice lasts until the tab closes.
  }
}

const locale = resolveLocale(storedLocale(), navigator.languages);
document.documentElement.lang = locale;

const root = document.getElementById("root");
if (root) {
  createRoot(root).render(
    <StrictMode>
      <App api={api} locale={locale} onLocaleChange={keepLocale} queryClient={queryClient} router={router} />
    </StrictMode>,
  );
}
