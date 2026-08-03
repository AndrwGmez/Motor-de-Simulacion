import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

const API = "http://api.flowverse.test";

function respuesta(status: number, cuerpo: unknown = {}): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers(),
    json: vi.fn().mockResolvedValue(cuerpo),
    text: vi.fn().mockResolvedValue(JSON.stringify(cuerpo)),
  } as unknown as Response;
}

/** Devuelve 401 las primeras `fallos` veces y 200 después. */
function servidorQueExpira(fallos: number) {
  const llamadas: string[] = [];
  const fetchMock = vi.fn(async (url: string) => {
    llamadas.push(url);
    if (url.endsWith("/v1/auth/refresh")) return respuesta(200, { csrfToken: "nuevo" });
    const previas = llamadas.filter((u) => u === url).length;
    return previas <= fallos ? respuesta(401, { message: "Authentication is required" }) : respuesta(200, { ok: true });
  });
  return { fetchMock, llamadas };
}

describe("apiFetch renueva la sesión en vez de dejar la pantalla muerta", () => {
  it("ante un 401 renueva y reintenta la petición original", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", API);
    const { fetchMock, llamadas } = servidorQueExpira(1);
    vi.stubGlobal("fetch", fetchMock);

    const { apiFetch } = await import("./api-client");
    const respuestaFinal = await apiFetch("/v1/projects");

    expect(respuestaFinal.status).toBe(200);
    expect(llamadas.filter((u) => u.endsWith("/v1/auth/refresh"))).toHaveLength(1);
    expect(llamadas.filter((u) => u.endsWith("/v1/projects"))).toHaveLength(2);
  });

  it("no reintenta indefinidamente si la renovación tampoco vale", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", API);
    const llamadas: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      llamadas.push(url);
      return url.endsWith("/v1/auth/refresh") ? respuesta(401) : respuesta(401);
    }));

    const { apiFetch } = await import("./api-client");
    const final = await apiFetch("/v1/projects");

    expect(final.status).toBe(401);
    // Una original, una renovación fallida y ningún reintento más.
    expect(llamadas.filter((u) => u.endsWith("/v1/projects"))).toHaveLength(1);
    expect(llamadas.filter((u) => u.endsWith("/v1/auth/refresh"))).toHaveLength(1);
  });

  it("con varias peticiones caducando a la vez renueva una sola vez", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", API);
    const { fetchMock, llamadas } = servidorQueExpira(1);
    vi.stubGlobal("fetch", fetchMock);

    const { apiFetch } = await import("./api-client");
    // El editor carga borrador, versiones y ejecuciones en paralelo: si cada una
    // dispara su propia renovación, el token se rota varias veces y la última
    // invalida a las demás.
    await Promise.all([
      apiFetch("/v1/flows/a/draft"),
      apiFetch("/v1/flows/a/versions"),
      apiFetch("/v1/flows/a/runs"),
    ]);

    expect(llamadas.filter((u) => u.endsWith("/v1/auth/refresh"))).toHaveLength(1);
  });

  it("renueva también cuando falla la comprobación de sesión, que es la primera en caducar", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", API);
    const { fetchMock, llamadas } = servidorQueExpira(1);
    vi.stubGlobal("fetch", fetchMock);
    const { apiFetch } = await import("./api-client");
    await apiFetch("/v1/auth/me");
    expect(llamadas.filter((u) => u.endsWith("/v1/auth/refresh"))).toHaveLength(1);
  });

  it("no intenta renovar una renovación fallida, que sería un bucle", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", API);
    const llamadas: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      llamadas.push(url);
      return respuesta(401);
    }));

    const { apiFetch } = await import("./api-client");
    await apiFetch("/v1/auth/refresh");

    // Una sola: la original. Si hubiera reintento, serían dos y el bucle
    // se comería la pestaña.
    expect(llamadas.filter((u) => u.endsWith("/v1/auth/refresh"))).toHaveLength(1);
  });

  it("vuelve a renovar en un fallo posterior, no una sola vez por vida de la página", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", API);
    const { fetchMock, llamadas } = servidorQueExpira(1);
    vi.stubGlobal("fetch", fetchMock);

    const { apiFetch } = await import("./api-client");
    await apiFetch("/v1/projects");
    await apiFetch("/v1/flows/a/draft");

    expect(llamadas.filter((u) => u.endsWith("/v1/auth/refresh"))).toHaveLength(2);
  });
});
