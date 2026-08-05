# Estrategia y estado de pruebas

## 1. Cómo leer este documento

Este documento separa deliberadamente:

- la suite que existe y se ejecuta hoy;
- las verificaciones manuales realizadas sobre el stack;
- el backlog de calidad que todavía no es un gate de CI.

Un objetivo futuro no debe interpretarse como cobertura ya implementada.

## 2. Estado verificado

La matriz de CI vigente verifica:

| Área | Verificación actual |
|---|---|
| Contratos | OpenAPI, AsyncAPI, ocho JSON Schema, fixtures y contratos empresariales |
| Paquetes | Core, engine, CLI, codegraph y viewer con sus suites de Node |
| Web | Pruebas unitarias, de servicios, hooks y componentes |
| API | Suite Go unitaria, HTTP, runtime, empresas y persistencia |
| Concurrencia Go | `go test -race ./...` |
| Análisis Go | `go vet ./...` y build del binario |
| PostgreSQL | Migraciones y repositorios principal/empresarial contra PostgreSQL real |
| Navegador | 6 escenarios Playwright en modo demo |
| Navegador full-stack | 5 escenarios Playwright contra Compose, API, PostgreSQL y WebSocket |
| Contenedores | Build de las imágenes y smoke manual del stack Compose |

El job del API también aplica un umbral global mínimo de cobertura del 50 %. Los
conteos de casos individuales no se fijan aquí porque crecen con cada capacidad;
la ejecución de CI es la fuente de verdad.

## 3. Comandos reproducibles

Desde la raíz:

```bash
pnpm contracts:check
pnpm lint
pnpm typecheck
pnpm test
make test-all
make build
docker compose build
```

La integración PostgreSQL del API requiere una base real:

```bash
cd apps/api
TEST_DATABASE_URL='postgres://flowverse:flowverse_local_password@localhost:55433/flowverse?sslmode=disable' \
  go test ./internal/store -run TestPostgresRepositoryIntegration
```

Sin `TEST_DATABASE_URL`, esa prueba se omite de forma explícita. CI sí configura
PostgreSQL y la ejecuta.

## 4. Contratos implementados

El paquete `@flowverse/contracts`:

- compila los ocho JSON Schema con Ajv 2020;
- analiza OpenAPI y AsyncAPI y rechaza claves YAML duplicadas;
- comprueba referencias locales y `operationId` duplicados;
- valida el fixture positivo y confirma el rechazo del negativo;
- valida contratos de diff, escenarios, incidentes, copiloto y gobierno
  empresarial;
- genera y valida de forma determinista un grafo de 500 nodos y 1.000 edges.

Esto comprueba la coherencia de los artefactos de contrato. Todavía no sustituye
una prueba automática de cada respuesta HTTP contra OpenAPI.

## 5. Backend implementado

La suite Go cubre actualmente:

- Argon2id, registro, login, refresh rotatorio y detección de reutilización;
- evaluación de condiciones y mutaciones JSON Pointer;
- validación, análisis, ciclos, rutas y camino crítico;
- simulación determinista, decisiones, fan-out, joins, overrides y límites;
- diff semántico y restauración segura de versiones;
- Scenario Lab, informes de incidente y grounding del copiloto;
- parser mock y adaptador OpenAI con transporte simulado;
- repositorio en memoria, aislamiento, ETag, idempotencia y coordinación de runs;
- ciclo HTTP principal con memoria: auth, proyecto, draft, conflicto, publicación,
  share, importación y parser;
- rate limiting y headers CORS;
- runtime, replay, tickets, controles, persistencia previa a publicación y
  recuperación segura de runs sin propietario activo;
- organizaciones, membresías, SSO metadata, políticas, plugins, cuotas y cadena
  de auditoría verificable;
- repositorio PostgreSQL: migración, usuario/proyecto/flujo, conflicto ETag,
  snapshot/eventos de run y persistencia empresarial.

`go test -race ./...` forma parte de la verificación y del CI.

La integración PostgreSQL actual prueba el repositorio. Una matriz HTTP completa
contra PostgreSQL —RBAC, auth, shares, idempotencia concurrente y WebSocket— sigue
en el backlog.

## 6. Frontend implementado

Vitest/jsdom cubre:

- store de flujo, historial y edición;
- validación y simulación local;
- reducer/estado visual de ejecución;
- adaptadores HTTP de cuenta y flujo;
- compatibilidad con el contrato canónico;
- autosave serializado, cambios de flujo y cierre de página;
- diff/restauración, Scenario Lab, Time Machine y copiloto;
- consola y adaptadores del plano de control empresarial;
- controles de ejecución.

El frontend no tiene todavía un porcentaje mínimo independiente. Sí es un gate
que pasen lint, tipos, todas sus pruebas y el build de producción.

## 7. E2E implementado

Hay dos suites Playwright con propósitos distintos.

### 7.1 Modo demo (`apps/web/e2e`, `pnpm test:e2e`)

