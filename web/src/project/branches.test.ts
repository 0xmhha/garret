import { describe, expect, it } from "vitest";
import { defaultTarget } from "./branches";

describe("defaultTarget", () => {
  it.each([
    [["refs/heads/fix", "refs/heads/main"], "refs/heads/main"],
    [["refs/heads/fix", "refs/heads/master"], "refs/heads/master"],
    [["refs/heads/backup/x", "refs/heads/dev", "refs/heads/feat/y"], "refs/heads/dev"],
    [["refs/heads/develop", "refs/heads/feat/y"], "refs/heads/develop"],
    [["refs/heads/main", "refs/heads/dev"], "refs/heads/main"],
    [["refs/heads/dev", "refs/heads/feat/x", "refs/heads/origin/dev", "refs/heads/origin/master"], "refs/heads/origin/master"],
    [["refs/heads/master", "refs/heads/origin/master"], "refs/heads/master"],
    [["refs/heads/feat/x", "refs/heads/origin/dev"], "refs/heads/origin/dev"],
    [["refs/heads/b", "refs/heads/a"], "refs/heads/b"],
    [[], ""],
  ])("%j -> %s", (list, want) => expect(defaultTarget(list)).toBe(want));
});
