import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { createMemoryRouter, RouterProvider, type RouteObject } from "react-router";
import { vi } from "vitest";
import { createClient, type Storage } from "../api/client";
import { EnvProvider } from "../app/context";

export function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => void m.set(k, v), removeItem: (k) => void m.delete(k) };
}

export function json(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
  });
}

/** renderApp renders routes at path with a fake fetch, in English. */
type FetchFn = (input: string, init: RequestInit) => Promise<Response>;

export function renderApp(routes: RouteObject[], path: string, fetch: FetchFn = vi.fn(async () => json(200, {})), storage = memoryStorage()) {
  const api = createClient({ fetch, storage });
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <EnvProvider api={api} locale="en">
      <QueryClientProvider client={qc}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </EnvProvider>,
  );
  return { ...view, router, api, fetch, storage };
}
