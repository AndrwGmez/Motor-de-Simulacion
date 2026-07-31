# Diseño del motor de simulación

## 1. Propiedades

El motor es:

- Determinista.
- Puramente simulado.
- Basado en tiempo lógico.
- Acotado por pasos y visitas.
- Persistente antes de emitir eventos.
- Controlable mediante pausa, step y cancelación.

No ejecuta nodos en paralelo real. Representa paralelismo mediante tokens con el
mismo tiempo lógico y los procesa en orden total. Esto elimina carreras y hace
reproducibles las pruebas.

## 2. Entrada

Una ejecución queda identificada por:

```text
snapshot/checksum del flujo
+ triggerNodeId
+ input
+ overrides
+ maxSteps
+ maxVisitsPerNode
= resultado determinista
```

`SimulationRequest` se define en
[`simulation-request.schema.json`](../packages/contracts/schemas/simulation-request.schema.json).

Antes de crear el run:

1. Autorizar acceso a la versión.
2. Validar snapshot y ejecutabilidad.
3. Verificar trigger.
4. Construir contexto con defaults e input.
5. Verificar overrides.
6. Aplicar idempotencia.
7. Persistir run `queued`.

## 3. Estado interno

### Run

```text
id
status
logicalTimeMs
sequence
priorityQueue
tokens
joinBarriers
visitCountByNode
stepCount
limits
playbackSpeed
cancel/pause control
hasUnhandledFailure
```

### Token

```text
tokenId
currentNodeId
availableAtMs
context
lineage[]
incomingEdgeId
```

Cada frame de `lineage` contiene `forkId`, branch ID, ramas esperadas e
iteración. Los IDs se generan con contadores del run, no UUID aleatorios, para
conservar reproducibilidad.

### Unidad de cola

Clave total:

```text
(availableAtMs, incomingEdge.priority, nodeId, tokenId)
```

No se depende del orden de iteración de mapas.

## 4. Ciclo principal

Pseudocódigo normativo:

```text
persist run.started
enqueue trigger token

while queue or barriers contain work:
  honor cancel
  wait while paused unless one step permit exists
  item = dequeue by total order
  logicalTime = max(logicalTime, item.availableAt)
  validate run and per-node limits
  visit = activate(item)

  if visit waits:
    persist node.waiting
    continue

  persist node.started
  outcome = execute node atomically

  if outcome failed:
    persist node.failed
    terminate branch and record unhandled failure
  else:
    persist node.completed
    choose outgoing edges
    for each selected edge in deterministic order:
      persist edge.traversed
      enqueue child token and persist node.queued

  if a step permit was consumed:
    transition to paused and persist run.paused

finalize aggregate result
persist one terminal run event
```

`node.queued` del trigger se emite después de `run.started`. El `sequence`
empieza en 1 y aumenta exactamente de uno en uno.

## 5. Ejecución por tipo

- `trigger`: crea/valida el contexto raíz.
- `process`: consume `durationMs` y aplica operaciones configuradas.
- `decision`: evalúa salidas sin mutar contexto.
- `data`: aplica operaciones atómicamente.
- `integration`: consume `durationMs + latencyMs` y devuelve éxito/fallo
  configurado.
- `delay`: consume `durationMs + delayMs`.
- `end`: registra resultado y termina la rama.
- `group`: nunca entra en la cola.

El tiempo se aplica al completar el nodo. Hijos reciben:

```text
availableAt = nodeStartLogicalTime + effectiveDuration
```

## 6. Operaciones de contexto

Cada visita trabaja sobre una copia estructural del contexto del token.

- `set`: escribe un literal.
- `copy`: copia profundamente el valor leído.
- `delete`: elimina; borrar una ruta ausente es idempotente.

Las operaciones se preparan en una copia y se confirman juntas. Una ruta
inválida, tipo incompatible o variable no declarada produce fallo sin cambios
parciales.

No se interpolan strings ni se evalúan expresiones.

## 7. Decisiones

Conexiones se ordenan por `(priority, edgeId)`.

### `first_match`

1. Evalúa no-default en orden.
2. Selecciona la primera verdadera.
3. Si ninguna coincide, selecciona default.

### `all_matches`

1. Evalúa todas las no-default.
2. Selecciona todas las verdaderas en orden.
3. Si ninguna coincide, selecciona default.

Una condición inválida debería haber bloqueado el run. Si el snapshot persistido
está corrupto, el nodo falla con `condition.invalid`; no se usa default para
ocultar el error.

Un override `force_edge`:

- Debe apuntar a una salida del decision visitado.
- Omite evaluación exclusivamente en esa visita si el alcance del override es
  global para el run.
- Se registra en payload/auditoría sin revelar contexto.

## 8. Forks

Un nodo no-decision con varias salidas o un decision `all_matches` crea un frame
de fork:

```text
forkId = siguiente contador
expectedBranches = IDs ordenados de conexiones seleccionadas
```

Cada hijo obtiene el mismo contexto base, una branch ID distinta y el frame
añadido a lineage. El procesamiento sigue siendo secuencial, pero
`availableAtMs` conserva la simultaneidad lógica.

## 9. Uniones

### `each`

No crea barrera; cada token sigue con su contexto.

### `any`

La clave de barrera es `(nodeId, forkId, iteration)`.