Seis escenarios que usan el fallback local del frontend: no registran un usuario
real, no levantan la API y no tocan PostgreSQL. Cubren la interacción del editor
de forma rápida y sin dependencias.

### 7.2 Pila real (`apps/web/e2e-fullstack`, `make test-e2e-full`)

Cinco escenarios contra las mismas imágenes que se despliegan, levantadas con
Docker Compose:

1. registro con sesión persistida en la API;
2. creación de proyecto y flujo con el editor 3D montado;
3. edición guardada en la API y conservada al recargar;
4. simulación ejecutada por el motor de Go con sus eventos recibidos por
   WebSocket (`run.started` … `run.completed`);
5. publicación de una versión inmutable y enlace público abierto sin sesión.

Esta suite es la que atraviesa el contrato compartido en ambas direcciones. El
stack se levanta fuera de Playwright, así que la ejecución local usa
`make test-e2e-full`; repetir la suite contra un stack ya arrancado puede topar
con el rate limit de `auth.register`, que se reinicia al recrear el contenedor.

## 8. Smoke manual full-stack

También se verificó manualmente:

- `docker compose up --build`;
- PostgreSQL, API y web en estado saludable;
- web en `http://localhost:3000`;
- liveness/readiness del API en `http://localhost:8080`;
- API conectada al PostgreSQL del stack;
- creación y consulta básica mediante la superficie HTTP.

Este smoke confirmó que las piezas arrancan juntas antes de que existiera la
suite full-stack descrita en 7.2, que ya cubre ese recorrido de forma
automatizada.

## 9. CI actual

Los jobs vigentes son:

1. contratos;
2. PR Flight Check con la acción compuesta del repositorio;
3. API con PostgreSQL, tests, cobertura mínima del 50 %, race detector, vet y
   build;
4. web y paquetes con lint, tipos, unit tests y build;
5. los seis Playwright demo con Chromium;
6. los cinco Playwright full-stack contra Compose, API, PostgreSQL y WebSocket;
7. build de imágenes Docker después de los gates anteriores.

## 10. Backlog de pruebas y gates de release

Antes de considerar el MVP endurecido para producción se deben añadir:

### Integración y E2E real

- matriz owner/editor/viewer sobre HTTP;
- dos clientes provocando y recuperando un conflicto ETag;
- `Idempotency-Key` repetida con payload igual y diferente;
- dos réplicas disputando/reclamando una ejecución y recuperando una lease
  vencida;
- WebSocket real con pausa, step, reconexión y replay paginado sin huecos;
- expiración, consumo único y cruce de run de tickets;
- share vencido, revocado y asociado a runs permitidos;
- recuperación del navegador tras error de autosave.

### Contrato y base de datos

- validar ejemplos y respuestas HTTP directamente contra OpenAPI;
- publicación concurrente y numeración de versiones;
- migraciones y upgrades desde cada versión soportada, no solo desde cero;
- estrategia de rollback o roll-forward documentada y probada;
- fallos de persistencia inyectados contra PostgreSQL.

### Seguridad

- CSRF negativo en todas las mutaciones con cookie;
- payload mayor de 24 MiB;
- viewer intentando mutar;
- recursos de otro proyecto;
- denegaciones de política empresarial en rutas de datos de proyecto;
- labels con HTML/script;
- errores sin SQL, stack, tokens ni contexto privado;
- pruebas del rate limit detrás del proxy de despliegue.

### Accesibilidad

- axe sobre pantallas principales;
- navegación completa por teclado;
- gestión de foco en diálogos;
- recorrido manual con lector de pantalla;
- contraste, reduced motion y estados que no dependan solo del color.

### Rendimiento y carga

- medición reproducible del fixture 500/1.000 en navegador;
- validación/análisis de grafos grandes;
- ráfagas de eventos y uso de memoria;
- creación/listado y replay WebSocket bajo carga;
- presupuesto de FPS en una máquina de referencia controlada.

Ninguna de estas pruebas de carga o accesibilidad está activa hoy en CI.

## 11. Cobertura

El backend exige al menos 50 % global en CI. Este valor es una red mínima, no una
medida suficiente de calidad: los invariantes de concurrencia, aislamiento,
idempotencia, políticas, simulación y persistencia se verifican además con casos
dirigidos, race detector e integración real con PostgreSQL.

Los paquetes y el frontend todavía no publican tendencias ni aplican umbrales
por archivo. El siguiente paso es medir una línea base por paquete y elevar gates
en rutas críticas sin incentivar pruebas superficiales.

## 12. Criterio de aceptación

Para el estado actual, una entrega requiere:

- contratos, lint, typecheck, pruebas Go/web y build en verde;
- race detector en verde;
- cobertura global del backend igual o superior al 50 %;
- integración PostgreSQL en verde dentro de CI;
- PR Flight Check en verde;
- seis Playwright demo en verde;
- cinco Playwright full-stack en verde;
- imágenes construibles.

Los gates de accesibilidad, carga y cobertura se incorporarán cuando
las suites correspondientes existan; hasta entonces se reportan como backlog y
no como capacidades verificadas.
