import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createBrowserRouter, RouterProvider, type RouteObject } from "react-router";
import type { Client } from "../api/client";
import type { Locale } from "../i18n/i18n";
import { NotFound } from "../routes/NotFound";
import { Projects } from "../routes/Projects";
import { DiscussionPage } from "../project/DiscussionPage";
import { ProjectPage } from "../project/ProjectPage";
import { ReviewPage } from "../review/ReviewPage";
import { Inbox } from "../routes/Inbox";
import { Shell } from "../routes/Shell";
import { SignIn } from "../routes/SignIn";
import { EnvProvider } from "./context";

/** routes is the screen map (docs/specs/web.md 6). */
export const routes: RouteObject[] = [
  { path: "/login", element: <SignIn /> },
  {
    path: "/",
    element: <Shell />,
    children: [
      { index: true, element: <Projects /> },
      { path: "inbox", element: <Inbox /> },
      { path: "p/:project", element: <ProjectPage /> },
      { path: "p/:project/d/:id", element: <DiscussionPage /> },
      { path: "p/:project/r/:number/*", element: <ReviewPage /> },
      { path: "*", element: <NotFound /> },
    ],
  },
];

/** App wires the client, the translator, the query cache and the router. */
export function App({ api, locale, queryClient, router }: { api: Client; locale: Locale; queryClient: QueryClient; router: ReturnType<typeof createBrowserRouter> }) {
  return (
    <EnvProvider api={api} locale={locale}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </EnvProvider>
  );
}

export function newQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { staleTime: 15_000, refetchOnWindowFocus: true } } });
}
