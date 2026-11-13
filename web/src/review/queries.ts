import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { keys } from "../api/keys";
import { useApi } from "../app/context";
import type { User } from "../app/session";
import type { Comment, DiffSummary, Hunk, ReviewDetail } from "./model";

const base = (project: string, n: number) => `/api/v1/projects/${encodeURIComponent(project)}/reviews/${n}`;

export function useReview(project: string, n: number) {
  const api = useApi();
  return useQuery({
    queryKey: keys.review(project, n),
    queryFn: ({ signal }) => api.request<ReviewDetail>("GET", base(project, n), undefined, { signal }),
  });
}

/** useUsers maps user IDs to accounts, for names next to comments and verdicts. */
export function useUsers() {
  const api = useApi();
  return useQuery({
    queryKey: ["users"],
    queryFn: ({ signal }) => api.request<{ users: User[] }>("GET", "/api/v1/users", undefined, { signal }),
    select: (r) => new Map(r.users.map((u) => [u.id, u])),
    staleTime: 5 * 60_000,
  });
}

export function useDiff(project: string, n: number, rev: number) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.review(project, n), "diff", rev],
    queryFn: ({ signal }) => api.request<DiffSummary>("GET", `${base(project, n)}/diff?rev=${rev}`, undefined, { signal }),
    enabled: rev > 0,
  });
}

export function useFileDiff(project: string, n: number, from: string, to: string, oldPath: string, newPath: string) {
  const api = useApi();
  const q = new URLSearchParams({ from, to, old: oldPath, new: newPath });
  return useQuery({
    queryKey: [...keys.review(project, n), "file", from, to, oldPath, newPath],
    queryFn: ({ signal }) => api.request<{ hunks: Hunk[] }>("GET", `${base(project, n)}/diff/file?${q}`, undefined, { signal }),
    enabled: from !== "" && to !== "",
    staleTime: Infinity, // commits never change
  });
}

export function useComments(project: string, n: number, rev: number) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.reviewComments(project, n), rev],
    queryFn: ({ signal }) => api.request<{ comments: Comment[] }>("GET", `${base(project, n)}/comments${rev > 0 ? `?rev=${rev}` : ""}`, undefined, { signal }),
    select: (r) => r.comments,
  });
}

export interface HistoryEvent {
  id: string;
  actor_id: string;
  action: string;
  data: Record<string, unknown>;
  at: string;
}

export function useHistory(project: string, n: number) {
  const api = useApi();
  return useQuery({
    queryKey: keys.reviewHistory(project, n),
    queryFn: ({ signal }) => api.request<{ events: HistoryEvent[]; next_cursor: string }>("GET", `${base(project, n)}/history?limit=100`, undefined, { signal }),
  });
}

/** useReviewAction posts to the review and refreshes everything under it. */
export function useReviewAction<T>(project: string, n: number, path: (vars: T) => string, body: (vars: T) => unknown, method: "POST" | "PATCH" | "DELETE" = "POST") {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: T) => api.request(method, path(vars), body(vars)),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.review(project, n) }),
  });
}

export const reviewPath = base;
