import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { Admin } from "./Admin";

const admin = { id: "u1", username: "ann", display_name: "Ann", role: "admin", disabled: false, created_at: "2026-11-12T00:00:00Z" };
const bob = { id: "u2", username: "bob", display_name: "Bob", role: "member", disabled: false, created_at: "2026-11-12T00:00:00Z" };
const emptyZip = () => {
  const b = new Uint8Array(22);
  b.set([0x50, 0x4b, 0x05, 0x06]);
  return b;
};

function setup({ me = admin, path = "/admin", zip = emptyZip() } = {}) {
  const calls: { method: string; url: string; body?: unknown }[] = [];
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    const method = init.method ?? "GET";
    calls.push({ method, url, ...(init.body ? { body: JSON.parse(String(init.body)) } : {}) });
    if (url === "/api/v1/me") return json(200, me);
    if (url === "/api/v1/projects" && method === "GET") return json(200, { projects: [{ id: "p1", name: "libfoo", description: "" }] });
    if (url === "/api/v1/admin/invitations" && method === "POST") return json(201, { invitation: {}, link: "https://h:8443/enroll#ca=ab", code: "WXYZ-1234" });
    if (url === "/api/v1/admin/invitations") return json(200, { invitations: [] });
    if (url === "/api/v1/admin/users") return json(200, { users: [admin, bob] });
    if (url.endsWith("/export")) return new Response(zip, { status: 200, headers: { "Content-Disposition": 'attachment; filename="libfoo-export.zip"' } });
    return json(204);
  });
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  renderApp([{ path: "/admin", element: <Admin /> }], path, fetch, storage);
  return calls;
}

afterEach(() => vi.restoreAllMocks());

it("refuses members", async () => {
  setup({ me: bob });
  expect(await screen.findByText("Only administrators can open this page.")).toBeTruthy();
});

it("creates an invitation and shows the command and the code", async () => {
  const calls = setup();
  fireEvent.click(await screen.findByRole("button", { name: "Create invitation" }));
  expect(await screen.findByText("WXYZ-1234")).toBeTruthy();
  expect(screen.getByText("garret enroll 'https://h:8443/enroll#ca=ab'")).toBeTruthy();
  expect(calls).toContainEqual({ method: "POST", url: "/api/v1/admin/invitations", body: { role: "member" } });
});

it("purges only after the project name is typed", async () => {
  const calls = setup();
  fireEvent.click(await screen.findByRole("button", { name: "Purge" }));
  const submit = screen.getByRole("button", { name: "Purge libfoo" });
  expect(submit.hasAttribute("disabled")).toBe(true);
  fireEvent.change(screen.getByRole("textbox", { name: "Type libfoo to confirm" }), { target: { value: "libfoo" } });
  fireEvent.click(submit);
  await waitFor(() => expect(calls).toContainEqual({ method: "POST", url: "/api/v1/admin/projects/libfoo/purge", body: { confirm_name: "libfoo" } }));
});

it("saves a complete export and shows its digest", async () => {
  const create = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:x");
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  setup();
  fireEvent.click(await screen.findByRole("button", { name: "Export" }));
  expect(await screen.findByText("Saved libfoo-export.zip.", { exact: false })).toBeTruthy();
  expect(screen.getByText(/^[0-9a-f]{64}$/)).toBeTruthy();
  expect(create).toHaveBeenCalledOnce();
  expect(click).toHaveBeenCalledOnce();
});

it("does not save an export that was cut short", async () => {
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  setup({ zip: new Uint8Array([0x50, 0x4b, 3, 4, 0, 0]) });
  fireEvent.click(await screen.findByRole("button", { name: "Export" }));
  expect(await screen.findByText(/stopped before it finished/)).toBeTruthy();
  expect(click).not.toHaveBeenCalled();
});

it("fills the erasure form from the link and erases after confirming", async () => {
  const calls = setup({ path: "/admin?erase=comment&id=C1" });
  const id = (await screen.findByRole("textbox", { name: "ID" })) as HTMLInputElement;
  expect(id.value).toBe("C1");
  fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "leaked key" } });
  fireEvent.click(screen.getByRole("button", { name: "Erase permanently" }));
  fireEvent.click(screen.getAllByRole("button", { name: "Erase permanently" })[0]!);
  await waitFor(() => expect(calls).toContainEqual({ method: "POST", url: "/api/v1/admin/erasures", body: { scope: "comment", id: "C1", reason: "leaked key", cleaned_text: "" } }));
});

it("does not offer to disable yourself", async () => {
  setup();
  expect(await screen.findByRole("button", { name: "Disable Bob" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Disable Ann" })).toBeNull();
});
