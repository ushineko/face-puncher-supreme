const BASE = "/fps/api";

async function apiFetch<T>(
  path: string,
  opts?: RequestInit,
): Promise<T> {
  const res = await fetch(BASE + path, {
    credentials: "same-origin",
    ...opts,
  });
  if (res.status === 401) {
    window.dispatchEvent(new CustomEvent("fps:unauthorized"));
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || res.statusText);
  }
  return res.json() as Promise<T>;
}

export async function login(
  username: string,
  password: string,
): Promise<string> {
  const res = await apiFetch<{ token: string }>("/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  return res.token;
}

export async function logout(): Promise<void> {
  await apiFetch("/auth/logout", { method: "POST" });
}

export async function authStatus(): Promise<{ authenticated: boolean }> {
  return apiFetch("/auth/status");
}

export async function fetchReadme(): Promise<string> {
  const res = await fetch(BASE + "/readme", { credentials: "same-origin" });
  if (!res.ok) throw new Error(res.statusText);
  return res.text();
}

export async function fetchConfig(): Promise<Record<string, unknown>> {
  return apiFetch("/config");
}

export interface LogEntry {
  timestamp: string;
  level: string;
  msg: string;
  attrs?: Record<string, unknown>;
}

export async function fetchLogs(
  n = 100,
  level = "INFO",
): Promise<LogEntry[]> {
  return apiFetch(`/logs?n=${n}&level=${level}`);
}
