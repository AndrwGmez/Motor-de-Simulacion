import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("account service", () => {
  it("usa sesión demo únicamente cuando no existe URL de API", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "");
    const { authenticate, hasConfiguredApi } = await import("./account-service");
    const session = await authenticate("login", { email: "demo@flowverse.dev", password: "flowverse-demo" });
    expect(hasConfiguredApi).toBe(false);
    expect(session.user.id).toBe("demo-user");
  });

  it("envía registro a la API con cookies habilitadas", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const response = {
      ok: true,
      json: vi.fn().mockResolvedValue({
        user: {
          id: "01fbd9ba-8dd8-4f61-93be-845f067370f9",
          email: "ana@example.com",
          displayName: "Ana",
          createdAt: "2026-07-30T20:00:00Z",
        },
        csrfToken: "csrf-value",
        accessExpiresAt: "2026-07-30T20:15:00Z",
      }),
    } as unknown as Response;
    const fetchMock = vi.fn().mockResolvedValue(response);
    vi.stubGlobal("fetch", fetchMock);
    const { authenticate } = await import("./account-service");
    const session = await authenticate("register", {
      email: "ana@example.com",
      password: "a-secure-password",
      displayName: "Ana",
    });
    expect(session.user.displayName).toBe("Ana");
    expect(fetchMock).toHaveBeenCalledWith(
      "http://api.flowverse.test/v1/auth/register",
      expect.objectContaining({ method: "POST", credentials: "include" }),
    );
  });

  it("no convierte un 401 de la API en una sesión demo", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      json: vi.fn().mockResolvedValue({ message: "Credenciales incorrectas." }),
    }));
    const { authenticate } = await import("./account-service");
    await expect(authenticate("login", { email: "ana@example.com", password: "bad" }))
      .rejects.toThrow("Credenciales incorrectas.");
  });

  it("devuelve sesión ausente ante 401 de /me sin inventar usuario demo", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401 }));
    const { getCurrentUser } = await import("./account-service");
    await expect(getCurrentUser()).resolves.toBeUndefined();
  });
});
