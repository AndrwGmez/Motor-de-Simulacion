# Contrato de API y tiempo real

Las fuentes ejecutables son:

- [OpenAPI 3.1](../packages/contracts/openapi.yaml)
- [AsyncAPI 3.0](../packages/contracts/asyncapi.yaml)
- [RunEvent JSON Schema](../packages/contracts/schemas/run-event.schema.json)

Este documento fija semántica, seguridad y recuperación.

## 1. Convenciones HTTP

- Base local: `http://localhost:8080`.
- Recursos autenticados bajo `/v1`.
- Lectura pública bajo `/public/v1`.
- JSON UTF-8.
- UUID en IDs de persistencia.
- Fechas RFC 3339 UTC.
- `X-Request-Id` en cada respuesta.
- Sin slash final canónico.

### 1.1 Códigos

| Código | Uso |
|---|---|
| `200` | Lectura o mutación síncrona |
| `201` | Recurso creado |
| `202` | Orden de control aceptada |
| `204` | Eliminación/revocación sin cuerpo |
| `400` | Solicitud no interpretable |
| `401` | Sesión ausente/vencida |
| `403` | Rol insuficiente o CSRF inválido |
| `404` | Recurso inexistente o no visible |
| `409` | Estado incompatible/idempotencia conflictiva |
| `412` | ETag no coincide |
| `413` | Payload mayor del límite |
| `422` | Entrada o grafo semánticamente inválido |
| `429` | Rate limit |
| `502` | Proveedor de IA falló |
| `503` | Dependencia requerida no disponible |

Un recurso ajeno se responde como `404` cuando revelar su existencia expondría
información.

## 2. Errores

```json
{
  "code": "decision.default.missing",
  "message": "La decisión necesita una ruta predeterminada.",
  "requestId": "01J...",
  "details": [
    {
      "code": "decision.default.missing",
      "severity": "error",
      "message": "Añade una conexión default.",
      "path": "/nodes/2",
      "nodeId": "payment-approved"
    }
  ]
}
```

- `code` es estable y apto para lógica/i18n.
- `message` es humano y puede evolucionar.
- `details` ordena issues por severidad, path y código.
- Nunca devuelve stack traces, SQL, tokens o contexto privado.

## 3. Autenticación

### 3.1 Cookies

| Cookie | Contenido | Propiedades |
|---|---|---|
| `flowverse_access` | Token opaco corto | HttpOnly, SameSite=Lax, Secure prod |
| `flowverse_refresh` | Token opaco rotatorio | HttpOnly, Path refresh, Secure prod |

La base guarda únicamente hashes de tokens. Access dura 15 minutos y refresh 30
días de forma predeterminada.

Registro/login devuelven usuario, expiración y `csrfToken`. El token CSRF se
mantiene solo en memoria del cliente y se envía como `X-CSRF-Token` en toda
mutación autenticada.

### 3.2 Rotación

`POST /v1/auth/refresh`:

1. Verifica refresh, sesión, expiración y CSRF.
2. Marca el token usado.
3. Emite access y refresh nuevos.
4. Si detecta reutilización, revoca toda la familia.

Logout revoca la sesión actual aunque el access todavía no haya expirado.

### 3.3 Password

- 12–128 caracteres.
- Derivación Argon2id según variables documentadas.
- Email normalizado para unicidad.
- Mensaje de login no distingue usuario inexistente de contraseña incorrecta.
- Rate limit actual por IP y ruta: registro 5/10 min, login 10/min, refresh
  30/min y parseo de texto 10/min. El estado vive en memoria de una instancia.

## 4. Autorización

| Acción | owner | editor | viewer |
|---|:---:|:---:|:---:|
| Leer proyecto/flujo/versiones | ✓ | ✓ | ✓ |
| Leer runs/análisis | ✓ | ✓ | ✓ |
| Crear/editar borrador | ✓ | ✓ | — |
| Publicar y ejecutar | ✓ | ✓ | — |
| Cambiar proyecto | ✓ | — | — |
| Gestionar miembros | ✓ | — | — |
| Crear/revocar shares | ✓ | — | — |
| Eliminar flujo/proyecto | ✓ | — | — |

El owner no se puede eliminar ni degradar en el MVP. Solo se añaden emails ya
registrados.

## 5. Recursos

### Auth

```text
POST /v1/auth/register
POST /v1/auth/login
POST /v1/auth/refresh
POST /v1/auth/logout
GET  /v1/auth/me
```

