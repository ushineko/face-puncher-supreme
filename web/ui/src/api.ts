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

// --- Rewrite rules API ---

export interface RewriteRule {
  id: string;
  name: string;
  pattern: string;
  replacement: string;
  is_regex: boolean;
  domains: string[];
  url_patterns: string[];
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export type RewriteRuleInput = Omit<RewriteRule, "id" | "created_at" | "updated_at">;

export async function fetchRewriteRules(): Promise<RewriteRule[]> {
  return apiFetch("/rewrite/rules");
}

export async function createRewriteRule(rule: RewriteRuleInput): Promise<RewriteRule> {
  return apiFetch("/rewrite/rules", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(rule),
  });
}

export async function updateRewriteRule(id: string, rule: RewriteRuleInput): Promise<RewriteRule> {
  return apiFetch(`/rewrite/rules/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(rule),
  });
}

export async function deleteRewriteRule(id: string): Promise<void> {
  await apiFetch(`/rewrite/rules/${id}`, { method: "DELETE" });
}

export async function toggleRewriteRule(id: string): Promise<RewriteRule> {
  return apiFetch(`/rewrite/rules/${id}/toggle`, { method: "PATCH" });
}

export interface RewriteTestResult {
  result: string;
  match_count: number;
  valid: boolean;
  error?: string;
}

export async function testRewritePattern(
  pattern: string,
  replacement: string,
  is_regex: boolean,
  sample: string,
): Promise<RewriteTestResult> {
  return apiFetch("/rewrite/test", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ pattern, replacement, is_regex, sample }),
  });
}

export interface RestartResult {
  status: string;
  message: string;
}

export async function restartProxy(): Promise<RestartResult> {
  return apiFetch("/restart", { method: "POST" });
}