- Primer token: cierra barrera y ejecuta.
- Hermanos posteriores: `node.skipped` con razón `join.any_already_resolved`.
- Se elimina el frame correlacionado antes de continuar.

### `all`

- La barrera retiene un token por branch ID.
- Llegadas incompletas emiten `node.waiting`.
- Cuando están todas las `expectedBranches`, se combinan en orden de branch ID.
- Se elimina el frame correlacionado y se ejecuta una visita.

La validación bloquea joins cuya cobertura estáticamente no puede completarse.
Si una rama falla antes del join, las barreras dependientes se liberan como
fallo `join.branch_failed`, evitando una espera infinita.

## 10. Merge de contexto

Cada rama se compara con el contexto base del fork:

- Cambio en una sola rama: se acepta.
- Mismo valor final en varias ramas: se acepta.
- Cambios distintos en rutas disjuntas: se aceptan.
- Valores diferentes en la misma ruta: `context.merge_conflict`.
- Delete contra modificación de la misma ruta/descendiente: conflicto.
- Modificar padre y descendiente en ramas distintas: conflicto.

Los conflictos se ordenan por JSON Pointer y se reportan sin incluir valores
sensibles en logs públicos. No existe “última escritura gana”.

## 11. Ciclos

Se permite regresar a nodos visitados. Cada token conserva lineage e iteración;
el motor mantiene:

- `stepCount` global.
- Visitas globales por nodo.
- Opcionalmente visitas por token para diagnóstico.

Defaults:

- `maxSteps = 10_000`.
- `maxVisitsPerNode = 100`.

Al exceder un límite:

1. No inicia la siguiente visita.
2. Emite `run.limit_exceeded` con tipo de límite y conteos.
3. Marca tokens restantes como no procesados.
4. Finaliza run `failed`.

El validador bloquea SCC sin ninguna salida; un ciclo con salida es warning
porque sus condiciones pueden no terminar para ciertos inputs.

## 12. Fallos

Un `fail_node` override o error configurado produce:

1. `node.started`.
2. `node.failed`.
3. Terminación de la rama.
4. `hasUnhandledFailure = true`.

El MVP no modela puertos de error. Otras ramas ya creadas pueden terminar para
producir un diagnóstico completo, pero el resultado agregado será `failed`.

Códigos iniciales:

- `node.forced_failure`
- `integration.simulated_failure`
- `condition.invalid`
- `operation.invalid`
- `context.merge_conflict`
- `join.branch_failed`
- `run.max_steps`
- `run.max_visits_per_node`
- `run.deadlock`

## 13. Resultado agregado

Cuando no hay cola ni barreras:

- Si hubo fallo no manejado: `failed`.
- Si no se alcanzó ningún `end`: `failed` con `run.no_end_reached`.
- Si algún `end.result` es failure: `failed`.
- Si todos los finales alcanzados son success: `completed`.

El resultado guarda ruta por token, tiempos, finales, issues y output controlado.
El contexto completo permanece privado.

## 14. Controles concurrentes

Todas las órdenes llegan al propietario en memoria del run mediante un canal
serializado.

### Pausa

- Estado permitido: `running`.
- Se aplica después de la transición atómica actual.
- Repetir pausa es idempotente a nivel de resultado HTTP o devuelve conflicto
  estable; nunca duplica evento.

### Reanudar

- Estado permitido: `paused`.
- Emite `run.resumed` antes de tomar nueva unidad.

### Step

- Estado permitido: `paused`.
- Concede un permiso.
- Procesa llegadas a barreras necesarias hasta completar/fallar exactamente una
  visita.
- Vuelve a `paused` y emite un único `run.paused`.
- Si solo existe un deadlock de barreras, falla en lugar de esperar.

### Velocidad

- Multiplicadores: `0.25`, `0.5`, `1`, `2`, `4`.
- Solo controla pacing entre eventos visibles.
- No cambia `logicalTimeMs`, ruta, análisis ni checksum.

### Cancelación

- Se comprueba entre transiciones y durante pacing cancelable.
- Emite `run.cancelled`.
- Conserva eventos y resultados parciales.

## 15. Persistencia y publicación

La transición lógica y sus registros se escriben en una transacción:

- Estado de `runs`.
- `node_runs` cuando corresponda.
- Uno o más `run_events` con sequence reservado.

Solo después del commit se publica cada evento a suscriptores. Por ello un
cliente nunca observa un evento que luego no puede recuperar por HTTP/replay.

Un suscriptor lento no bloquea el motor: se cierra su conexión y podrá reanudar.

## 16. Reinicio de proceso

El MVP es de instancia única y no persiste la cola interna completa.

Al arrancar:

1. Buscar runs `queued`, `running` o `paused` cuyo owner ya no existe.
2. Cambiar a `interrupted`.
3. Insertar `run.interrupted` con nueva sequence.
4. Permitir reiniciar como run nuevo.

No se finge una reanudación exacta.

## 17. Pruebas doradas

Cada fixture fija la secuencia completa de eventos:

- Lineal.
- Decision first/default/all.
- Fork anidado.
- Join each/any/all.
- Merge compatible y conflictivo.
- Ciclo que termina y límites excedidos.
- Delay sin espera real.
- Fallo de integración y override.
- Pausa, step, resume y cancelación.

La prueba ejecuta cada caso varias veces y compara eventos ignorando únicamente
`occurredAt`; IDs deterministas, sequence, logical time, payload y resultado
deben coincidir.

