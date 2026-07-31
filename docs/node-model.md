# Modelo funcional de nodos

Este documento define el comportamiento de los ocho nodos del contrato
`FlowDefinition 1.0`. La forma normativa de los datos está en
[`flow-definition.schema.json`](../packages/contracts/schemas/flow-definition.schema.json).

## 1. Estructura común

```json
{
  "id": "validate-payment",
  "type": "process",
  "label": "Validar pago",
  "description": "Consulta el estado simulado del proveedor.",
  "inputs": [{ "id": "input", "label": "Entrada" }],
  "outputs": [{ "id": "next", "label": "Continuar" }],
  "activationMode": "each",
  "durationMs": 350,
  "configuration": { "operations": [] },
  "position": { "x": 120, "y": -40, "z": 80 },
  "locked": false,
  "metadata": {
    "category": "payments",
    "color": "#8B5CF6",
    "tags": ["critical"],
    "groupId": "payments-group"
  }
}
```

### 1.1 Campos

| Campo | Regla |
|---|---|
| `id` | Único, estable, 1–64 caracteres y patrón alfanumérico con `_`/`-` |
| `type` | Uno de los ocho tipos registrados |
| `label` | Texto visible, 1–120 caracteres |
| `description` | Texto opcional, máximo 2.000 caracteres |
| `inputs`/`outputs` | Puertos con ID único dentro de su lado |
| `activationMode` | `each`, `any` o `all` |
| `durationMs` | Tiempo lógico base, no una espera de pared |
| `configuration` | Estructura discriminada por `type` |
| `position` | Coordenadas finitas entre −100.000 y 100.000 |
| `locked` | Evita que layouts/física cambien la posición |
| `metadata` | Categoría, color, tags y pertenencia a grupo |

La etiqueta y descripción se tratan como texto, nunca como HTML. `metadata.color`
es una preferencia; los estados de ejecución tienen precedencia visual.

## 2. Puertos y conexiones

Un puerto tiene:

```json
{ "id": "approved", "label": "Aprobado" }
```

Reglas semánticas:

- Un ID de puerto es único dentro de `inputs` o `outputs` del nodo.
- Toda conexión referencia un puerto de salida de `source` y uno de entrada de
  `target`.
- `trigger` no tiene entradas.
- `end` no tiene salidas.
- `group` no tiene puertos.
- Los nodos ejecutables intermedios tienen al menos una entrada y una salida.
- `decision` tiene al menos dos salidas.
- Eliminar un puerto exige eliminar o reconectar sus conexiones en la misma
  operación del editor.
- Un puerto puede participar en varias conexiones.

## 3. Modos de activación

### `each`

Cada token entrante crea una visita independiente. Es el valor normal para
procesos y obligatorio para `trigger` y `decision`.

### `any`

Funciona como unión competitiva. Para un mismo fork, el primer token que llega
ejecuta el nodo; tokens hermanos posteriores producen `node.skipped`. Tokens de
forks diferentes siguen siendo ejecuciones independientes.

### `all`

Funciona como barrera. El nodo entra en `waiting` hasta recibir todas las ramas
hermanas esperadas del fork correlacionado. Después combina sus contextos y se
ejecuta una vez.

No se permite `all` si el análisis no puede determinar las ramas esperadas o si
alguna no puede alcanzar la unión. Un join no mezcla tokens de ejecuciones,
forks o iteraciones distintas.

## 4. Tipos

### 4.1 `trigger`

Inicia un run:

```json
{
  "type": "trigger",
  "configuration": { "eventName": "order.received" }
}
```

- Cero inputs y al menos un output.
- `activationMode` debe ser `each`.
- `durationMs` normalmente es cero.
- Un flujo puede tener varios, pero `SimulationRequest.triggerNodeId` selecciona
  exactamente uno.
- Crea el token raíz y el contexto a partir de variables predeterminadas más la
  entrada validada.

Representación: esfera hueca con halo.

### 4.2 `process`

Representa una tarea simulada:

```json
{
  "type": "process",
  "durationMs": 500,
  "configuration": {
    "operations": [
      { "op": "set", "path": "/order/status", "value": "validated" }
    ]
  }
}
```

- Aplica operaciones en orden después de consumir `durationMs`.
- Si una operación falla por ruta/tipo, el nodo falla y no recorre salidas.
- Sin operaciones, solo consume tiempo lógico y registra resultado.
- Varias salidas incondicionales crean ramas paralelas.

Representación: cubo con bordes redondeados visualmente.

### 4.3 `decision`

Evalúa condiciones sobre una copia inmutable del contexto de entrada:

```json
{
  "type": "decision",
  "configuration": { "strategy": "first_match" }
}
```

- Debe tener una y solo una conexión `isDefault: true`, sin `condition`.
- Toda conexión no default debe tener `condition`.
- Prioridad menor se evalúa primero; un empate se resuelve por ID de conexión.
- `first_match` recorre la primera condición verdadera.
- `all_matches` recorre todas las verdaderas y crea ramas.
- Default se recorre solo si ninguna condición coincide.
- Evaluar no modifica el contexto.
- `activationMode` debe ser `each`.

