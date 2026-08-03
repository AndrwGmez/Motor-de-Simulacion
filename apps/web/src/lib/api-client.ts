/**
 * Cliente HTTP de la API.
 *
 * Existe por un motivo concreto: el token de acceso dura quince minutos y la web
 * no renovaba nunca, así que la sesión moría sola y la única salida era recargar
 * la página. Con las llamadas repartidas en tres servicios no había un sitio
 * donde interceptar el 401; ahora sí.
 */

const API_URL = process.env.NEXT_PUBLIC_API_URL?.replace(/\/$/, "");

/** Base absoluta de la API; vacía en modo demo. */
export const apiBaseUrl = API_URL ?? "";
export const hasConfiguredApi = Boolean(API_URL);

export interface ApiFetchInit extends RequestInit {
  /** Poner a `false` cuando un 401 sea la respuesta esperada. */
  renovarSesion?: boolean;
}

export class ApiHttpError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiHttpError";
  }
}

export async function httpError(response: Response, fallback: string): Promise<ApiHttpError> {
  const payload = await response.json().catch(() => ({})) as { message?: string; error?: { message?: string } };
  return new ApiHttpError(response.status, payload.message ?? payload.error?.message ?? fallback);
}

export function csrfToken(): string | undefined {
  if (typeof document === "undefined") return undefined;
  return document.cookie
    .split("; ")
    .find((entry) => entry.startsWith("flowverse_csrf="))
    ?.split("=")
    .slice(1)
    .join("=");
}

export function apiHeaders(extra: Record<string, string> = {}): Record<string, string> {
  const csrf = csrfToken();
  return {
    "Content-Type": "application/json",
    ...(csrf ? { "X-CSRF-Token": csrf } : {}),
    ...extra,
  };
}

/**
 * Renovación compartida.
 *
 * El editor carga borrador, versiones y ejecuciones en paralelo. Si cada una
 * lanzara su propia renovación al caducar, el servidor rotaría el token varias
 * veces y la última rotación invalidaría a las anteriores: el usuario acabaría
 * expulsado justamente por el mecanismo que debía salvarlo. Todas comparten la
 * misma promesa, y se libera al terminar para que un vencimiento posterior
 * vuelva a renovar.
 */
let renovacionEnCurso: Promise<boolean> | undefined;

async function renovarSesion(): Promise<boolean> {
  if (renovacionEnCurso) return renovacionEnCurso;
  renovacionEnCurso = (async () => {
    try {
      const response = await fetch(`${API_URL}/v1/auth/refresh`, {
        method: "POST",
        credentials: "include",
        headers: apiHeaders(),
      });
      return response.ok;
    } catch {
      return false;
    } finally {
      renovacionEnCurso = undefined;
    }
  })();
  return renovacionEnCurso;
}

/**
 * Solo estas rutas quedan fuera de la renovación: intentar renovar una
 * renovación fallida sería un bucle, y en un acceso o un registro el 401
 * significa credenciales incorrectas.
 *
 * `/v1/auth/me` sí renueva, y es lo que descubrió la prueba de extremo a
 * extremo: es la primera petición que falla al caducar el token, y excluirla
 * dejaba a la aplicación concluyendo que no había sesión.
 */
function esRutaSinRenovacion(path: string): boolean {
  return ["/v1/auth/refresh", "/v1/auth/login", "/v1/auth/register", "/v1/auth/logout"]
    .some((ruta) => path.includes(ruta));
}

export async function apiFetch(path: string, init: ApiFetchInit = {}): Promise<Response> {
  const { renovarSesion: permiteRenovar = true, headers, ...resto } = init;
  const url = path.startsWith("http") ? path : `${API_URL}${path}`;
  const peticion = (): Promise<Response> => fetch(url, {
    credentials: "include",
    ...resto,
    headers: { ...apiHeaders(), ...(headers as Record<string, string> | undefined) },
  });

  const respuesta = await peticion();
  if (respuesta.status !== 401 || !permiteRenovar || esRutaSinRenovacion(path)) return respuesta;

  const renovada = await renovarSesion();
  if (!renovada) {
    // Sin sesión recuperable, llevar al acceso en lugar de dejar una pantalla
    // muerta repitiendo "Authentication is required".
    if (typeof window !== "undefined" && !window.location.pathname.startsWith("/acceso")) {
      window.location.assign("/acceso");
    }
    return respuesta;
  }
  return peticion();
}