### Proyectos y miembros

```text
GET|POST         /v1/projects
GET|PATCH|DELETE /v1/projects/{projectId}
GET|POST         /v1/projects/{projectId}/members
```

### Flujos y versiones

```text
GET|POST         /v1/projects/{projectId}/flows
GET|PATCH|DELETE /v1/flows/{flowId}
GET|PUT          /v1/flows/{flowId}/draft
GET              /v1/flows/{flowId}/versions
POST             /v1/flows/{flowId}/publish
GET              /v1/flow-versions/{versionId}
POST             /v1/flow-versions/{versionId}/validate
GET              /v1/flow-versions/{versionId}/analysis
```

### Entrada

```text
POST /v1/flows/import
POST /v1/flows/parse-text
```

Ambos son previsualizaciones y nunca persisten por sí solos.

### Runs

```text
POST  /v1/flows/{flowId}/runs
POST  /v1/flow-versions/{versionId}/runs
GET   /v1/runs/{runId}
GET   /v1/runs/{runId}/events
POST  /v1/runs/{runId}/pause
POST  /v1/runs/{runId}/resume
POST  /v1/runs/{runId}/step
POST  /v1/runs/{runId}/cancel
PATCH /v1/runs/{runId}/speed
POST  /v1/runs/{runId}/ws-ticket
WS    /v1/runs/{runId}/live
```

### Shares

```text
GET|POST /v1/flows/{flowId}/share-links
DELETE   /v1/share-links/{shareId}
GET      /public/v1/shares/{token}
```

## 6. Concurrencia de borradores

Lectura:

```http
GET /v1/flows/{flowId}/draft
ETag: "4f53cda18c2baa0c0354bb5f9a3ecbe5..."
```

Guardado:

```http
PUT /v1/flows/{flowId}/draft
If-Match: "4f53cda18c2baa0c0354bb5f9a3ecbe5..."
X-CSRF-Token: ...
Content-Type: application/json
```

Respuesta exitosa incluye ETag nuevo. Si el borrador cambió:

```http
HTTP/1.1 412 Precondition Failed
```

El servidor nunca fusiona grafos automáticamente. La UI conserva la versión
local para descargar o comparar.

Publicar también exige `If-Match`, de modo que no se publique accidentalmente un
borrador que el editor no ha visto.

## 7. Idempotencia de runs

Las dos rutas de creación exigen `Idempotency-Key` de 8 a 128 caracteres una vez
recortados sus espacios exteriores:

- versión publicada: usuario + `flow_version` + `versionId` + key;
- borrador: usuario + `flow_draft` + `flowId` + ETag actual + key.

La retención es de 24 horas. Durante ese periodo:

- primer uso válido: crea el run y devuelve `201`;
- misma key, target y body: devuelve el run existente con `200`;
- misma key y target con body distinto: devuelve
  `409 idempotency.payload_mismatch`;
- header ausente: `400 idempotency.key_required`;
- longitud inválida: `400 idempotency.key_invalid`.

El body se decodifica al `SimulationRequest` controlado, se serializa de forma
determinista y se compara por SHA-256. Una key expirada vuelve a estar
disponible.

`POST /v1/flows/{flowId}/runs` no exige `If-Match`: el servidor captura
atómicamente para el run el documento y ETag que leyó. Si el borrador cambia, la
nueva ETag crea otro target idempotente y la misma key puede crear un run nuevo.
`POST /v1/flow-versions/{versionId}/runs` usa como target la versión inmutable.

Los controles son naturalmente idempotentes cuando el estado ya representa el
resultado solicitado; si la transición no es legal devuelven un `409` estable.

## 8. Importación y parser

### Import

- Content-Length máximo 1 MB.
- JSON estricto.
- Devuelve `definition` normalizada más `ValidationReport`.
- Un report con errores puede devolverse como `422`; warnings no bloquean la
  previsualización.

### Parse text

```json
{
  "text": "Cuando llegue un pedido...",
  "locale": "es"
}
```

La respuesta:

```json
{
  "proposal": {},
  "warnings": [],
  "ambiguities": [
    {
      "code": "decision.default.inferred",
      "question": "¿Qué ocurre si el pago no se aprueba?",
      "suggestedResolution": "Finalizar como pago rechazado."
    }
  ],
  "provider": "mock"
}
```

