import { fireEvent, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { json, memoryStorage, renderApp } from "../test/render";
import { maxAttachmentBytes } from "./attachments";
import { Composer } from "./Composer";

function setup() {
  const calls: { method: string; url: string }[] = [];
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    calls.push({ method: init.method ?? "GET", url });
    if (init.method === "POST") return json(201, { id: "ATT1", filename: "crash.log", media_type: "text/plain", size: 5, inline: false, created_at: "" });
    return json(204);
  });
  const storage = memoryStorage();
  storage.setItem("garret.csrf", "tok");
  const onSubmit = vi.fn(async () => undefined);
  renderApp([{ path: "/", element: <Composer label="New comment" submitLabel="Comment" project="lib foo" onSubmit={onSubmit} /> }], "/", fetch, storage);
  return { calls, onSubmit };
}

const choose = async (file: File) => {
  await screen.findByRole("button", { name: "Attach files" });
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(input, { target: { files: [file] } });
};

it("uploads a chosen file at once and sends its ID with the comment", async () => {
  const { calls, onSubmit } = setup();
  await choose(new File(["panic"], "crash.log"));
  await waitFor(() => expect(calls).toContainEqual({ method: "POST", url: "/api/v1/projects/lib%20foo/attachments" }));
  expect(await screen.findByText("crash.log")).toBeTruthy();
  fireEvent.change(document.querySelector("textarea")!, { target: { value: "Log attached." } });
  await waitFor(() => expect(screen.getByRole("button", { name: "Comment" }).hasAttribute("disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Comment" }));
  await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("Log attached.", ["ATT1"]));
  await waitFor(() => expect(screen.queryByText("crash.log")).toBeNull());
});

it("refuses a file over the limit without uploading it", async () => {
  const { calls } = setup();
  const big = new File(["x"], "core.dump");
  Object.defineProperty(big, "size", { value: maxAttachmentBytes + 1 });
  await choose(big);
  expect(await screen.findByText("Over 25 MiB, the limit")).toBeTruthy();
  expect(calls.some((c) => c.method === "POST")).toBe(false);
});

it("deletes an uploaded file when it is removed", async () => {
  const { calls } = setup();
  await choose(new File(["panic"], "crash.log"));
  await screen.findByText("5 bytes");
  fireEvent.click(screen.getByRole("button", { name: "Remove crash.log" }));
  await waitFor(() => expect(calls).toContainEqual({ method: "DELETE", url: "/api/v1/attachments/ATT1" }));
  expect(screen.queryByText("crash.log")).toBeNull();
});
