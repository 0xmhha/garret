import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { Inbox, link, type Note } from "./Inbox";

const note = (over: Partial<Note>): Note => ({ id: "n1", kind: "mention", subject_kind: "comment", created_at: "2026-11-13T00:00:00Z", read_at: null, project: "lib foo", ...over });

describe("link", () => {
  it.each([
    [note({ review: 12 }), "/p/lib%20foo/r/12"],
    [note({ discussion: "D/1" }), "/p/lib%20foo/d/D%2F1"],
    [note({}), ""],
  ])("%j -> %s", (n, want) => {
    expect(link(n)).toBe(want);
  });
});

it("lists notifications and marks them all read", async () => {
  const posts: unknown[] = [];
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    if (init.method === "POST") {
      posts.push({ url, body: JSON.parse(String(init.body)) });
      return json(204);
    }
    return json(200, { notifications: [note({ id: "a", review: 3 }), note({ id: "b", read_at: "2026-11-13T01:00:00Z", discussion: "D1" })], next_cursor: "" });
  });
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  renderApp([{ path: "/inbox", element: <Inbox /> }], "/inbox", fetch, storage);
  expect(await screen.findByText("You were mentioned", { selector: "a[href='/p/lib%20foo/r/3']" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Mark all as read" }));
  await waitFor(() => expect(posts).toEqual([{ url: "/api/v1/inbox/read", body: { ids: ["a"] } }]));
});
