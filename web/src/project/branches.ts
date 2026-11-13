/** Usual names of the branch fixes go into, most usual first. */
const usualTargets = ["main", "master", "dev", "develop", "trunk"];

/**
 * defaultTarget picks the branch a new review most likely goes into: the
 * first of the usual names that exists, otherwise the first branch. It
 * returns "" when there are no branches.
 */
export function defaultTarget(branches: readonly string[]): string {
  for (const name of usualTargets) {
    const ref = `refs/heads/${name}`;
    if (branches.includes(ref)) return ref;
  }
  return branches[0] ?? "";
}
