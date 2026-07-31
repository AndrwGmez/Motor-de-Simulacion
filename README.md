# FlowVerse 3D

FlowVerse 3D es una plataforma web para diseñar, importar, visualizar, validar,
simular y analizar procesos como grafos tridimensionales dirigidos. El MVP
simula procesos de forma determinista; no ejecuta código ni acciones externas.

## Estructura

```text
apps/
  api/                 API, persistencia, validación y simulación en Go
  web/                 Editor y visualizador 3D en Next.js
packages/
  contracts/           JSON Schema, OpenAPI, AsyncAPI y fixtures canónicos
docs/                  Especificaciones funcionales y técnicas
compose.yaml           Entorno local reproducible
```

Los dos productos de `apps/` son desplegables de forma independiente. Los
contratos de `packages/contracts` son la fuente de verdad compartida.

## Requisitos

- Node.js 24 LTS
- pnpm 10
- Go 1.26
- Docker con Compose v2

## Inicio rápido

1. Copia `.env.example` como `.env` y cambia los secretos si el entorno no es
   exclusivamente local.
2. Ejecuta `corepack enable && pnpm install --frozen-lockfile`.
3. Valida los contratos con `pnpm contracts:check`.
4. Inicia la plataforma con `docker compose up --build`.
5. Abre `http://localhost:3000`; la API se sirve en
   `http://localhost:8080`.

Para ejecutar componentes fuera de Docker:

```bash
pnpm --filter @flowverse/web dev
cd apps/api && go run ./cmd/api
```

El parser usa `mock` de forma predeterminada: es determinista y no realiza
solicitudes externas. Para usar OpenAI hay que configurar explícitamente
`FLOW_PARSER_PROVIDER=openai`, `OPENAI_API_KEY` y, opcionalmente,
`OPENAI_MODEL`. En ambos modos el resultado es una previsualización validada;
nunca se ejecuta ni se persiste automáticamente.

## Comandos

| Comando | Propósito |
|---|---|
| `make contracts` | Valida esquemas, especificaciones y fixtures |
| `make test` | Ejecuta pruebas unitarias de contratos, API y web |
| `make test-all` | Añade race detector y Playwright |
| `make lint` | Ejecuta lint de JavaScript/TypeScript y `go vet` |
| `make build` | Compila web, paquetes y API |
| `make compose-build` | Reconstruye ambas imágenes desde cero |
| `make up` | Inicia el stack local completo |
| `make down` | Detiene el stack |

## Verificación actual

El estado actual se verificó con contratos, 30 pruebas web, la suite Go,
`go test -race`, `go vet`, build, integración del repositorio con PostgreSQL y
cuatro escenarios Playwright. Los E2E actuales usan el modo demo del frontend;
el recorrido de navegador contra API/PostgreSQL/WebSocket reales permanece como
gate posterior.

También se realizó un smoke manual con PostgreSQL, API y web saludables mediante
Compose. [La estrategia de pruebas](docs/testing.md) distingue la suite
implementada de los objetivos pendientes de accesibilidad, carga, cobertura y
E2E full-stack automatizado.

## Contratos y documentación

- [Especificación técnica](docs/flowverse-3d-especificacion-tecnica.md)
- [Modelo funcional de nodos](docs/node-model.md)
- [Contrato del flujo](docs/flow-contract.md)
- [Experiencia tridimensional](docs/3d-experience.md)
- [Motor de simulación](docs/simulation-engine.md)
- [API y tiempo real](docs/api-realtime.md)
- [Estrategia de pruebas](docs/testing.md)
- [OpenAPI](packages/contracts/openapi.yaml)
- [AsyncAPI](packages/contracts/asyncapi.yaml)

## Principios de seguridad

- Las condiciones y transformaciones son datos estructurados; nunca JavaScript.
- La salida de IA siempre se normaliza, valida y presenta como previsualización.
- Las versiones publicadas son inmutables.
- Cada acceso se autoriza en el proyecto correspondiente.
- Los enlaces públicos omiten entradas, salidas y contextos privados.

## Estado

La versión `0.1.0` implementa una primera vertical funcional del MVP. Los
documentos distinguen las capacidades actuales de los gates de endurecimiento y
del roadmap. Integraciones reales, colaboración simultánea, VR, BPMN y
marketplace quedan fuera del MVP.
