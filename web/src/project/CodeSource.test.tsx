import { fireEvent, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { CodeSource } from "./CodeSource";

function setup(info: object, branches: Record<string, string>, syncs: object[] = []) {
  const calls: { url: string; body?: unknown }[] = [];
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    if (init.method === "POST") {
      calls.push({ url, body: JSON.parse(String(init.body)) });
      return json(200, syncs.shift());
    }
    if (url.endsWith("/branches")) return json(200, { branches });
    return json(200, { name: "libfoo", description: "", clone_url: "https://h:8443/git/libfoo.git", source_path: "", ...info });
  });
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  renderApp([{ path: "/", element: <CodeSource project="libfoo" /> }], "/", fetch, storage);
  return calls;
}

it("tells how to push when the project has no code", async () => {
  setup({}, {});
  expect(await screen.findByText(/git remote add garret https:\/\/h:8443\/git\/libfoo\.git/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Sync from source" })).toBeNull();
});

it("syncs from the source, reports what changed and overwrites rewritten branches after confirming", async () => {
  const calls = setup({ source_path: "/Users/me/fix" }, { "refs/heads/main": "a".repeat(40) }, [
    { created: ["extra"], updated: ["main"], rewritten: ["topic"], unchanged: [] },
    { created: [], updated: ["topic"], rewritten: [], unchanged: ["main", "extra"] },
  ]);
  expect(await screen.findByText("/Users/me/fix")).toBeTruthy();
  expect(screen.queryByText(/git remote add/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Sync from source" }));
  expect(await screen.findByText("New branches: extra. Updated: main.")).toBeTruthy();
  expect(screen.getByText(/left as they are here: topic/)).toBeTruthy();
  expect(calls).toEqual([{ url: "/api/v1/projects/libfoo/sync", body: { overwrite: false } }]);
  fireEvent.click(screen.getByRole("button", { name: "Overwrite" }));
  fireEvent.click(screen.getByRole("button", { name: "Overwrite" }));
  await waitFor(() => expect(calls.at(-1)).toEqual({ url: "/api/v1/projects/libfoo/sync", body: { overwrite: true } }));
  expect(await screen.findByText("Updated: topic.")).toBeTruthy();
});
