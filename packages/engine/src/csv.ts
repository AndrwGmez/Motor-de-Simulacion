import type { FlowDefinition, FlowEdge, FlowNode, NodeType } from "@flowverse/core";

/**
 * Importación de flujos desde CSV (§6.3).
 *
 * Formato: una fila por nodo, con las columnas `id`, `tipo`, `etiqueta` y
 * `conecta_con` —esta última con los destinos separados por punto y coma—.
 * Es el formato más simple que permite describir un grafo dirigido completo sin
 * pedirle al usuario dos archivos ni un orden concreto de filas.
 *
 * Se prefiere fallar con un mensaje concreto antes que adivinar: un CSV mal
 * formado produciría un flujo que la API rechazaría después con un error
 * genérico, y el usuario no sabría qué corregir.
 */

const TIPOS: NodeType[] = ["trigger", "process", "decision", "data", "integration", "delay", "end", "group"];
const OBLIGATORIAS = ["id", "tipo", "etiqueta"];

/** Divide respetando las comillas: una etiqueta puede contener comas. */
function celdas(linea: string): string[] {
  const salida: string[] = [];
  let actual = "";
  let entreComillas = false;
  for (let i = 0; i < linea.length; i += 1) {
    const caracter = linea[i];
    if (caracter === '"') {
      if (entreComillas && linea[i + 1] === '"') { actual += '"'; i += 1; }
      else entreComillas = !entreComillas;
      continue;
    }
    if (caracter === "," && !entreComillas) { salida.push(actual.trim()); actual = ""; continue; }
    actual += caracter;
  }
  salida.push(actual.trim());
  return salida;
}

function configuracion(tipo: NodeType, id: string): Record<string, unknown> {
  switch (tipo) {
    case "trigger": return { eventName: "csv.import" };
    case "end": return { result: "success" };
    case "decision": return { strategy: "first_match" };
    case "integration": return { latencyMs: 0, outcome: "success" };
    case "data": return { operations: [{ op: "set", path: `/csv/${id}`, value: true }] };
    case "group": return { collapsed: false };
    default: return {};
  }
}

export function parseFlowCsv(contenido: string, nombre = "Flujo importado"): FlowDefinition {
  const lineas = contenido.split(/\r?\n/).filter((linea) => linea.trim().length > 0);
  if (lineas.length < 2) throw new Error("El CSV necesita una cabecera y al menos una fila.");

  const cabecera = celdas(lineas[0]).map((columna) => columna.toLowerCase());
  for (const columna of OBLIGATORIAS) {
    if (!cabecera.includes(columna)) {
      throw new Error(`Falta la columna obligatoria "${columna}". Se esperaban: ${OBLIGATORIAS.join(", ")} y opcionalmente conecta_con.`);
    }
  }
  const indice = (columna: string) => cabecera.indexOf(columna);

  const filas = lineas.slice(1).map(celdas);
  const nodes: FlowNode[] = [];
  const destinos: { desde: string; hacia: string }[] = [];

  filas.forEach((fila, posicion) => {
    const id = fila[indice("id")];
    const tipo = fila[indice("tipo")] as NodeType;
    if (!id) throw new Error(`La fila ${posicion + 2} no tiene identificador.`);
    if (!TIPOS.includes(tipo)) {
      throw new Error(`Tipo de nodo desconocido "${tipo}" en la fila ${posicion + 2}. Válidos: ${TIPOS.join(", ")}.`);
    }
    const conecta = indice("conecta_con") >= 0 ? (fila[indice("conecta_con")] ?? "") : "";
    for (const destino of conecta.split(";").map((d) => d.trim()).filter(Boolean)) {
      destinos.push({ desde: id, hacia: destino });
    }

    nodes.push({
      id, type: tipo, label: fila[indice("etiqueta")] || id, description: "",
      inputs: tipo === "trigger" || tipo === "group" ? [] : [{ id: "entrada", label: "Entrada" }],
      outputs: tipo === "end" || tipo === "group" ? [] : [{ id: "salida", label: "Salida" }],
      activationMode: "each", durationMs: 0, configuration: configuracion(tipo, id),
      position: { x: posicion * 180 - 200, y: (posicion % 3) * 120 - 120, z: 0 },
      locked: false, metadata: { category: "csv" },
    } as FlowNode);
  });

  const conocidos = new Set(nodes.map((n) => n.id));
  const salidasPorNodo = new Map<string, number>();
  const edges: FlowEdge[] = destinos.map(({ desde, hacia }, posicion) => {
    if (!conocidos.has(hacia)) {
      throw new Error(`La conexión de "${desde}" apunta a "${hacia}", que no existe en el CSV.`);
    }
    const orden = salidasPorNodo.get(desde) ?? 0;
    salidasPorNodo.set(desde, orden + 1);
    return {
      id: `c${posicion + 1}`, source: desde, target: hacia,
      sourcePort: "salida", targetPort: "entrada",
      priority: orden, isDefault: false,
    } as FlowEdge;
  });

  // Cada decisión necesita exactamente un camino por defecto: se marca el
  // último, que es la lectura natural de "si no encaja ninguno, por aquí".
  for (const decision of nodes.filter((n) => n.type === "decision")) {
    const suyas = edges.filter((e) => e.source === decision.id);
    if (suyas.length > 0) suyas[suyas.length - 1].isDefault = true;
  }

  return {
    schemaVersion: "1.0", name: nombre.slice(0, 120),
    description: `Importado desde CSV: ${nodes.length} nodos y ${edges.length} conexiones.`,
    metadata: { tags: ["csv"], createdWith: "@flowverse/engine" },
    variables: [], layout: { mode: "directional" }, nodes, edges,
  } as FlowDefinition;
}
