/**
 * Query keys, built in one place (docs/specs/web.md 5). Keys nest: a
 * review's keys start with its project's key, so invalidating a prefix
 * reaches everything under it.
 */
export const keys = {
  me: () => ["me"] as const,
  tokens: () => ["me", "tokens"] as const,
  certificates: () => ["me", "certificates"] as const,
  invitations: () => ["admin", "invitations"] as const,
  adminUsers: () => ["admin", "users"] as const,
  users: () => ["users"] as const,
  inbox: () => ["inbox"] as const,
  projects: () => ["projects"] as const,
  project: (project: string) => ["project", project] as const,
  reviews: (project: string) => ["project", project, "reviews"] as const,
  review: (project: string, n: number) => ["project", project, "review", n] as const,
  reviewComments: (project: string, n: number) => ["project", project, "review", n, "comments"] as const,
  reviewHistory: (project: string, n: number) => ["project", project, "review", n, "history"] as const,
  discussions: (project: string) => ["project", project, "discussions"] as const,
  discussion: (project: string, id: string) => ["project", project, "discussions", id] as const,
};

/** A query key from keys. */
export type QueryKey = readonly (string | number)[];
