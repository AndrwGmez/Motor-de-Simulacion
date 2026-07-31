# Especificación de experiencia 3D

## 1. Objetivo

La escena 3D debe ayudar a comprender y editar un proceso, no ser una animación
decorativa. Toda función esencial dispone también de controles DOM accesibles.

La implementación encapsula `react-force-graph-3d` y Three.js detrás de
`FlowSceneAdapter`. Store, paneles y simulación no importan directamente la
librería de renderizado; esto permite sustituir el motor sin reescribir el
producto.

## 2. Composición de pantalla

```text
┌───────────────────────────────────────────────────────────────────┐
│ Barra: proyecto / flujo / versión / guardar / publicar / usuario │
├──────────────┬───────────────────────────────────┬────────────────┤
│ Paleta,      │                                   │ Inspector de   │
│ búsqueda y   │          Universo 3D              │ nodo, edge o   │
│ texto/JSON   │                                   │ validación     │
├──────────────┴───────────────────────────────────┴────────────────┤
│ Run / pausa / step / velocidad / timeline / cámara / resultados  │
└───────────────────────────────────────────────────────────────────┘
```

- Paneles redimensionables, con mínimos que no oculten el canvas.
- Inspector cambia según selección de nodo, conexión, grupo o issue.
- Timeline inferior aparece al crear/seleccionar un run.
- El estado de guardado (`guardando`, `guardado`, `conflicto`, `offline`) es
  siempre visible.

## 3. Entrada y navegación

| Acción | Mouse/trackpad | Teclado |
|---|---|---|
| Seleccionar | Clic | Enter sobre elemento en árbol |
| Multiseleccionar | Shift/Ctrl + clic | Shift + navegación |
| Enfocar | Doble clic | `F` |
| Mover nodo | Arrastre | Inspector de coordenadas |
| Orbitar | Arrastre del fondo | Controles de cámara |
| Pan | Botón secundario/gesto | Flechas con modificador |
| Zoom | Rueda/pinch | `+`/`-` |
| Encuadrar todo | Botón | `0` |
| Crear conexión | Arrastre puerto a puerto | Formulario de conexión |
| Cancelar | Escape | Escape |
| Eliminar | Menú contextual | Delete/Backspace |
| Duplicar | Menú contextual | Ctrl/Cmd + D |
| Deshacer/rehacer | Barra | Ctrl/Cmd + Z / Shift + Z |

El arrastre empieza solo después de superar un umbral para no convertir un clic
en movimiento accidental. Soltar una conexión fuera de un puerto la cancela.

## 4. Selección

- Selección primaria: un elemento con inspector completo.
- Selección secundaria: conjunto para movimiento, bloqueo, agrupación o
  eliminación.
- Seleccionar desde búsqueda/issue enfoca y hace visible el elemento.
- Clic en fondo limpia la selección salvo durante multiselección.
- Una conexión tiene un volumen de hit-test mayor que su grosor visual.
- Elementos ocultos por `execution` siguen disponibles en el árbol.

El store conserva IDs, no objetos de Three.js.

## 5. Representación

### 5.1 Geometría

| Tipo | Geometría |
|---|---|
| `trigger` | Esfera con aro |
| `process` | Caja |
| `decision` | Octaedro |
| `data` | Cilindro |
| `integration` | Cilindro de seis lados |
| `delay` | Toro |
| `end` | Esfera sólida |
| `group` | Bounding box transparente |

Geometrías y materiales se reutilizan. No se crea un material nuevo por nodo.

### 5.2 Conexiones

- Curva direccional con punta de flecha.
- Etiqueta cerca del punto medio cuando el nivel de detalle lo permite.
- Grosor base uniforme; análisis histórico puede escalarlo de forma acotada.
- Partícula solo durante `edge.traversed` o replay.
- Conexión seleccionada/issue se destaca sin ocultar flecha.
- Las líneas paralelas reciben curvaturas distintas.
- Self-loop tiene curva explícita y se puede seleccionar.

### 5.3 Etiquetas y nivel de detalle

- Nodo seleccionado: etiqueta completa siempre.
- Cerca: label completo y estado.
- Media distancia: label truncado.
- Lejos: icono/forma sin texto.
- El HTML de etiquetas se construye con nodos de texto, nunca `innerHTML`.
- El inspector contiene el texto completo.

## 6. Estado visual de simulación

La escena recibe eventos normalizados a través de un reducer idempotente. Nunca
abre WebSocket por nodo.

Precedencia visual agregada:

```text
failed > running > waiting > queued > success > skipped > idle
```

| Estado | Color | Señal adicional |
|---|---|---|
| idle | Tipo/categoría | Ninguna |
| queued | Amarillo | Contorno |
| waiting | Naranja | Icono de espera |
| running | Azul | Halo/pulso |
| success | Verde | Check |
| failed | Rojo | Cruz y panel de error |
| skipped | Gris | Opacidad reducida |

