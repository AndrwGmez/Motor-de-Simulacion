import { apiFetch, hasConfiguredApi } from "./api-client";

export { hasConfiguredApi } from "./api-client";
export interface AccountUser {
  id: string;
  email: string;
  displayName: string;
  createdAt: string;
}

export interface AccountSession {
  user: AccountUser;
  csrfToken: string;
  accessExpiresAt: string;
}



function csrfToken(): string | undefined {
  if (typeof document === "undefined") return undefined;
  return document.cookie
    .split("; ")
    .find((entry) => entry.startsWith("flowverse_csrf="))
    ?.split("=")
    .slice(1)
    .join("=");
}

async function errorMessage(response: Response): Promise<string> {
  const payload = await response.json().catch(() => ({})) as {
    message?: string;
    error?: { message?: string };
  };
  return payload.message ?? payload.error?.message ?? `La API respondió con estado ${response.status}.`;
}

function unwrap<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as { data: T }).data;
  }
  return payload as T;
}

export async function authenticate(
  mode: "login" | "register",
  values: { email: string; password: string; displayName?: string },
): Promise<AccountSession> {
  if (!hasConfiguredApi) {
    return {
      user: {
        id: "demo-user",
        email: values.email,
        displayName: values.displayName || "Usuario demo",
        createdAt: new Date().toISOString(),
      },
      csrfToken: "demo-csrf",
      accessExpiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
    };
  }
  const body = mode === "register"
    ? { email: values.email, password: values.password, displayName: values.displayName }
    : { email: values.email, password: values.password };
  const response = await apiFetch(`/v1/auth/${mode}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(await errorMessage(response));
  const session = unwrap<AccountSession>(await response.json() as unknown);
  if (session.csrfToken && typeof document !== "undefined" && !csrfToken()) {
    document.cookie = `flowverse_csrf=${encodeURIComponent(session.csrfToken)}; Path=/; SameSite=Lax`;
  }
  return session;
}

export async function getCurrentUser(): Promise<AccountUser | undefined> {
  if (!hasConfiguredApi) return undefined;
  const response = await apiFetch(`/v1/auth/me`, {});
  if (response.status === 401) return undefined;
  if (!response.ok) throw new Error(await errorMessage(response));
  return unwrap<AccountUser>(await response.json() as unknown);
}

export async function logout(): Promise<void> {
  if (!hasConfiguredApi) return;
  const csrf = csrfToken();
  const response = await apiFetch(`/v1/auth/logout`, {
    method: "POST",
    headers: csrf ? { "X-CSRF-Token": decodeURIComponent(csrf) } : {},
  });
  if (!response.ok && response.status !== 401) throw new Error(await errorMessage(response));
}
