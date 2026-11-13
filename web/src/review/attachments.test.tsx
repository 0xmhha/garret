import { screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { json, renderApp } from "../test/render";
import { AttachmentList } from "./attachments";

it("shows images inline, other files as download links, and a deleted file as such", async () => {
  const fetch = vi.fn(async (url: string) => {
    if (url.endsWith("/IMG/meta")) return json(200, { id: "IMG", filename: "shot.png", media_type: "image/png", size: 2048, inline: true, created_at: "" });
    if (url.endsWith("/LOG/meta")) return json(200, { id: "LOG", filename: "crash.log", media_type: "text/plain", size: 1_500_000, inline: false, created_at: "" });
    return json(404, { error: { code: "not_found", message: "attachment not found" } });
  });
  renderApp([{ path: "/", element: <AttachmentList ids={["IMG", "LOG", "GONE"]} /> }], "/", fetch);
  const img = (await screen.findByRole("img", { name: "shot.png" })) as HTMLImageElement;
  expect(img.getAttribute("src")).toBe("/api/v1/attachments/IMG");
  const link = await screen.findByRole("link", { name: /crash\.log/ });
  expect(link.getAttribute("href")).toBe("/api/v1/attachments/LOG");
  expect(link.textContent).toContain("1.5 MB");
  expect(await screen.findByText("A deleted file")).toBeTruthy();
});
