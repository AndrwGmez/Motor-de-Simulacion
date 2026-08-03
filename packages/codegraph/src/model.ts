/**
 * Modelo intermedio, independiente del lenguaje.
 *
 * Cada adaptador (TypeScript, Go, …) produce esta forma, y los extractores
 * trabajan solo sobre ella. Añadir un lenguaje nuevo es escribir un adaptador,
 * no reescribir los extractores.
 */

export type Papel = "process" | "data" | "integration";

export interface Modulo {
  /** Ruta relativa a la raíz analizada. */
  ruta: string;
  directorio: string;
  nombre: string;
  papel: Papel;
  /** Paquetes externos que justifican el papel asignado. */
  motivo?: string;
}

export interface Referencia {
  desde: string;
  hacia: string;
  clase: "import";
}

export interface ModeloCodigo {
  raiz: string;
  modulos: Modulo[];
  referencias: Referencia[];
}


/** Paso del recorrido de negocio, extraído del flujo de control. */
export type ClasePaso = "entrada" | "proceso" | "decision" | "integracion" | "datos" | "bucle" | "fin_ok" | "fin_error";

export interface Paso {
  id: string;
  clase: ClasePaso;
  etiqueta: string;
  /** Pasos que siguen a este; el último es el camino por defecto. */
  siguientes: { destino: string; etiqueta?: string }[];
}

export interface Recorrido {
  entrada: string;
  pasos: Paso[];
}
