import { Link } from "react-router";
import { useT } from "../app/context";

/** NotFound is shown for any path the interface does not know. */
export function NotFound() {
  const t = useT();
  return (
    <main className="page">
      <h1 className="page__title">{t("notFound.title")}</h1>
      <Link to="/">{t("notFound.back")}</Link>
    </main>
  );
}
