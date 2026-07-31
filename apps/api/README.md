# FlowVerse API

Backend de FlowVerse 3D. Expone autenticación, proyectos con RBAC, borradores
versionados, validación/análisis de grafos, simulación determinista, replay de
eventos y WebSockets.

## Requisitos

- Go 1.23 o posterior.
- PostgreSQL 15 o posterior cuando `STORE_DRIVER=postgres`.
- Ningún servicio externo para el modo predeterminado (`memory` + parser
  `mock`).

El contenedor usa una imagen de compilación Go 1.26.5, pero el módulo mantiene
compatibilidad con Go 1.23.

## Inicio rápido

```bash
go run ./cmd/api
```

Ese comando usa los valores predeterminados (`memory` + parser `mock`). El
binario no carga archivos `.env` automáticamente. Si se quiere partir del
ejemplo, hay que exportarlo en la shell antes de arrancar:

```bash
cp .env.example .env
set -a
source .env
set +a
go run ./cmd/api
```

El proceso escucha en `http://localhost:8080`. Comprobaciones:

```bash
curl http://localhost:8080/health/live
curl http://localhost:8080/health/ready
```

Para usar PostgreSQL:

```bash
export STORE_DRIVER=postgres
export DATABASE_URL='postgres://flowverse:flowverse@localhost:5432/flowverse?sslmode=disable'
go run ./cmd/api
```

`AUTO_MIGRATE=true` aplica las migraciones idempotentes embebidas al iniciar.
En un despliegue administrado puede establecerse en `false` y ejecutar el SQL
de `internal/store/migrations` como parte del release.

## Configuración

| Variable | Predeterminado | Uso |
|---|---|---|
| `PORT` | `8080` | Puerto HTTP |
| `APP_ENV` | `development` | Activa cookies `Secure` y Gin release en `production` |
| `PUBLIC_ORIGIN` | `http://localhost:3000` | Origen CORS y WebSocket permitido |
| `STORE_DRIVER` | `memory` | `memory` o `postgres` |
| `DATABASE_URL` | — | DSN requerido para PostgreSQL |
| `AUTO_MIGRATE` | `true` | Aplica la migración embebida |
| `ACCESS_TOKEN_TTL` | `15m` | Duración del token opaco de acceso |
| `REFRESH_TOKEN_TTL` | `720h` | Duración y rotación de refresh |
| `FLOW_PARSER_PROVIDER` | `mock` | `mock` u `openai` |
| `OPENAI_API_KEY` | — | Solo necesaria con proveedor `openai` |
| `OPENAI_MODEL` | `gpt-4.1-mini` | Modelo configurable de Responses API |

## Arquitectura

```text
cmd/api                 arranque, configuración y cierre ordenado
internal/domain         contrato de dominio FlowDefinition y agregados
internal/engine         condiciones, validación, análisis y simulación
internal/auth           Argon2id, tokens opacos y refresh rotatorio
internal/parser         proveedor mock y OpenAI Responses/Structured Outputs
internal/runtime        pacing, controles, persistencia y fan-out WebSocket
internal/store          Repository, memoria, PostgreSQL y migración
internal/httpapi        Gin, autorización, REST, shares y WebSocket
```

PostgreSQL conserva los documentos editables y publicados en JSONB. Las
versiones publicadas son inmutables; las ejecuciones guardan el snapshot exacto,
eventos ordenados y resultados por nodo. El repositorio en memoria implementa
el mismo contrato para desarrollo y pruebas.

## Contrato HTTP

La fuente canónica es `../../packages/contracts/openapi.yaml` junto con los JSON
Schema del mismo paquete. Las rutas principales son:

- `/v1/auth/*`: registro, login, refresh rotatorio, logout y usuario actual.
- `/v1/projects/{projectId}/flows`: creación y listado.
- `/v1/flows/{flowId}/draft`: documento editable con `ETag`/`If-Match`.
- `/v1/flows/{flowId}/publish`: publicación inmutable.
- `/v1/flow-versions/{versionId}/validate|analysis|runs`.
- `/v1/flows/{flowId}/runs`: simulación del snapshot exacto del borrador.
- `/v1/runs/{runId}/*`: consulta, eventos, pausa, resume, step, cancelación,
  velocidad y ticket WebSocket.
- `/v1/flows/{flowId}/share-links` y `/public/v1/shares/{token}`.

