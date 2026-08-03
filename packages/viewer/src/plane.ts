import type { NodeType } from "@flowverse/core";

/**
 * Proyección al plano para la vista bidimensional.
 *
 * Las posiciones ya las calcula `applyLayout` para los modos direccional,
 * capas, cronología y clústeres: aquí solo se proyectan al plano ignorando la
 * profundidad y se encuadran en el lienzo. No hay algoritmo de disposición
 * nuevo, y por eso las dos vistas muestran siempre el mismo grafo.
 */

export interface Punto {
  x: number;
  y: number;
}

export interface NodoPlano {
  id: string;
  position: { x: number; y: number; z: number };
}

export interface Encuadre {
  posiciones: Map<string, Punto>;
  viewBox: string;
  escala: number;
}

export function encuadrar(nodos: readonly NodoPlano[], lienzo: { width: number; height: number }, margen = 48): Encuadre {
  const viewBox = `0 0 ${lienzo.width} ${lienzo.height}`;
  const posiciones = new Map<string, Punto>();
  if (nodos.length === 0) return { posiciones, viewBox, escala: 1 };

  const xs = nodos.map((n) => n.position.x);
  const ys = nodos.map((n) => n.position.y);
  const minX = Math.min(...xs);
  const maxX = Math.max(...xs);
  const minY = Math.min(...ys);
  const maxY = Math.max(...ys);
  const ancho = maxX - minX;
  const alto = maxY - minY;

  const utilAncho = lienzo.width - margen * 2;
  const utilAlto = lienzo.height - margen * 2;
  // Una sola escala para los dos ejes: deformar el grafo lo haría ilegible.
  const escala = ancho === 0 && alto === 0
    ? 1
    : Math.min(ancho === 0 ? Infinity : utilAncho / ancho, alto === 0 ? Infinity : utilAlto / alto);

  const centradoX = (lienzo.width - ancho * escala) / 2;
  const centradoY = (lienzo.height - alto * escala) / 2;
  for (const nodo of nodos) {
    posiciones.set(nodo.id, {
      x: centradoX + (nodo.position.x - minX) * escala,
      y: centradoY + (nodo.position.y - minY) * escala,
    });
  }
  return { posiciones, viewBox, escala };
}

export type FormaTipo = "circulo" | "rectangulo" | "rombo" | "cilindro" | "hexagono" | "anillo" | "contenedor";

export interface Forma {
  tipo: FormaTipo;
  radio: number;
}

/** Misma identidad visual que en 3D: la forma dice el tipo de nodo. */
export function formaDeNodo(tipo: NodeType): Forma {
  switch (tipo) {
    case "trigger": return { tipo: "circulo", radio: 11 };
    case "end": return { tipo: "anillo", radio: 12 };
    case "decision": return { tipo: "rombo", radio: 13 };
    case "data": return { tipo: "cilindro", radio: 12 };
    case "integration": return { tipo: "hexagono", radio: 12 };
    case "delay": return { tipo: "anillo", radio: 10 };
    case "group": return { tipo: "contenedor", radio: 26 };
    default: return { tipo: "rectangulo", radio: 11 };
  }
}

/**
 * Curva suave entre dos nodos. `desvio` separa las aristas paralelas: sin él,
 * dos conexiones entre los mismos nodos se dibujarían una encima de otra y
 * parecerían una sola.
 */
export function rutaDeArista(desde: Punto, hasta: Punto, desvio = 0): string {
  const medioX = (desde.x + hasta.x) / 2;
  const medioY = (desde.y + hasta.y) / 2;
  if (desvio === 0) return `M ${desde.x} ${desde.y} L ${hasta.x} ${hasta.y}`;

  const dx = hasta.x - desde.x;
  const dy = hasta.y - desde.y;
  const largo = Math.hypot(dx, dy) || 1;
  const separacion = desvio * 18;
  const controlX = medioX + (-dy / largo) * separacion;
  const controlY = medioY + (dx / largo) * separacion;
  return `M ${desde.x} ${desde.y} Q ${controlX} ${controlY} ${hasta.x} ${hasta.y}`;
}
