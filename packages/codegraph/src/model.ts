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
