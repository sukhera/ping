import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const BASE = "http://localhost:8080";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

// api.ts keeps its access token in a module-level variable — vi.resetModules
// forces a fresh module instance per test so that variable can't leak
// between tests (without this, `import("./api")` returns the same cached
// instance every time and tests only appear isolated because each one
// happens to overwrite the token via login/register/logout before reading it).
async function freshApi() {
  vi.resetModules();
  const { register, login, logout, apiFetch, ApiError, getAccessToken } =
    await import("./api");
  return { register, login, logout, apiFetch, ApiError, getAccessToken };
}

beforeEach(() => {
  process.env.NEXT_PUBLIC_API_BASE_URL = BASE;
});

describe("login", () => {
  it("stores the access token on success", async () => {
    server.use(
      http.post(`${BASE}/api/v1/auth/login`, () =>
        HttpResponse.json({
          access_token: "token-abc",
          user: { id: "u1", email: "a@example.com" },
        }),
      ),
    );
    const { login, getAccessToken } = await freshApi();

    const data = await login("a@example.com", "correcthorsebatterystaple");

    expect(data.user.email).toBe("a@example.com");
    expect(getAccessToken()).toBe("token-abc");
  });

  it("throws ApiError with status on 401", async () => {
    server.use(
      http.post(`${BASE}/api/v1/auth/login`, () =>
        HttpResponse.json({ error: "invalid email or password" }, { status: 401 }),
      ),
    );
    const { login, ApiError } = await freshApi();

    await expect(login("a@example.com", "wrong")).rejects.toMatchObject({
      name: "ApiError",
      status: 401,
    });
    await expect(login("a@example.com", "wrong")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("register", () => {
  it("stores the access token on success", async () => {
    server.use(
      http.post(`${BASE}/api/v1/auth/register`, () =>
        HttpResponse.json({
          access_token: "token-xyz",
          user: { id: "u2", email: "b@example.com" },
        }),
      ),
    );
    const { register, getAccessToken } = await freshApi();

    await register("b@example.com", "correcthorsebatterystaple");

    expect(getAccessToken()).toBe("token-xyz");
  });

  it("surfaces 403 as ApiError", async () => {
    server.use(
      http.post(`${BASE}/api/v1/auth/register`, () =>
        HttpResponse.json({ error: "registration is closed" }, { status: 403 }),
      ),
    );
    const { register } = await freshApi();

    await expect(
      register("b@example.com", "correcthorsebatterystaple"),
    ).rejects.toMatchObject({ status: 403 });
  });
});

describe("logout", () => {
  it("clears the in-memory token", async () => {
    server.use(
      http.post(`${BASE}/api/v1/auth/login`, () =>
        HttpResponse.json({
          access_token: "token-abc",
          user: { id: "u1", email: "a@example.com" },
        }),
      ),
      http.post(`${BASE}/api/v1/auth/logout`, () => new HttpResponse(null, { status: 200 })),
    );
    const { login, logout, getAccessToken } = await freshApi();

    await login("a@example.com", "correcthorsebatterystaple");
    expect(getAccessToken()).toBe("token-abc");

    await logout();
    expect(getAccessToken()).toBeNull();
  });

  it("clears the token even if the network call fails", async () => {
    server.use(
      http.post(`${BASE}/api/v1/auth/login`, () =>
        HttpResponse.json({
          access_token: "token-abc",
          user: { id: "u1", email: "a@example.com" },
        }),
      ),
      http.post(`${BASE}/api/v1/auth/logout`, () => HttpResponse.error()),
    );
    const { login, logout, getAccessToken } = await freshApi();

    await login("a@example.com", "correcthorsebatterystaple");
    await logout();

    expect(getAccessToken()).toBeNull();
  });
});

describe("apiFetch 401-refresh-retry", () => {
  it("retries once after a successful silent refresh", async () => {
    let calls = 0;
    server.use(
      http.get(`${BASE}/api/v1/protected`, () => {
        calls += 1;
        return calls === 1
          ? HttpResponse.json({ error: "unauthorized" }, { status: 401 })
          : HttpResponse.json({ ok: true });
      }),
      http.post(`${BASE}/api/v1/auth/refresh`, () =>
        HttpResponse.json({
          access_token: "new-token",
          user: { id: "u1", email: "a@example.com" },
        }),
      ),
    );
    const { apiFetch, getAccessToken } = await freshApi();

    const result = await apiFetch<{ ok: boolean }>("/api/v1/protected");

    expect(result).toEqual({ ok: true });
    expect(calls).toBe(2);
    expect(getAccessToken()).toBe("new-token");
  });

  it("throws the original 401 if refresh also fails, and clears the token", async () => {
    server.use(
      http.get(`${BASE}/api/v1/protected`, () =>
        HttpResponse.json({ error: "unauthorized" }, { status: 401 }),
      ),
      http.post(`${BASE}/api/v1/auth/refresh`, () =>
        HttpResponse.json({ error: "invalid or expired refresh token" }, { status: 401 }),
      ),
    );
    const { apiFetch, getAccessToken } = await freshApi();

    await expect(apiFetch("/api/v1/protected")).rejects.toMatchObject({ status: 401 });
    expect(getAccessToken()).toBeNull();
  });
});
