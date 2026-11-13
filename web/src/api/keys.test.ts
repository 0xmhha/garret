import { expect, it } from "vitest";
import { keys } from "./keys";

it("nests review keys under their project so one prefix invalidates them", () => {
  expect(keys.review("libfoo", 12).slice(0, keys.project("libfoo").length)).toEqual(keys.project("libfoo"));
  expect(keys.reviewComments("libfoo", 12).slice(0, keys.review("libfoo", 12).length)).toEqual(keys.review("libfoo", 12));
  expect(keys.review("libfoo", 12)).not.toEqual(keys.review("libfoo", 13));
  expect(keys.review("libfoo", 1)).not.toEqual(keys.review("libbar", 1));
});
