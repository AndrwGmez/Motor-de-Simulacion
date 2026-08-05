# Enterprise Control Plane

FlowVerse expone un plano de control multi-tenant para organizaciones, RBAC,
SSO, políticas, plugins, proyectos y auditoría verificable. Su contrato canónico
está en [`packages/contracts/openapi.yaml`](../packages/contracts/openapi.yaml)
y sus modelos JSON Schema en
[`packages/contracts/schemas/enterprise.schema.json`](../packages/contracts/schemas/enterprise.schema.json).

## Garantías de seguridad

- Toda ruta requiere una sesión autenticada. Las mutaciones también requieren
  el token CSRF usado por el resto de la API.
- Un usuario sin membresía activa recibe `404 resource.not_found`, igual que
  ante un tenant o recurso inexistente. Esto evita confirmar IDs ajenos.
- Los UUID de path deben estar en formato canónico y los bodies JSON son
  estrictos, aceptan un solo documento y tienen un máximo de 64 KiB.
- Mutaciones, evaluaciones y verificaciones completas tienen rate limit y control de concurrencia. Las
  respuestas `429` y `503` incluyen semántica de reintento.
- Las mutaciones se confirman junto con su evento de auditoría en una misma
  sección crítica o transacción. Si el evento no puede persistirse, la mutación
  no se vuelve visible.
- El modelo SSO contiene exclusivamente metadatos públicos de discovery. No
  admite client secrets, tokens, llaves privadas ni certificados completos.

## Roles

| Capacidad | owner | admin | auditor | member |
|---|:---:|:---:|:---:|:---:|
| Ver organización, proyectos y plugins | ✓ | ✓ | ✓ | ✓ |
| Evaluar políticas | ✓ | ✓ | ✓ | ✓ |
| Leer miembros, SSO, reglas y auditoría | ✓ | ✓ | ✓ | — |
| Mutar miembros, SSO, reglas, plugins y adscripciones | ✓ | ✓ | — | — |
| Asignar o modificar owners | ✓ | — | — | — |

Todos los permisos requieren `status=active`. La organización debe conservar
al menos un owner activo. El precheck HTTP mejora la respuesta normal y el
repositorio vuelve a aplicar la invariantes dentro de la transacción para
cubrir carreras concurrentes. Ambos rechazos producen `409
organization.last_owner` y un evento `denied`.

## Rutas

La base es `/v1/organizations`:

- `POST /` crea una organización; `GET /` lista las visibles.
- `GET /{organizationId}` obtiene una organización.
- `GET|POST /{organizationId}/members` lista o establece una membresía por
  email registrado.
- `GET|POST /{organizationId}/sso-connections` y `GET|PUT
  /{organizationId}/sso-connections/{connectionId}` administran SSO.
- `GET|POST /{organizationId}/policy-rules` y `GET|PUT|DELETE
  /{organizationId}/policy-rules/{ruleId}` administran reglas.
- `POST /{organizationId}/policy/evaluate` evalúa `{action, resource}`.
- `GET|POST /{organizationId}/plugins` y `GET|PATCH
  /{organizationId}/plugins/{registrationId}` administran plugins.
- `GET /{organizationId}/audit` pagina eventos y `GET
  /{organizationId}/audit/verify` verifica la cadena completa contra el
  checkpoint retenido.
- `GET /{organizationId}/projects` lista adscripciones y `POST
  /{organizationId}/projects/{projectId}/attach` adscribe un proyecto.

Para adscribir un proyecto, el actor debe ser owner o admin activo de la
organización y además owner del proyecto. Un proyecto ya adscrito a otro tenant
no puede moverse mediante esta API.

## Políticas

Una regla tiene `effect`, patrones de `actions` y `resources`, y una condición
opcional por roles. El evaluador normaliza y ordena reglas para obtener el mismo
resultado independientemente del orden de persistencia.

```json
{
  "description": "Members can inspect project resources",
  "effect": "allow",
  "actions": ["project.read"],
  "resources": ["project:**"],
  "conditions": {"roles": ["member"]},
  "disabled": false
}
```

`*` consume caracteres dentro de un segmento y `**` puede cruzar `:` o `/`.
Los patrones tienen presupuestos estrictos y no admiten expresiones regulares,
caracteres inseguros ni segmentos de traversal. La evaluación usa deny por
defecto y cualquier deny coincidente prevalece sobre los allow.

El cliente envía solamente:

```json
{"action":"project.read","resource":"project:restricted"}
```

El servidor deriva `subjectId` y `role` de la sesión y la membresía activa. Un
campo `role` enviado por el cliente es rechazado como JSON desconocido.

## Auditoría verificable

Cada evento incluye secuencia monotónica, hash anterior, hash propio, actor,
request ID, IP normalizada, resultado y metadatos JSON acotados. Los metadatos
de handlers contienen identificadores y decisiones operativas, no emails,
secretos SSO ni payloads arbitrarios.

La paginación usa un cursor exclusivo:

```http
GET /v1/organizations/{organizationId}/audit?afterSequence=120&limit=100
```

```json
{
  "items": [],
  "afterSequence": 120,
  "nextAfterSequence": 120,
  "limit": 100,
  "hasMore": false
}
```

`limit` acepta de 1 a 200. La verificación sincrónica recorre páginas internas
de hasta 1000 eventos y se limita a 100 000 eventos para mantener un presupuesto
operativo predecible. Valida contenido, secuencias, enlaces hash y el checkpoint,
por lo que también detecta una cola truncada que por sí sola parecería válida.

## Plugins

Un registro identifica de forma inmutable el artefacto por `pluginKey`, versión
semántica, URL HTTPS/OCI y checksum SHA-256. Empieza `disabled` salvo que se
solicite `active`. El estado `revoked` es terminal: cualquier cambio posterior
responde `409 plugin.revoked`.

El registro no descarga ni ejecuta código. La verificación del checksum y el
sandbox de ejecución pertenecen al runtime que consuma este control plane.

## Códigos relevantes

- `400 request.invalid_json`, `request.invalid_uuid` o `request.invalid_query`.
- `404 resource.not_found` para inexistencia, falta de rol o aislamiento tenant.
- `409 organization.last_owner`, `plugin.revoked` o `resource.conflict`.
- `413 request.too_large` para bodies Enterprise mayores de 64 KiB.
- `422 enterprise.invalid` y errores específicos de campos de membresía/plugin.
- `429 rate_limit.exceeded` y `503 admission.overloaded`.
