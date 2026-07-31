# Contrato JSON del flujo

`FlowDefinition` es la representación canónica que comparten editor, API,
simulador, importador y versiones publicadas.

- Esquema normativo:
  [`flow-definition.schema.json`](../packages/contracts/schemas/flow-definition.schema.json)
- Ejemplo completo:
  [`order-processing.flow.json`](../packages/contracts/fixtures/valid/order-processing.flow.json)
- Draft: JSON Schema 2020-12.
- Versión inicial: `schemaVersion: "1.0"`.

## 1. Sobre raíz

```json
{
  "schemaVersion": "1.0",
  "name": "Procesamiento de pedidos",
  "description": "Flujo de demostración",
  "metadata": {
    "tags": ["orders"],
    "createdWith": "FlowVerse 3D"
  },
  "variables": [],
  "layout": { "mode": "directional" },
  "nodes": [],
  "edges": []
}
```

El ID de base de datos, proyecto, número de versión, autor y fechas no forman
parte de `FlowDefinition`: pertenecen al recurso HTTP `flow`/`flow_version`.
Así, importar y exportar no arrastra propiedad ni credenciales.

## 2. Campos

| Campo | Obligatorio | Regla |
|---|---|---|
| `schemaVersion` | Sí | Exactamente `"1.0"` |
| `name` | Sí | 1–120 caracteres |
| `description` | No | Máximo 4.000 caracteres |
| `metadata` | No | Tags y herramienta de origen |
| `variables` | Sí | Hasta 250 declaraciones |
| `layout` | Sí | Modo, agrupación y cámara opcional |
| `nodes` | Sí | 1–500 nodos |
| `edges` | Sí | 0–1.000 conexiones |

Se rechazan propiedades desconocidas. Esto detecta errores tipográficos y evita
que cada cliente invente extensiones incompatibles.

## 3. Variables y contexto

```json
{
  "path": "/payment/status",
  "type": "string",
  "required": true,
  "description": "Estado simulado del pago",
  "default": "pending"
}
```

- `path` usa JSON Pointer RFC 6901, no notación con puntos.
- Las rutas son únicas.
- No se declara la raíz vacía como variable editable.
- `default`, cuando existe, coincide con `type`.
- Un input puede sobrescribir un default.
- Una variable `required` sin default debe estar presente en el input.
- Los objetos de entrada no pueden declarar rutas fuera del catálogo.
- Las operaciones y condiciones solo referencian rutas declaradas.

El contexto de ejecución es un árbol JSON privado del run. No se incluye en
enlaces públicos.

## 4. Layout

```json
{
  "mode": "clusters",
  "clusterBy": "category",
  "camera": {
    "position": { "x": 0, "y": 100, "z": 600 },
    "target": { "x": 0, "y": 0, "z": 0 }
  }
}
```

- Modos: `force`, `directional`, `layers`, `timeline`, `clusters`,
  `execution`.
- `clusterBy` solo tiene efecto en `clusters`.
- La cámara guardada es una preferencia inicial, no estado funcional.
- Cada nodo conserva posición aunque el layout actual la recalcule.
- `locked: true` impide cambiarla automáticamente.

## 5. Nodos

La definición discriminada está en [node-model.md](node-model.md). Invariantes
generales no expresables completamente en JSON Schema:

- IDs únicos de nodo.
- IDs de puerto únicos por lado.
- IDs de nodo y edge pertenecen a espacios separados.
- `metadata.groupId`, cuando existe, referencia un nodo `group`.
- Un grupo no puede pertenecer a sí mismo ni crear ciclos de agrupación.
- Los puertos y configuración son compatibles con el tipo.

## 6. Conexiones

```json
{
  "id": "edge-payment-approved",
  "source": "payment-approved",
  "target": "check-inventory",
  "sourcePort": "approved",
  "targetPort": "input",
  "label": "Sí",
  "condition": {
    "field": "/payment/status",
    "operator": "equals",
    "value": "approved"
  },
  "priority": 0,
  "isDefault": false
}
```

| Campo | Regla |
|---|---|
| `id` | Único dentro de `edges` |
| `source`/`target` | Referencian nodos existentes y no `group` |
| `sourcePort` | Existe en `source.outputs` |
| `targetPort` | Existe en `target.inputs` |
| `label` | Texto opcional, máximo 120 caracteres |
| `condition` | AST opcional; solo en salidas de `decision` |
| `priority` | Entero 0–10.000; menor se evalúa primero |
| `isDefault` | Solo una por `decision` |

En un nodo que no es decisión, varias salidas son paralelas. No se interpreta la
prioridad como exclusión.

## 7. Validación en capas

### 7.1 Sintáctica

