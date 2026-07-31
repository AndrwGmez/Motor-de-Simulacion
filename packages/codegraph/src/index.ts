import { basename, resolve } from "node:path";
import type { FlowDefinition } from "@flowverse/core";
import { analizarTypeScript } from "./adapters/typescript";
import { extraerModulos } from "./extractors/modules";

export type Modo = "modules" | "functions" | "journey";

export interface OpcionesAnalisis {
  modo: Modo;
  /** Topes del contrato. Se comprueban antes de emitir, no después. */
  limiteNodos?: number;
  limiteConexiones?: number;
}

export const LIMITE_NODOS = 5000;
export const LIMITE_CONEXIONES = 10000;

export async function analizar(raiz: string, opciones: OpcionesAnalisis): Promise<FlowDefinition> {
  const absoluta = resolve(raiz);
  const modelo = analizarTypeScript(absoluta);

  let flujo: FlowDefinition;
  switch (opciones.modo) {
    case "modules":
      flujo = extraerModulos(modelo, basename(absoluta));
      break;
    default:
      throw new Error(`El modo "${opciones.modo}" todavía no está implementado.`);
  }

  // Fallar aquí con un mensaje útil es mejor que emitir un archivo que la API
  // va a rechazar más tarde con un error genérico.
  const topeNodos = opciones.limiteNodos ?? LIMITE_NODOS;
  const topeConexiones = opciones.limiteConexiones ?? LIMITE_CONEXIONES;
  if (flujo.nodes.length > topeNodos) {
    throw new Error(
      `El análisis produjo ${flujo.nodes.length} nodos y supera el límite de ${topeNodos}. ` +
      "Acota el alcance con --incluir, --excluir o --desde.",
    );
  }
  if (flujo.edges.length > topeConexiones) {
    throw new Error(
      `El análisis produjo ${flujo.edges.length} conexiones y supera el límite de ${topeConexiones}. ` +
      "Acota el alcance con --incluir, --excluir o --desde.",
    );
  }
  return flujo;
}

export { analizarTypeScript } from "./adapters/typescript";
export { extraerModulos } from "./extractors/modules";
export type { ModeloCodigo, Modulo, Referencia } from "./model";
