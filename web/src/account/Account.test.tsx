import { fireEvent, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { Account } from "./Account";

const token = { id: "t1", name: "laptop", created_at: "2026-11-12T00:00:00Z", last_used_at: null, revoked_at: null };
const cert = (over: object) => ({ serial: "01", device_name: "mbp", issued_via: "invitation", not_before: "2026-11-12T00:00:00Z", not_after: "2099-01-01T00:00:00Z", revoked_at: null, current: false, ...over });

function setup() {
  const calls: { method: string; url: string; body?: unknown }[] = [];
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    const method = init.method ?? "GET";
    calls.push({ method, url, ...(init.body ? { body: JSON.parse(String(init.body)) } : {}) });
    if (url === "/api/v1/me/tokens" && method === "POST") return json(201, { token: { ...token, id: "t2", name: "ci" }, secret: "garret_pat_secret" });
    if (url === "/api/v1/me/tokens") return json(200, { tokens: [token] });
    if (url === "/api/v1/me/certificates") return json(200, { certificates: [cert({ serial: "01", current: true }), cert({ serial: "02", device_name: "old", revoked_at: "2026-11-12T01:00:00Z" })] });
    return json(204);
  });
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  renderApp([{ path: "/account", element: <Account /> }], "/account", fetch, storage);
  return calls;
}

it("creates a token and shows its secret once", async () => {
  const calls = setup();
  expect(await screen.findByText("laptop")).toBeTruthy();
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: " ci " } });
  fireEvent.click(screen.getByRole("button", { name: "Create token" }));
  expect(await screen.findByText("garret_pat_secret")).toBeTruthy();
  expect(calls).toContainEqual({ method: "POST", url: "/api/v1/me/tokens", body: { name: "ci" } });
  fireEvent.click(screen.getByRole("button", { name: "Done" }));
  expect(screen.queryByText("garret_pat_secret")).toBeNull();
});

it("revokes a token only after confirming", async () => {
  const calls = setup();
  fireEvent.click(await screen.findByRole("button", { name: "Revoke laptop" }));
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
  await waitFor(() => expect(calls).toContainEqual({ method: "DELETE", url: "/api/v1/me/tokens/t1" }));
});

it("warns before revoking this device's certificate and offers nothing for a revoked one", async () => {
  setup();
  expect(await screen.findByText("This device")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Revoke the certificate of old" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Revoke the certificate of mbp" }));
  expect(screen.getByRole("group", { name: /locked out until you enroll it again/ })).toBeTruthy();
});

it("switches the interface language", async () => {
  setup();
  fireEvent.click(await screen.findByRole("radio", { name: "한국어" }));
  expect(await screen.findByRole("heading", { level: 1, name: "계정" })).toBeTruthy();
});