El adaptador OpenAI solicita Structured Outputs sobre un esquema reducido. La
salida se convierte al contrato completo y vuelve a validarse. El modelo y key
se configuran por entorno; nunca se envían al navegador.

## 9. Protocolo WebSocket

### 9.1 Ticket

El navegador no puede añadir un header de autorización arbitrario al handshake.
Por ello:

```http
POST /v1/runs/{runId}/ws-ticket
```

devuelve:

```json
{
  "ticket": "<aleatorio>",
  "expiresAt": "2026-07-30T20:00:30Z",
  "url": "ws://localhost:8080/v1/runs/.../live"
}
```

- Entropía mínima: 256 bits.
- Validez: 30 segundos.
- Un solo uso.
- Ligado a usuario y run.
- Solo se almacena hash.

Conexión:

```text
WS /v1/runs/{runId}/live?ticket=...&afterSequence=42
```

### 9.2 Replay

1. Consumir ticket atómicamente.
2. Autorizar run.
3. Leer eventos `sequence > afterSequence` en orden.
4. Suscribirse al publicador del run sin ventana de pérdida.
5. Enviar replay y luego eventos nuevos.

La implementación mantiene el bloqueo del estado del run mientras prepara el
replay y registra al suscriptor, cerrando la carrera entre ambos pasos.
Duplicados son aceptables; huecos no. El cliente deduplica por `sequence`.

### 9.3 Mensajes

Cada frame de texto contiene exactamente un `RunEvent` JSON. No se agrupan
eventos y no se aceptan comandos del cliente por el socket.

```json
{
  "schemaVersion": "1.0",
  "type": "edge.traversed",
  "runId": "02fbd9ba-8dd8-4f61-93be-845f067370f9",
  "sequence": 15,
  "occurredAt": "2026-07-30T20:00:01Z",
  "logicalTimeMs": 2750,
  "payload": {
    "edgeId": "edge-payment-approved",
    "tokenId": "token-1"
  }
}
```

`sequence` es monótona, sin huecos dentro del run. `occurredAt` es observabilidad;
la animación y análisis usan `logicalTimeMs`.

### 9.4 Backpressure y cierre

- Cola por suscriptor acotada.
- Si se llena, la implementación actual elimina al suscriptor y cierra el
  stream. Todavía no garantiza un código WebSocket de cierre específico.
- Ante cualquier cierre, el cliente solicita un ticket nuevo y reabre desde la
  última `sequence` aplicada; los eventos durables se recuperan por replay.
- Ping/pong detecta conexiones muertas.
- Ticket inválido/vencido rechaza handshake sin revelar el run.

## 10. Privacidad pública

Un share solo puede referir:

- Una versión publicada.
- Cero o más runs terminales (`completed` o `failed`) de esa versión.

La respuesta pública incluye definición y, por cada run compartido, estado
final, ruta y tiempos agregados. Excluye:

- Inputs.
- Outputs de negocio.
- Contextos y diffs.
- Email/IDs de miembros.
- Overrides sensibles.
- Logs internos.

El token se devuelve una vez al crear y la base conserva solo hash. Revocar tiene
efecto inmediato. Un enlace vencido/revocado responde `404`.

## 11. Health y observabilidad

- `/health/live`: proceso responde.
- `/health/ready`: cuando se usa PostgreSQL, ejecuta `Ping` contra la base. Las
  migraciones configuradas se aplican durante el arranque, antes de aceptar
  tráfico.
- Readiness no depende del proveedor OpenAI si `mock` permite operar.

Estado actual:

- cada respuesta HTTP incluye `X-Request-ID`;
- el proceso registra arranque, cierre, recuperación y errores fatales;
- la migración crea `audit_logs`, pero las operaciones todavía no emiten un
  historial de auditoría completo;
- no existe aún middleware de log por request ni endpoint/exportador Prometheus;
- handshake, replay y desconexión WebSocket todavía no publican métricas.

Logs HTTP estructurados, auditoría completa, métricas WebSocket y exportación
Prometheus son gates de endurecimiento posteriores, no capacidades actuales.

## 12. Evolución

- Rutas incompatibles crean `/v2`.
- Campos/eventos incompatibles incrementan schemaVersion.
- Un evento nuevo no se añade silenciosamente a 1.0: frontend antiguo debe poder
  reconocer la versión o ignorarlo de forma explícita.
- OpenAPI, AsyncAPI, schemas, tipos generados y pruebas de contrato se actualizan
  en el mismo cambio.
