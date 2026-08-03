import type { FlowDefinition, FlowEdge, FlowNode, NodeType } from "@flowverse/core";
import type { ClasePaso, Recorrido } from "../model";
import { identificador } from "../identifier";

/**
 * Recorrido de negocio → FlowDefinition.
 *
 * Cada clase de paso se traduce al tipo de nodo que le corresponde en el
 * contrato, y las decisiones reciben su camino por defecto obligatorio: sin él
 * el validador rechaza el flujo con `decision.default_required`.
 */

const TIPOS: Record<ClasePaso, NodeType> = {
  entrada: "trigger",
  proceso: "process",
  decision: "decision",
  integracion: "integration",
  datos: "data",
  bucle: "delay",
  fin_ok: "end",
  fin_error: "end",
};

const COLORES: Partial<Record<NodeType, string>> = {
  trigger: "#7F8CFF", process: "#4D8DFF", decision: "#B078FF",
  integration: "#F49A59", data: "#36C6D8", delay: "#F2C15D", end: "#35D39D",
};

function configuracion(clase: ClasePaso, id: string): Record<string, unknown> {
  switch (clase) {
    case "entrada": return { eventName: "codegraph.journey" };
    case "fin_ok": return { result: "success" };
    case "fin_error": return { result: "failure" };
    case "integracion": return { latencyMs: 0, outcome: "success" };
    case "datos": return { operations: [{ op: "set", path: `/recorrido/${id}`, value: true }] };
    case "bucle": return { delayMs: 0 };
    // El contrato exige la estrategia en toda decisión.
    case "decision": return { strategy: "first_match" };
    default: return {};
  }
}

export function extraerRecorridoDeNegocio(recorrido: Recorrido, nombre: string): FlowDefinition {
  const usados = new Set<string>();
  const idPorPaso = new Map<string, string>();
  for (const paso of recorrido.pasos) {
    const id = identificador(paso.id, usados);
    usados.add(id);
    idPorPaso.set(paso.id, id);
  }

  // Profundidad para colocar en capas de izquierda a derecha.
  const profundidad = new Map<string, number>([[recorrido.entrada, 0]]);
  const cola = [recorrido.entrada];
  while (cola.length > 0) {
    const actual = cola.shift()!;
    const paso = recorrido.pasos.find((p) => p.id === actual);
    for (const { destino } of paso?.siguientes ?? []) {
      if (!profundidad.has(destino)) {
        profundidad.set(destino, (profundidad.get(actual) ?? 0) + 1);
        cola.push(destino);
      }
    }
  }
  const porCapa = new Map<number, number>();

  const nodes: FlowNode[] = recorrido.pasos.map((paso) => {
    const tipo = TIPOS[paso.clase];
    const capa = profundidad.get(paso.id) ?? 0;
    const fila = porCapa.get(capa) ?? 0;
    porCapa.set(capa, fila + 1);
    const salidas = paso.siguientes.length;
    return {
      id: idPorPaso.get(paso.id)!,
      type: tipo,
      label: paso.etiqueta.slice(0, 120) || paso.clase,
      description: "",
      inputs: tipo === "trigger" ? [] : [{ id: "entrada", label: "Entrada" }],
      outputs: tipo === "end"
        ? []
        : tipo === "decision"
          ? Array.from({ length: Math.max(1, salidas) }, (_, i) => ({ id: `rama${i + 1}`, label: `Rama ${i + 1}` }))
          : [{ id: "salida", label: "Salida" }],
      activationMode: "each",
      durationMs: paso.clase === "proceso" ? 50 : 0,
      configuration: configuracion(paso.clase, idPorPaso.get(paso.id)!),
      position: { x: capa * 200 - 300, y: fila * 130 - 130, z: 0 },
      locked: false,
      metadata: { category: paso.clase, ...(COLORES[tipo] ? { color: COLORES[tipo] } : {}) },
    } as FlowNode;
  });

  const edges: FlowEdge[] = [];
  for (const paso of recorrido.pasos) {
    const origen = idPorPaso.get(paso.id)!;
    const esDecision = paso.clase === "decision";
    paso.siguientes.forEach((siguiente, indice) => {
      const ultima = indice === paso.siguientes.length - 1;
      edges.push({
        id: identificador(`c_${paso.id}_${siguiente.destino}_${indice}`, usados),
        source: origen,
        target: idPorPaso.get(siguiente.destino)!,
        sourcePort: esDecision ? `rama${indice + 1}` : "salida",
        targetPort: "entrada",
        ...(siguiente.etiqueta ? { label: siguiente.etiqueta.slice(0, 120) } : {}),
        priority: indice,
        // El validador exige exactamente un camino por defecto por decisión.
        isDefault: esDecision ? ultima : false,
      } as FlowEdge);
    });
  }

  return {
    schemaVersion: "1.0",
    name: `Recorrido de ${nombre}`.slice(0, 120),
    description: `Recorrido de negocio derivado del código: ${nodes.length} pasos.`,
    metadata: { tags: ["codegraph", "journey"], createdWith: "@flowverse/codegraph" },
    variables: [],
    layout: { mode: "directional" },
    nodes,
    edges,
  } as FlowDefinition;
}
