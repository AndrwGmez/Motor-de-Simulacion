# Flujos de ejemplo

Dos archivos `FlowDefinition 1.0` listos para importar desde **⇧ Importar JSON**
en el editor, o mediante `POST /v1/flows/import`.

| Archivo | Tamaño | Para qué sirve |
|---|---|---|
| `pedido-ejemplo.flow.json` | 16 nodos · 16 conexiones | Referencia legible del contrato completo |
| `plataforma-500.flow.json` | 482 nodos · 1.000 conexiones | Medir el editor 3D, el validador, el analizador y el simulador cerca de los límites |

## Ejemplo legible

`pedido-ejemplo.flow.json` usa los ocho tipos de nodo, condiciones
estructuradas, un contenedor `group`, variables con valores por defecto y un
ciclo de reintento con salida. Ambas ejecuciones terminan sin configurar nada
porque cada variable que un nodo de datos necesita declara su `default`.

## Flujo grande

`plataforma-500.flow.json` lo produce un generador determinista:

```bash
node samples/generar-plataforma.mjs > samples/plataforma-500.flow.json
```

Modela cuatro dominios y veintiocho áreas. Cada área tiene una decisión de
tres caminos —automático, manual y excepción—, un ciclo de reintento y un cierre
con activación `all` que espera a dos ramas. Las conexiones de escalado cruzado
salen de decisiones con condiciones improbables: densifican el grafo hasta las
1.000 conexiones sin multiplicar los caminos que recorre una ejecución normal.

Para cambiar su tamaño, edita `DOMAINS` en el generador. El contrato admite
como máximo 5.000 nodos y 10.000 conexiones, y la API rechaza cuerpos mayores
de 24 MiB.

Para la escala máxima:

```bash
node samples/generar-plataforma.mjs --escala grande > /tmp/plataforma-xxl.json
```

Eso produce 4.782 nodos y 10.000 conexiones (6,8 MB). No se versiona por tamaño.

## Comprobar un archivo antes de guardarlo

```bash
curl -s -X POST http://localhost:8080/v1/flows/import \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -b cookies.txt \
  --data-binary @samples/pedido-ejemplo.flow.json
```

La respuesta trae `definition` normalizada y un `report` con la validación. La
importación nunca guarda nada: el editor la muestra como previsualización y
sustituye el borrador solo cuando lo confirmas.

Ambos archivos validan con un único aviso `cycle.with_exit`, que es informativo:
avisa de que el ciclo de reintento existe y que el motor lo acotará en
tiempo de ejecución.
