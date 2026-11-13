import { describe, expect, it, vi } from "vitest";
import { ApiError, createClient, fileName, type Storage } from "./client";

function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
  };
}

function reply(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
  });
}

describe("request", () => {
  it("sends JSON with the CSRF header on state-changing requests only", async () => {
    const fetch = vi.fn(async () => reply(200, { ok: true }));
    const storage = memoryStorage();
    const api = createClient({ fetch, storage });
    api.setCsrf("tok");
    await api.request("GET", "/api/v1/me");
    await api.request("POST", "/api/v1/me/tokens", { name: "git" });
    const get = fetch.mock.calls[0] as unknown as [string, RequestInit];
    const post = fetch.mock.calls[1] as unknown as [string, RequestInit];
    expect(new Headers(get[1].headers).get("X-CSRF-Token")).toBeNull();
    expect(new Headers(post[1].headers).get("X-CSRF-Token")).toBe("tok");
    expect(new Headers(post[1].headers).get("Content-Type")).toBe("application/json");
    expect(post[1].body).toBe(JSON.stringify({ name: "git" }));
    expect(post[1].credentials).toBe("same-origin");
  });

  it("keeps the token across clients through storage", () => {
    const storage = memoryStorage();
    createClient({ fetch: vi.fn(), storage }).setCsrf("tok");
    expect(createClient({ fetch: vi.fn(), storage }).hasSession()).toBe(true);
  });

  it("returns undefined for 204", async () => {
    const api = createClient({ fetch: vi.fn(async () => reply(204)), storage: memoryStorage() });
    await expect(api.request("DELETE", "/api/v1/me/tokens/1")).resolves.toBeUndefined();
  });

  it("maps the error body to ApiError", async () => {
    const api = createClient({
      fetch: vi.fn(async () => reply(409, { error: { code: "conflict", message: "the resource changed" } })),
      storage: memoryStorage(),
    });
    const err = await api.request("POST", "/api/v1/x", {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 409, code: "conflict", message: "the resource changed" });
  });

  it("maps a body that is not the error shape to internal", async () => {
    const api = createClient({ fetch: vi.fn(async () => new Response("<html>", { status: 502 })), storage: memoryStorage() });
    await expect(api.request("GET", "/api/v1/me")).rejects.toMatchObject({ status: 502, code: "internal" });
  });

  it("clears the session and reports it on 401", async () => {
    const storage = memoryStorage();
    const onUnauthenticated = vi.fn();
    const api = createClient({
      fetch: vi.fn(async () => reply(401, { error: { code: "unauthenticated", message: "sign in again" } })),
      storage,
      onUnauthenticated,
    });
    api.setCsrf("tok");
    await expect(api.request("GET", "/api/v1/me")).rejects.toMatchObject({ code: "unauthenticated" });
    expect(api.hasSession()).toBe(false);
    expect(onUnauthenticated).toHaveBeenCalledOnce();
  });

  it("refuses paths outside the API", async () => {
    const fetch = vi.fn();
    const api = createClient({ fetch, storage: memoryStorage() });
    for (const p of ["https://evil.example/api/v1/me", "//evil.example/api/v1/me", "/git/x", "api/v1/me"]) {
      await expect(api.request("GET", p)).rejects.toThrow(/API path/);
    }
    expect(fetch).not.toHaveBeenCalled();
  });

  it("reports a network failure as ApiError with status 0", async () => {
    const api = createClient({ fetch: vi.fn(async () => Promise.reject(new TypeError("Failed to fetch"))), storage: memoryStorage() });
    await expect(api.request("GET", "/api/v1/me")).rejects.toMatchObject({ status: 0, code: "network" });
  });

  it("passes the abort signal through", async () => {
    const fetch = vi.fn(async () => reply(200, {}));
    const api = createClient({ fetch, storage: memoryStorage() });
    const ctl = new AbortController();
    await api.request("GET", "/api/v1/me", undefined, { signal: ctl.signal });
    expect((fetch.mock.calls[0] as unknown as [string, RequestInit])[1].signal).toBe(ctl.signal);
  });
});

describe("download", () => {
  it("posts with the CSRF header and returns the body as a Blob with its file name", async () => {
    const fetch = vi.fn(async () => new Response(new Uint8Array([80, 75]), { status: 200, headers: { "Content-Disposition": 'attachment; filename="libfoo-export.zip"' } }));
    const api = createClient({ fetch, storage: memoryStorage() });
    api.setCsrf("tok");
    const got = await api.download("/api/v1/admin/projects/libfoo/export");
    expect(got.name).toBe("libfoo-export.zip");
    expect(got.blob.size).toBe(2);
    const [, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("tok");
  });

  it("maps errors like request does", async () => {
    const api = createClient({ fetch: vi.fn(async () => reply(403, { error: { code: "forbidden", message: "not allowed" } })), storage: memoryStorage() });
    await expect(api.download("/api/v1/admin/projects/x/export")).rejects.toMatchObject({ status: 403, code: "forbidden" });
  });
});

describe("fileName", () => {
  it.each([
    ['attachment; filename="a-export.zip"', "a-export.zip"],
    ["attachment; filename=b.zip", "b.zip"],
    ['attachment; filename="../x.zip"', ""],
    ['attachment; filename="a\\\\b.zip"', ""],
    [null, ""],
  ])("%s -> %s", (h, want) => expect(fileName(h)).toBe(want));
});
