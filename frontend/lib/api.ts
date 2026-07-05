import type {
  CheckinListParams,
  CheckinListResponse,
  EventListParams,
  EventListResponse,
  Monitor,
  MonitorListParams,
  MonitorListResponse,
} from "@/types/monitor";

const BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "";

// In-memory only — never persisted to localStorage/sessionStorage (XSS risk
// per the react-frontend-specialist skill). Lost on every hard reload;
// restoreSession() re-derives it from the httpOnly refresh cookie.
let accessToken: string | null = null;

export function getAccessToken(): string | null {
  return accessToken;
}

function setAccessToken(token: string | null): void {
  accessToken = token;
}

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export class NetworkError extends Error {
  constructor() {
    super("Unable to reach the server.");
    this.name = "NetworkError";
  }
}

export type User = {
  id: string;
  email: string;
};

type AuthResponse = {
  access_token: string;
  user: User;
};

async function rawFetch(path: string, init: RequestInit = {}): Promise<Response> {
  try {
    return await fetch(`${BASE_URL}${path}`, {
      ...init,
      // Required so the httpOnly ping_refresh cookie travels on every
      // /api/v1/auth/* call; the frontend never reads the cookie itself.
      credentials: "include",
      headers: {
        ...(init.body ? { "Content-Type": "application/json" } : {}),
        ...(accessToken ? { Authorization: `Bearer ${accessToken}` } : {}),
        ...init.headers,
      },
    });
  } catch {
    throw new NetworkError();
  }
}

async function parseErrorBody(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string };
    return body.error ?? `request failed (${res.status})`;
  } catch {
    return `request failed (${res.status})`;
  }
}

/**
 * Attempts to restore a session from the httpOnly refresh cookie. Used both
 * on app mount (to answer "is there a valid session") and internally by
 * apiFetch's 401 handler. Never throws — returns null on any failure and
 * clears the in-memory access token.
 */
export async function refresh(): Promise<User | null> {
  const res = await rawFetch("/api/v1/auth/refresh", { method: "POST" }).catch(
    () => null,
  );
  if (!res || !res.ok) {
    setAccessToken(null);
    return null;
  }
  const data = (await res.json()) as AuthResponse;
  setAccessToken(data.access_token);
  return data.user;
}

export const restoreSession = refresh;

/**
 * Generic authenticated fetch wrapper — all backend calls go through this,
 * never raw fetch in components. On a 401 (other than from refresh itself),
 * attempts one silent refresh-and-retry before giving up.
 */
export async function apiFetch<T>(
  path: string,
  init: RequestInit = {},
  signal?: AbortSignal,
): Promise<T> {
  let res = await rawFetch(path, { ...init, signal });

  if (res.status === 401 && path !== "/api/v1/auth/refresh") {
    const user = await refresh();
    if (user) {
      res = await rawFetch(path, { ...init, signal });
    }
  }

  if (!res.ok) {
    throw new ApiError(res.status, await parseErrorBody(res));
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export async function register(email: string, password: string): Promise<AuthResponse> {
  const data = await apiFetch<AuthResponse>("/api/v1/auth/register", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  setAccessToken(data.access_token);
  return data;
}

export async function login(email: string, password: string): Promise<AuthResponse> {
  const data = await apiFetch<AuthResponse>("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  setAccessToken(data.access_token);
  return data;
}

export async function logout(): Promise<void> {
  await rawFetch("/api/v1/auth/logout", { method: "POST" }).catch(() => {});
  setAccessToken(null);
}

export async function listMonitors(
  params: MonitorListParams = {},
  signal?: AbortSignal,
): Promise<MonitorListResponse> {
  const qs = new URLSearchParams();
  if (params.q) qs.set("q", params.q);
  if (params.kind) qs.set("kind", params.kind);
  if (params.state) qs.set("state", params.state);
  if (params.cursor) qs.set("cursor", params.cursor);
  if (params.limit) qs.set("limit", String(params.limit));
  const suffix = qs.toString() ? `?${qs}` : "";
  return apiFetch<MonitorListResponse>(`/api/v1/monitors${suffix}`, {}, signal);
}

export async function getMonitor(id: string, signal?: AbortSignal): Promise<Monitor> {
  return apiFetch<Monitor>(`/api/v1/monitors/${id}`, {}, signal);
}

export async function listMonitorCheckins(
  id: string,
  params: CheckinListParams = {},
  signal?: AbortSignal,
): Promise<CheckinListResponse> {
  const qs = new URLSearchParams();
  if (params.cursor) qs.set("cursor", params.cursor);
  if (params.limit) qs.set("limit", String(params.limit));
  const suffix = qs.toString() ? `?${qs}` : "";
  return apiFetch<CheckinListResponse>(`/api/v1/monitors/${id}/checkins${suffix}`, {}, signal);
}

export async function listMonitorEvents(
  id: string,
  params: EventListParams = {},
  signal?: AbortSignal,
): Promise<EventListResponse> {
  const qs = new URLSearchParams();
  if (params.type) qs.set("type", params.type);
  if (params.cursor) qs.set("cursor", params.cursor);
  if (params.limit) qs.set("limit", String(params.limit));
  const suffix = qs.toString() ? `?${qs}` : "";
  return apiFetch<EventListResponse>(`/api/v1/monitors/${id}/events${suffix}`, {}, signal);
}

export async function pauseMonitor(id: string): Promise<Monitor> {
  return apiFetch<Monitor>(`/api/v1/monitors/${id}/pause`, { method: "POST" });
}

export async function resumeMonitor(id: string): Promise<Monitor> {
  return apiFetch<Monitor>(`/api/v1/monitors/${id}/resume`, { method: "POST" });
}

export async function muteMonitor(id: string): Promise<Monitor> {
  return apiFetch<Monitor>(`/api/v1/monitors/${id}/mute`, { method: "POST" });
}

export async function unmuteMonitor(id: string): Promise<Monitor> {
  return apiFetch<Monitor>(`/api/v1/monitors/${id}/unmute`, { method: "POST" });
}