El token de un share se devuelve una sola vez junto con una URL de interfaz
`{PUBLIC_ORIGIN}/compartir/{token}`. El endpoint `/public/v1/shares/{token}` es
el recurso JSON consumido por esa pantalla.

Crear un run publicado o de borrador exige `Idempotency-Key` de 8 a 128
caracteres. Durante 24 horas, el alcance es usuario + target exacto + key:
`versionId` para versiones publicadas y `flowId` + ETag para borradores. Repetir
el mismo body devuelve el run original; reutilizar la key en ese target con
otro body devuelve `409 idempotency.payload_mismatch`. Usuarios, versiones y
revisiones de borrador distintas no comparten el mismo espacio de keys.

El ETag es un hash fuerte entre comillas. El cliente debe devolver literalmente
el header recibido en `If-Match`; una copia obsoleta recibe `412`.

Las sesiones usan `flowverse_access` y `flowverse_refresh` como cookies
`HttpOnly`, más `flowverse_csrf` legible por el cliente. Todas las mutaciones
autenticadas mediante cookie requieren `X-CSRF-Token`. Los tokens Bearer se
admiten para clientes de API y no necesitan CSRF.

Los errores tienen forma:

```json
{
  "code": "draft.conflict",
  "message": "Draft changed since it was loaded",
  "requestId": "uuid",
  "details": null
}
```

## Simulación

El motor no ejecuta código ni acciones externas. Trabaja con tiempo lógico y
produce un stream reproducible:

- decisiones `first_match` y `all_matches`;
- fan-out y activación `each`, `any` y `all`;
- operaciones controladas `set`, `copy` y `delete` con JSON Pointer;
- integration, delay y end simulados;
- overrides `force_edge` y `fail_node`;
- límites de pasos/visitas para ciclos;
- persistencia antes de publicar cada evento.

La velocidad modifica únicamente el ritmo visual. El replay HTTP y WebSocket
usa `afterSequence`; el ticket WebSocket es efímero y de un solo uso.

Antes de aceptar tráfico, el bootstrap marca transaccionalmente como
`interrupted` cualquier run persistido en estado activo y añade
`run.interrupted` con la siguiente secuencia. La recuperación es idempotente y
preserva sin cambios los runs terminales.

## Texto a flujo

`FLOW_PARSER_PROVIDER=mock` es determinista y no usa red. Con `openai`, el
adaptador llama `POST /v1/responses` con `store:false` y
`text.format.type=json_schema`. Refusos, respuestas incompletas, timeouts y
propuestas inválidas se devuelven como errores recuperables. El endpoint solo
genera una previsualización: nunca persiste automáticamente la respuesta.

## Pruebas

```bash
go test ./...
go test -race ./...
```

La suite incluye:

- condiciones y mutaciones JSON Pointer table-driven;
- validador, SCC/ciclos, rutas y camino crítico;
- golden stream de simulación, decisiones, overrides, forks, join y conflictos;
- Argon2id, login, rotación y detección de reutilización de refresh;
- parser mock;
- repositorio en memoria, ETag e idempotencia;
- contrato HTTP create → draft → conflicto → publish y aislamiento entre
  proyectos;
- replay/runtime.

Para entornos con caché restringida:

```bash
GOCACHE=/tmp/flowverse-go-build \
GOMODCACHE=/tmp/flowverse-go-mod \
go test ./...
```

## Seguridad y límites actuales

- Máximo 1 MiB por request, 500 nodos y 1.000 conexiones.
- La importación rechaza propiedades desconocidas y contenido JSON adicional;
  las configuraciones y metadatos se validan según el tipo de nodo.
- Rate limit por IP+ruta, con memoria acotada: registro 5/10 min, login
  10/min, refresh 30/min y parseo de texto 10/min. Un exceso devuelve `429` y
  `Retry-After`; salud y WebSocket no pasan por este limitador.
- Contraseñas de 12–128 caracteres, Argon2id, refresh rotatorio y revocación de
  familia ante reutilización.
- Consultas autorizadas por membresía de proyecto; recursos ajenos responden
  como no encontrados.
- Los enlaces públicos almacenan únicamente el hash del token y omiten inputs,
  outputs y contextos.
- La primera versión opera con una sola instancia de API. Tickets, suscriptores
  WebSocket y control activo viven en memoria; un reinicio conserva los eventos
  PostgreSQL pero interrumpe una ejecución activa.
- La tabla de auditoría está creada, pero la emisión completa de auditoría y la
  exportación Prometheus quedan como endurecimiento posterior.
