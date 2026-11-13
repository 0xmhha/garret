import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createTranslator, pickLocale } from "./i18n/i18n";

const t = createTranslator(pickLocale(navigator.languages));

function App() {
  return <p>{t("app.loading")}</p>;
}

const root = document.getElementById("root");
if (root) {
  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}