- JSON bien formado y UTF-8.
- Cuerpo máximo de 1 MB.
- Sin contenido adicional después del documento.

El decoder actual todavía no detecta propiedades JSON duplicadas; añadir esa
protección queda como endurecimiento pendiente.

### 7.2 JSON Schema

- Campos, tipos, enumeraciones, longitudes y máximos.
- Configuración discriminada.
- Forma recursiva de condiciones.
- JSON Pointer e identificadores.

### 7.3 Semántica

- Unicidad y referencias.
- Compatibilidad de variables y operadores.
- Reglas de puertos por tipo.
- Default de decisiones.
- Trigger y terminaciones alcanzables.
- Ciclos y joins posibles.

### 7.4 Ejecutabilidad

El `SimulationRequest` añade:

- Trigger seleccionado existente.
- Input conforme a variables requeridas.
- Overrides referenciando nodos/edges existentes.
- Límites dentro de los máximos admitidos.

Un documento puede guardarse con warnings, pero no publicarse/ejecutarse con
errores.

## 8. Normalización

La importación JSON es estricta y no corrige silenciosamente datos inválidos. El
normalizador del editor/parser sí puede:

- Recortar espacios exteriores de etiquetas.
- Generar IDs válidos para elementos nuevos.
- Completar descripciones vacías y metadatos opcionales.
- Añadir configuración y puertos predeterminados del tipo.
- Convertir posiciones ausentes en posiciones calculadas antes de validar.
- Ordenar arrays solo para visualización, no para alterar prioridad.

No puede:

- Convertir tipos de valores.
- Inventar condiciones ambiguas.
- Eliminar nodos o conexiones.
- Convertir código a expresiones.
- Cambiar `schemaVersion`.

Toda corrección material se muestra en la previsualización.

## 9. Identidad, checksum y ETag

La implementación actual normaliza `FlowDefinition`, lo serializa con
`encoding/json` de Go y calcula SHA-256 sobre esos bytes. Esta representación es
determinista para el mismo documento normalizado, pero no implementa
canonicalización JSON RFC 8785 entre lenguajes.

- ETag fuerte: `"<hex de 64 caracteres>"`, sin prefijo `sha256:`.
- El checksum de una versión reutiliza actualmente esa misma representación
  citada.
- Dos documentos que producen la misma representación normalizada de Go
  producen el mismo ETag.
- El orden de `nodes`, `edges` y operaciones sí es significativo y no se
  reordena al calcular el hash.

Ejemplo:

```http
ETag: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
```

`PUT /v1/flows/{flowId}/draft` exige el último `If-Match`. Un mismatch produce:

```json
{
  "code": "draft.conflict",
  "message": "Draft changed since it was loaded",
  "requestId": "...",
  "details": null
}
```

## 10. Publicación

La publicación actual:

1. Verifica permisos e `If-Match`.
2. Valida el documento.
3. Consulta las versiones existentes y asigna el siguiente número.
4. Inserta snapshot, checksum, autor y fecha.
5. Conserva el borrador para futuras ediciones.

La restricción única de PostgreSQL impide dos versiones con el mismo número,
pero la numeración concurrente todavía no está serializada mediante bloqueo de
la fila del flujo. Ese caso requiere una prueba y endurecimiento adicionales.
La tabla `audit_logs` existe, pero la publicación aún no emite su registro.

Una versión publicada nunca se actualiza. Un run guarda `flowVersionId`,
checksum y, para borradores permitidos en desarrollo, un snapshot autónomo.

## 11. Compatibilidad

### Cambios compatibles en 1.0

- Añadir campos opcionales solo si clientes anteriores los rechazan de forma
  controlada y existe normalización previa.
- Ampliar mensajes humanos o metadatos fuera del documento.
- Añadir códigos de warning.

### Cambios incompatibles

- Renombrar/eliminar campos.
- Alterar semántica de operadores.
- Añadir un tipo de nodo o evento que un cliente deba interpretar.
- Cambiar defaults conductuales.
- Modificar límites de una forma que invalide documentos publicados.

Requieren `schemaVersion: "2.0"`, nuevo esquema, migrador puro
`1.0 → 2.0`, fixtures antes/después y pruebas de round-trip. La API conserva el
lector anterior mientras existan versiones publicadas de esa versión.

## 12. Exportación

La descarga usa:

- `Content-Type: application/json`.
- UTF-8.
- Nombre seguro `<flow-name>-v<version>.flow.json`.
- Documento sin IDs de usuario/proyecto, tokens, historial ni inputs de runs.

Importar el archivo exportado y volver a exportarlo debe conservar el checksum
de la serialización normalizada actual, salvo que el usuario confirme una
migración de esquema.
