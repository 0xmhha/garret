import { fireEvent, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { ReviewPage } from "./ReviewPage";

const detail = {
  review: { id: "R1", number: 1, title: "Bound the length field", source_branch: "refs/heads/fix", target_branch: "refs/heads/main", state: "open", author_id: "bob", description_doc_id: "D", created_at: "2026-11-13T00:00:00Z", updated_at: "2026-11-13T00:00:00Z" },
  revisions: [{ number: 1, base_sha: "a".repeat(40), head_sha: "b".repeat(40), commits_omitted: false, pushed_by: "bob", pushed_at: "2026-11-13T00:00:00Z", commits: [] }],
  verdicts: [],
};

function server(posts: unknown[]) {
  return vi.fn(async (url: string, init: RequestInit) => {
    if (init.method === "POST") {
      posts.push({ url, body: JSON.parse(String(init.body)) });
      return json(201, {});
    }
    if (url.endsWith("/reviews/1")) return json(200, detail);
    if (url === "/api/v1/me") return json(200, { id: "carol", username: "carol", display_name: "Carol", role: "member" });
    if (url === "/api/v1/users") return json(200, { users: [{ id: "bob", username: "bob", display_name: "Bob", role: "member" }] });
    if (url.includes("/comments")) return json(200, { comments: [{ id: "c1", thread_root_id: "", author_id: "bob", anchor: null, resolved: false, version: 1, body: "Please add a **test**.", deleted: false, attachments: [], created_at: "2026-11-13T00:00:00Z" }] });
    if (url.includes("/history")) return json(200, { events: [], next_cursor: "" });
    return json(404, { error: { code: "not_found", message: "not found" } });
  });
}

it("shows the review, its threads, and records a verdict", async () => {
  const posts: unknown[] = [];
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  renderApp([{ path: "/p/:project/r/:number/*", element: <ReviewPage /> }], "/p/libfoo/r/1", server(posts), storage);
  expect(await screen.findByRole("heading", { name: /Bound the length field/ })).toBeTruthy();
  expect((await screen.findByText("test")).tagName).toBe("STRONG");
  expect(screen.getByText("Opened by Bob")).toBeTruthy();
  fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
  await waitFor(() => expect(posts).toContainEqual({ url: "/api/v1/projects/libfoo/reviews/1/verdicts", body: { value: "approve" } }));
});
