import type { FlowDefinition, FlowEdge, FlowNode } from "@flowverse/core";
import type { Funcion, Llamada } from "../adapters/typescript-functions";
import { identificador } from "../identifier";

/**
 * Funciones y sus llamadas. Sin filtrar, en un proyecto real produce miles de
 * nodos; por eso el análisis acepta `incluir` y `excluir`, y por eso el tope del
 * contrato se comprueba antes de emitir nada.
 */
export function extraerFunciones(
  funciones: Funcion[],
  llamadas: Llamada[],
  nombre: string,
): FlowDefinition {
  const usados = new Set<string>();
  const idPorNombre = new Map<string, string>();
  for (const funcion of funciones) {
    if (idPorNombre.has(funcion.nombre)) continue;
    const id = identificador(funcion.nombre, usados);
    usados.add(id);
    idPorNombre.set(funcion.nombre, id);
  }

  const llamadaPor = new Set(llamadas.map((l) => l.hacia));
  const raices = funciones.filter((f) => !llamadaPor.has(f.nombre));

  const nodo = (id: string, tipo: FlowNode["type"], label: string, x: number, y: number, config: Record<string, unknown>): FlowNode => ({
    id, type: tipo, label: label.slice(0, 120), description: "",
    inputs: tipo === "trigger" ? [] : [{ id: "entrada", label: "Entrada" }],
    outputs: tipo === "end" ? [] : [{ id: "salida", label: "Salida" }],
    activationMode: "each", durationMs: 0, configuration: config,
    position: { x, y, z: 0 }, locked: false,
    metadata: { category: "funcion" },
  } as FlowNode);

  const nodes: FlowNode[] = [nodo("entrada_funciones", "trigger", "Funciones raíz", -260, 0, { eventName: "codegraph.functions" })];
  funciones.forEach((funcion, indice) => {
    const id = idPorNombre.get(funcion.nombre)!;
    if (nodes.some((n) => n.id === id)) return;
    nodes.push(nodo(id, funcion.papel, funcion.nombre, (indice % 6) * 190, Math.floor(indice / 6) * 130 - 130,
      funcion.papel === "data"
        ? { operations: [{ op: "set", path: `/funciones/${id}`, value: true }] }
        : funcion.papel === "integration" ? { latencyMs: 0, outcome: "success" } : {}));
  });
  nodes.push(nodo("fin_funciones", "end", "Sin más llamadas", 1200, 0, { result: "success" }));

  const edges: FlowEdge[] = [];
  const arista = (desde: string, hacia: string) => {
    edges.push({
      id: identificador(`c_${desde}_${hacia}`, usados),
      source: desde, target: hacia, sourcePort: "salida", targetPort: "entrada",
      priority: 0, isDefault: false,
    } as FlowEdge);
  };
  for (const raiz of raices) arista("entrada_funciones", idPorNombre.get(raiz.nombre)!);
  for (const llamada of llamadas) arista(idPorNombre.get(llamada.desde)!, idPorNombre.get(llamada.hacia)!);
  const llamadoras = new Set(llamadas.map((l) => l.desde));
  for (const hoja of funciones.filter((f) => !llamadoras.has(f.nombre))) {
    arista(idPorNombre.get(hoja.nombre)!, "fin_funciones");
  }

  return {
    schemaVersion: "1.0",
    name: `Funciones de ${nombre}`.slice(0, 120),
    description: `${funciones.length} funciones y ${llamadas.length} llamadas internas.`,
    metadata: { tags: ["codegraph", "functions"], createdWith: "@flowverse/codegraph" },
    variables: [], layout: { mode: "force" }, nodes, edges,
  } as FlowDefinition;
}
