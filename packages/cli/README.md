# @flowverse/cli

CLI publicable de FlowVerse para validar, comparar y simular `FlowDefinition`,
y para convertir esas mismas comprobaciones en un gate reproducible de pull
requests. El CLI invoca directamente `@flowverse/core` para el diff semántico
y `@flowverse/engine` para validación y simulación; no mantiene una segunda
implementación de esas reglas.

## Instalación

```bash
pnpm add --global @flowverse/cli
flowverse --help
```

Dentro de este monorepo:

```bash
pnpm --filter @flowverse/cli build
node packages/cli/dist/cli.js --help
```

Requiere Node.js 24.

## Comandos

```bash
# Validación semántica y métricas
flowverse validate flow.json

# Diff por identidades estables, no por el orden de los arrays
flowverse diff baseline.json candidate.json --format json

# Simulación local determinista
flowverse simulate flow.json \
  --input @fixtures/approved.json \
  --fail-node payment-provider \
  --force-edge decision-id=fallback-edge

# PR Flight Check
flowverse check candidate.json \
  --baseline baseline.json \
  --fail-on behavioral \
  --format sarif \
  --output artifacts/flowverse.sarif
```

`--input` acepta JSON inline o `@ruta/al/archivo.json`. `--fail-node` y
`--force-edge` pueden repetirse.

Todos los comandos ofrecen tres salidas:

- `human`, predeterminada y legible en terminal;
- `json`, estable para automatización;
- `sarif`, SARIF 2.1.0 compatible con code scanning.

Se puede usar `--output archivo` con cualquiera de ellas. Los atajos `--json`
y `--sarif` equivalen a `--format json` y `--format sarif`.

## Política y códigos de salida

`check` siempre bloquea errores de validación del candidato. Si se proporciona
`--baseline`:

- `--fail-on behavioral` bloquea cambios `behavioral` y `breaking`;
- `--fail-on breaking` bloquea sólo cambios `breaking`;
- `--fail-on none` o la ausencia de la opción reporta el diff sin bloquearlo.

Los códigos de salida son `0` para resultado aceptado, `1` para validación o
política fallida y `2` para uso, archivos o argumentos inválidos. `diff` no
bloquea por sí mismo: para aplicar una política se usa `check`.

## PR Flight Check para GitHub Actions

El repositorio incluye la acción compuesta
`.github/actions/flowverse-flight-check`. No recibe tokens ni secretos. Para
subir SARIF, el workflow llamador concede el permiso estándar de GitHub:

```yaml
permissions:
  contents: read
  security-events: write

steps:
  - uses: actions/checkout@v4
  - uses: ./.github/actions/flowverse-flight-check
    with:
      flow-file: flows/candidate.flow.json
      baseline-file: flows/baseline.flow.json
      fail-on: behavioral
```

La acción resuelve el CLI desde su propio `github.action_path`, por lo que
funciona tanto con la ruta local anterior como publicada desde otro
repositorio. Instala la versión fijada de pnpm, compila el CLI, conserva el
código de salida del gate y sube el SARIF antes de hacer fallar el job cuando
procede.
