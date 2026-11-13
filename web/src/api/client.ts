/** The part of Web Storage the client uses; localStorage in the browser. */
export interface Storage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

type Fetch = (input: string, init: RequestInit) => Promise<Response>;

export type Method = "GET" | "POST" | "PATCH" | "DELETE";

const apiPrefix = "/api/v1/";
const csrfKey = "garret.csrf";
const csrfHeader = "X-CSRF-Token";

/** ApiError is a failed request: the server's error code, or network or internal. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ClientOptions {
  fetch: Fetch;
  storage: Storage;
  /** Called after a 401, once the stored token is gone. */
  onUnauthenticated?: () => void;
}

/**
 * createClient returns the JSON client for /api/v1 (docs/specs/web.md 5).
 * State-changing requests carry the CSRF token, which lives in storage so
 * that every tab and a reload share it; a 401 removes it.
 */
export function createClient(opts: ClientOptions) {
  const { fetch, storage, onUnauthenticated } = opts;

  async function send(method: Method, path: string, accept: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
    if (!path.startsWith(apiPrefix)) {
      throw new Error(`not an API path: ${path}`);
    }
    const headers = new Headers({ Accept: accept });
    if (body !== undefined) headers.set("Content-Type", "application/json");
    const token = storage.getItem(csrfKey);
    if (method !== "GET" && token) headers.set(csrfHeader, token);
    let resp: Response;
    try {
      resp = await fetch(path, {
        method,
        headers,
        credentials: "same-origin",
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        ...(signal ? { signal } : {}),
      });
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") throw err;
      throw new ApiError(0, "network", "the server cannot be reached");
    }
    if (resp.status === 401) {
      storage.removeItem(csrfKey);
      onUnauthenticated?.();
    }
    if (!resp.ok) throw await toError(resp);
    return resp;
  }

  async function request<T = unknown>(method: Method, path: string, body?: unknown, init: { signal?: AbortSignal } = {}): Promise<T> {
    const resp = await send(method, path, "application/json", body, init.signal);
    if (resp.status === 204) return undefined as T;
    return (await resp.json()) as T;
  }

  /**
   * download posts to path and returns the response body as a Blob, with
   * the file name from Content-Disposition (empty when there is none).
   */
  async function download(path: string, body?: unknown): Promise<{ blob: Blob; name: string }> {
    const resp = await send("POST", path, "*/*", body);
    return { blob: await resp.blob(), name: fileName(resp.headers.get("Content-Disposition")) };
  }

  return {
    request,
    download,
    setCsrf: (token: string) => storage.setItem(csrfKey, token),
    clearSession: () => storage.removeItem(csrfKey),
    hasSession: () => storage.getItem(csrfKey) !== null,
  };
}

/** Client is the JSON client createClient returns. */
export type Client = ReturnType<typeof createClient>;

async function toError(resp: Response): Promise<ApiError> {
  try {
    const body = (await resp.json()) as { error?: { code?: unknown; message?: unknown } };
    const { code, message } = body.error ?? {};
    if (typeof code === "string" && typeof message === "string") {
      return new ApiError(resp.status, code, message);
    }
  } catch {
    // Not JSON: a proxy page or a crash; reported as internal below.
  }
  return new ApiError(resp.status, "internal", "the server could not handle the request");
}

/** fileName returns the plain filename parameter of a Content-Disposition header. */
export function fileName(header: string | null): string {
  const m = /filename="([^"]*)"/.exec(header ?? "") ?? /filename=([^;\s]+)/.exec(header ?? "");
  const name = m?.[1] ?? "";
  // A name with a path separator would be refused by the browser anyway.
  return /[\\/]/.test(name) ? "" : name;
}
