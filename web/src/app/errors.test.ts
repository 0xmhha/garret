import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import { createTranslator } from "../i18n/i18n";
import { errorMessage } from "./errors";

const t = createTranslator("en");

describe("errorMessage", () => {
  it.each([
    [new ApiError(0, "network", "x"), "The server cannot be reached. Check that you are on the same network."],
    [new ApiError(403, "forbidden", "x"), "You do not have permission to do that."],
    [new ApiError(400, "invalid", "name is required"), "The server refused this: name is required."],
    [new ApiError(409, "export_required", "x"), "Export the project before you purge it."],
    [new ApiError(500, "internal", "x"), "Something went wrong. Try again."],
    [new Error("boom"), "Something went wrong. Try again."],
  ])("%s", (err, want) => expect(errorMessage(err, t)).toBe(want));
});
