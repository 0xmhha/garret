import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { ProjectPage } from "./ProjectPage";

it("opens a review into dev when the repository has no main branch", async () => {
  const posts: unknown[] = [];
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    if (init.method === "POST") {
      posts.push({ url, body: JSON.parse(String(init.body)) });
      return json(201, { number: 1 });
    }
    if (url.endsWith("/branches")) return json(200, { branches: { "refs/heads/backup/old": "a".repeat(40), "refs/heads/dev": "b".repeat(40), "refs/heads/feat/fix": "c".repeat(40) } });
    if (url.includes("/reviews")) return json(200, { reviews: [] });
    if (url.includes("/discussions")) return json(200, { discussions: [] });
    if (url === "/api/v1/users") return json(200, { users: [] });
    return json(200, { name: "wemix", description: "", clone_url: "https://h/git/wemix.git", source_path: "/Users/me/wemix" });
  });
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  renderApp([{ path: "/p/:project", element: <ProjectPage /> }, { path: "/p/:project/r/:n", element: <p>review</p> }], "/p/wemix", fetch, storage);
  expect(await screen.findByText(/No open reviews\. Use New review/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "New review" }));
  const form = await screen.findByRole("form", { name: "New review" });
  const into = within(form).getByLabelText("Into") as HTMLSelectElement;
  await waitFor(() => expect(into.value).toBe("refs/heads/dev"));
  fireEvent.change(within(form).getByLabelText("Branch to review"), { target: { value: "refs/heads/feat/fix" } });
  fireEvent.change(within(form).getByRole("textbox", { name: "Title" }), { target: { value: "Croissant EIPs" } });
  fireEvent.click(within(form).getByRole("button", { name: "Open review" }));
  await waitFor(() =>
    expect(posts).toContainEqual({
      url: "/api/v1/projects/wemix/reviews",
      body: { source_branch: "refs/heads/feat/fix", target_branch: "refs/heads/dev", title: "Croissant EIPs" },
    }),
  );
});
