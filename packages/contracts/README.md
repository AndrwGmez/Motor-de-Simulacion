# Contratos de FlowVerse 3D

Este paquete contiene los formatos canónicos que comparten la API, el editor y
las pruebas:

- `schemas/flow-definition.schema.json`: documento editable/publicable.
- `schemas/simulation-request.schema.json`: entrada de una simulación.
- `schemas/run-event.schema.json`: sobre ordenado de eventos en tiempo real.
- `schemas/parse-proposal.schema.json`: resultado no persistido del parser.
- `openapi.yaml`: contrato HTTP.
- `asyncapi.yaml`: canal WebSocket.
- `fixtures/`: ejemplos válidos, inválidos y generadores de carga.

Ejecuta `pnpm --filter @flowverse/contracts check` después de modificar cualquier
contrato. La validación compila todos los JSON Schema, analiza ambos YAML,
comprueba referencias locales, evita `operationId` duplicados y verifica los
fixtures positivos y negativos.

Los cambios incompatibles requieren una nueva `schemaVersion`; no se debe
reinterpretar silenciosamente un documento ya publicado.

