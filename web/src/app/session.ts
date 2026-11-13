import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { keys } from "../api/keys";
import { useApi } from "./context";

/** User is the signed-in account (GET /api/v1/me). */
export interface User {
  id: string;
  username: string;
  display_name: string;
  role: "admin" | "member";
}

/** useMe loads the signed-in user; it fails with 401 when there is no session. */
export function useMe() {
  const api = useApi();
  return useQuery({
    queryKey: keys.me(),
    queryFn: ({ signal }) => api.request<User>("GET", "/api/v1/me", undefined, { signal }),
    retry: false,
    enabled: api.hasSession(),
    staleTime: 60_000,
  });
}

/** useSignIn posts the credentials and keeps the CSRF token it returns. */
export function useSignIn() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (creds: { username: string; password: string }) =>
      api.request<{ user: User; csrf_token: string }>("POST", "/api/v1/auth/login", creds),
    onSuccess: (r) => {
      api.setCsrf(r.csrf_token);
      qc.setQueryData(keys.me(), r.user);
    },
  });
}

/** useSignOut ends the session on the server and forgets it here. */
export function useSignOut() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.request("POST", "/api/v1/auth/logout"),
    onSettled: () => {
      api.clearSession();
      qc.clear();
    },
  });
}