Representación: octaedro/diamante.

### 4.4 `data`

Realiza transformaciones controladas:

```json
{
  "type": "data",
  "configuration": {
    "operations": [
      { "op": "copy", "from": "/customer/id", "path": "/order/customerId" },
      { "op": "delete", "path": "/temporary" }
    ]
  }
}
```

Operaciones:

- `set`: asigna un literal JSON.
- `copy`: lee una ruta y escribe su copia profunda en otra.
- `delete`: elimina una ruta opcional.

Todas las rutas son JSON Pointer y deben corresponder a variables declaradas.
Las operaciones son atómicas: ante un error no se conserva un cambio parcial.

Representación: cilindro.

### 4.5 `integration`

Representa un sistema externo sin contactarlo:

```json
{
  "type": "integration",
  "durationMs": 50,
  "configuration": {
    "service": "Demo Logistics",
    "latencyMs": 800,
    "outcome": "success",
    "response": { "trackingId": "SIM-0001" }
  }
}
```

- Nunca realiza red, I/O externo ni usa credenciales reales.
- Duración efectiva: `durationMs + latencyMs`.
- `success` conserva la respuesta simulada en el resultado de la visita.
- `failure` produce `node.failed` con `errorCode` sanitizado.
- Un override `fail_node` tiene precedencia sobre la configuración.

Representación: prisma hexagonal.

### 4.6 `delay`

Avanza el reloj lógico:

```json
{
  "type": "delay",
  "durationMs": 10,
  "configuration": { "delayMs": 3600000 }
}
```

- Duración efectiva: `durationMs + delayMs`.
- No mantiene un goroutine dormido por el período configurado.
- El pacing visual traduce el salto lógico a una espera corta y cancelable.
- Pausa/cancelación se atienden en el límite de transición.

Representación: toro/anillo.

### 4.7 `end`

Finaliza una rama:

```json
{
  "type": "end",
  "configuration": {
    "result": "success",
    "output": { "status": "delivered" }
  }
}
```

- Al menos un input y cero outputs.
- El resultado puede ser `success` o `failure`.
- Un run termina cuando no quedan tokens ejecutables o en espera.
- Si cualquier final alcanzado es failure, el resultado agregado es failure;
  si todos los alcanzados son success, es success.
- Un run sin final alcanzado es inválido/fallido, no éxito implícito.

Representación: esfera sólida.

### 4.8 `group`

Contenedor exclusivamente visual:

```json
{
  "type": "group",
  "configuration": { "collapsed": false }
}
```

- Cero puertos, cero conexiones y `durationMs: 0`.
- No crea visitas ni eventos.
- Los miembros lo referencian mediante `metadata.groupId`.
- Agrupar o desagrupar no cambia la topología funcional.
- Ocultar/colapsar afecta únicamente la vista.

Representación: caja transparente con etiqueta.

## 5. Condiciones

### Comparación

```json
{
  "field": "/payment/status",
  "operator": "equals",
  "value": "approved"
}
```

### Existencia

```json
{
  "field": "/trackingId",
  "operator": "exists"
}
```

### Composición

```json
{
  "operator": "and",
  "conditions": [
    {
      "field": "/payment/status",
      "operator": "equals",
      "value": "approved"
    },
    {
      "field": "/inventory/available",
      "operator": "equals",
      "value": true
    }
  ]
}
```

Semántica:

- `equals` no convierte tipos (`1` no equivale a `"1"`).
- Comparaciones de orden requieren números contra números.
- `contains` opera sobre string o array; conserva comparación estricta.
- Leer una ruta inexistente solo es válido para `exists`/`not_exists`.
- `and` y `or` cortocircuitan de izquierda a derecha.
- Profundidad máxima del AST: 10; máximo 20 hijos por compuesto.

## 6. Estados de una visita

```text
idle → queued → running → success
                   │        └ failed
                   └ waiting → queued
idle/queued/waiting → skipped
```

El estado visual de un nodo agrega visitas: `failed` tiene mayor precedencia,
seguido por `running`, `waiting`, `queued`, `success`, `skipped` e `idle`. El
panel de detalle siempre muestra las visitas individuales para no perder
información en ciclos o paralelismo.

## 7. Validaciones por tipo

| Código estable | Severidad | Situación |
|---|---|---|
| `node.ports.invalid` | error | Puertos incompatibles con el tipo |
| `node.configuration.invalid` | error | Configuración incompleta |
| `node.variable.undeclared` | error | Ruta no declarada |
| `decision.default.missing` | error | No existe salida default |
| `decision.default.multiple` | error | Más de una salida default |
| `decision.condition.missing` | error | Salida no default sin condición |
| `group.connected` | error | Conexión funcional a un grupo |
| `join.unreachable_branch` | error | `all` nunca podrá liberar la barrera |
| `node.disconnected` | warning | Nodo sin conexión funcional |

Los códigos son contrato; mensajes y traducciones pueden evolucionar.

