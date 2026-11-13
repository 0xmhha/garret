import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { json, renderApp } from "../test/render";
import { SignIn, safeNext } from "./SignIn";

const routes = [
  { path: "/login", element: <SignIn /> },
  { path: "/p/libfoo", element: <p>project page</p> },
  { path: "/", element: <p>home</p> },
];

function fill(username: string, password: string) {
  fireEvent.change(screen.getByLabelText(/Username/), { target: { value: username } });
  fireEvent.change(screen.getByLabelText(/Password/), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

describe("SignIn", () => {
  it("stores the CSRF token and returns to the page that sent here", async () => {
    const fetch = vi.fn(async () => json(200, { user: { id: "u1", username: "bob", display_name: "Bob", role: "member" }, csrf_token: "tok" }));
    const { router, api } = renderApp(routes, "/login?next=%2Fp%2Flibfoo", fetch);
    fill("bob", "correct horse battery staple");
    await waitFor(() => expect(router.state.location.pathname).toBe("/p/libfoo"));
    expect(api.hasSession()).toBe(true);
    const [, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({ username: "bob", password: "correct horse battery staple" });
  });

  it("shows one message for any failed login", async () => {
    const fetch = vi.fn(async () => json(401, { error: { code: "unauthenticated", message: "sign in again" } }));
    const { router } = renderApp(routes, "/login", fetch);
    fill("bob", "wrong password here");
    expect((await screen.findByRole("alert")).textContent).toBe("The username or password is not correct.");
    expect(router.state.location.pathname).toBe("/login");
  });

  it("explains a network failure", async () => {
    const fetch = vi.fn(async () => Promise.reject(new TypeError("Failed to fetch")));
    renderApp(routes, "/login", fetch);
    fill("bob", "x");
    expect((await screen.findByRole("alert")).textContent).toContain("cannot be reached");
  });
});

describe("safeNext", () => {
  it.each([
    [null, "/"],
    ["/p/libfoo/r/1?rev=2", "/p/libfoo/r/1?rev=2"],
    ["//evil.example", "/"],
    ["https://evil.example", "/"],
    ["/\\evil.example", "/"],
    ["p/libfoo", "/"],
  ])("%s -> %s", (next, want) => {
    expect(safeNext(next)).toBe(want);
  });
});
