import { act, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { Shell } from "./Shell";

const routes = [
  { path: "/login", element: <p>login page</p> },
  { path: "/", element: <Shell />, children: [{ index: true, element: <p>projects</p> }, { path: "*", element: <p>inner</p> }] },
];

it("sends a visitor without a session to sign in, keeping the path", async () => {
  const { router } = renderApp(routes, "/p/libfoo/r/3");
  await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  expect(router.state.location.search).toBe("?next=%2Fp%2Flibfoo%2Fr%2F3");
});

it("shows the signed-in user", async () => {
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  const fetch = vi.fn(async () => json(200, { id: "u1", username: "bob", display_name: "Bob", role: "member" }));
  renderApp(routes, "/", fetch, storage);
  expect(await screen.findByText("Signed in as Bob")).toBeTruthy();
  expect(screen.getByText("projects")).toBeTruthy();
});

it("refetches what is on screen whenever the event stream (re)connects, so no change is missed in between", async () => {
  class FakeSocket {
    static all: FakeSocket[] = [];
    onopen: (() => void) | null = null;
    onmessage = null;
    onclose = null;
    constructor() {
      FakeSocket.all.push(this);
    }
    close() {}
  }
  vi.stubGlobal("WebSocket", FakeSocket);
  try {
    const storage = memoryStorage();
    storage.setItem("garret.csrf", "tok");
    const fetch = vi.fn(async () => json(200, { id: "u1", username: "bob", display_name: "Bob", role: "member" }));
    renderApp(routes, "/", fetch, storage);
    await screen.findByText("Signed in as Bob");
    const before = fetch.mock.calls.length;
    await waitFor(() => expect(FakeSocket.all.length).toBeGreaterThan(0));
    act(() => FakeSocket.all.at(-1)!.onopen?.());
    await waitFor(() => expect(fetch.mock.calls.length).toBeGreaterThan(before));
  } finally {
    vi.unstubAllGlobals();
  }
});
