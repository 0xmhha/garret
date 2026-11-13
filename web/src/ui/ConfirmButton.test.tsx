import { fireEvent, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { renderApp } from "../test/render";
import { ConfirmButton } from "./ConfirmButton";

function setup() {
  const onConfirm = vi.fn();
  renderApp([{ path: "/", element: <ConfirmButton label="Revoke" prompt="Revoke this token?" onConfirm={onConfirm} /> }], "/");
  return onConfirm;
}

it("asks before it acts and confirms on the second button", async () => {
  const onConfirm = setup();
  fireEvent.click(await screen.findByRole("button", { name: "Revoke" }));
  expect(onConfirm).not.toHaveBeenCalled();
  expect(screen.getByRole("group", { name: "Revoke this token?" })).toBeTruthy();
  expect(document.activeElement?.textContent).toBe("Cancel");
  fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
  expect(onConfirm).toHaveBeenCalledOnce();
  expect(screen.queryByRole("group")).toBeNull();
});

it("cancels without acting", async () => {
  const onConfirm = setup();
  fireEvent.click(await screen.findByRole("button", { name: "Revoke" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onConfirm).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "Revoke" })).toBeTruthy();
});