El reducer retiene visitas individuales para ciclos. Reiniciar la visualización
no modifica el run persistido.

## 7. Cámara

- Perspectiva con near/far adaptados al bounding box.
- `focusNode` anima posición y target durante 300–500 ms.
- `fitAll` incluye margen del 15%.
- Seguimiento de run es opt-in y se puede interrumpir moviendo la cámara.
- El seguimiento enfoca `node.started`, no cada evento.
- Cambiar selección manual desactiva seguimiento.
- La posición guardada pertenece a la versión; cambios temporales no disparan
  autosave hasta que la interacción termina.

Con `prefers-reduced-motion`, los saltos de cámara son inmediatos y no hay
seguimiento automático predeterminado.

## 8. Edición y autosave

El store separa:

- Documento persistible.
- Historial de comandos.
- Selección y cámara.
- Estado del servidor/ETag.
- Estado/replay de ejecución.

Una operación de usuario produce un comando reversible. Mover varios nodos es un
solo comando desde posiciones iniciales a finales. El autosave:

1. Marca documento sucio.
2. Espera 750 ms desde la última mutación.
3. Envía documento y `If-Match`.
4. Actualiza ETag al confirmar.
5. Reintenta errores transitorios con backoff.
6. Detiene reintentos ante `412` y presenta resolución.

No se publica automáticamente.

## 9. Layouts

### `force`

- Fuerza de carga, enlaces y colisión.
- Nodos bloqueados fijan `fx`, `fy`, `fz`.
- Se detiene al alcanzar umbral de energía o timeout.
- Reanuda al cambiar topología.

### `directional`

- Condensa SCC.
- Asigna rango desde triggers.
- Ordena nodos dentro del rango para reducir cruces.
- Eje X indica avance; Y/Z separan ramas.

### `layers`

- Usa el mismo grafo condensado.
- Profundidad topológica en Z.
- Categoría/grupo distribuye X/Y dentro de la capa.

### `timeline`

- Inicio temprano calculado con dependencias y duración lógica.
- X representa tiempo.
- Ramas paralelas ocupan carriles.
- Ciclos se representan como bloque iterativo, no como tiempo infinito.

### `clusters`

- Agrupa por `category`, `type` o `group`.
- Centroide determinista por clave ordenada.
- Fuerzas locales evitan superposición.

### `execution`

- Conserva coordenadas del layout anterior.
- Atenúa nodos/conexiones fuera de la ruta del run.
- Puede filtrar por token/rama.
- No elimina elementos del documento.

Layouts deterministas se calculan en Web Worker y devuelven solo posiciones.
Una respuesta obsoleta se descarta mediante revision ID.

## 10. Grupos

- Crear grupo calcula un bounding box con padding.
- Mover grupo desplaza miembros no bloqueados como una sola operación.
- Nodo bloqueado no se mueve y muestra aviso.
- Colapsar sustituye miembros visualmente por el contenedor; la topología no
  cambia y la simulación puede expandirlo temporalmente.
- No se permiten grupos recursivos en el MVP.

## 11. Accesibilidad

- Vista de árbol/lista sincronizada con la escena.
- Formularios para crear y conectar sin drag-and-drop.
- Orden de tabulación estable y foco visible.
- `aria-live` moderado para guardado y run; no anuncia cada partícula.
- Resumen de ejecución disponible como texto.
- Contraste WCAG AA en paneles.
- Color acompañado de forma/icono/texto.
- Objetivos táctiles DOM de mínimo 44 px.
- Atajos documentados y desactivables.

## 12. Presupuesto de rendimiento

Con el fixture 500/1.000 en un computador de referencia:

- Primer frame útil menor de 2 s después de cargar datos locales.
- Navegación ≥30 FPS tras estabilización.
- Evento aplicado sin reconstruir arrays completos del grafo.
- Máximo visual predeterminado de 100 partículas simultáneas.
- Etiquetas lejanas no montan DOM.
- Física se pausa cuando no es necesaria.

Medición:

- Playwright con navegador Chromium y WebGL real cuando el runner lo permita.
- `performance.mark` para carga, layout, fit y ráfaga de eventos.
- Prueba unitaria del adaptador con escena falsa para evitar depender solo de
  snapshots visuales.

## 13. Errores y recuperación

- WebGL no disponible: mostrar vista lista y explicación accionable.
- Contexto WebGL perdido: intentar recrear una vez y conservar el documento.
- Layout Worker falla: mantener posiciones anteriores.
- WebSocket cae: congelar estado, obtener nuevo ticket y reconectar con
  `afterSequence`.
- Autosave falla: conservar copia local y permitir descargar JSON.
- ETag conflictivo: comparar fecha/ETag, recargar o exportar; nunca sobrescribir
  sin confirmación.

