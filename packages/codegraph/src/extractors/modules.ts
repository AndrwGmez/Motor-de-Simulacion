import type { FlowDefinition, FlowEdge, FlowNode, NodeType } from "@flowverse/core";
import type { ModeloCodigo } from "../model";
import { identificador } from "../identifier";

/**
 * Extractor de módulos y dependencias.
 *
 * Cada archivo es un nodo, cada importación interna una conexión y cada
 * directorio un contenedor. Da una vista de arquitectura: responde "qué hay y
 * cómo se apoya en qué", no "cómo se ejecuta".
 *
 * Un grafo de módulos no tiene principio ni final por sí mismo, y el validador
 * exige ambos. Se añaden dos nodos sintéticos: la entrada conecta con los
 * módulos que nadie importa —los puntos de entrada reales del proyecto— y los
 * módulos que no importan nada interno desembocan en el final. Así el resultado
 * es un flujo válido y no un montón de nodos sueltos.
 */

const COLORES: Partial<Record<NodeType, string>> = {
  process: "#4D8DFF",
  data: "#36C6D8",
  integration: "#F49A59",
  group: "#8993AD",
  trigger: "#7F8CFF",
  end: "#35D39D",
};

const ENTRADA = [{ id: "entrada", label: "Entrada" }];
const SALIDA = [{ id: "salida", label: "Salida" }];

function configuracionPara(tipo: NodeType, ruta: string): Record<string, unknown> {
  switch (tipo) {
    case "trigger":
      return { eventName: "codegraph.analysis" };
    case "end":
      return { result: "success" };
    case "integration":
      return { latencyMs: 0, outcome: "success" };
    // El contrato exige al menos una operación en los nodos de datos.
    case "data":
      return { operations: [{ op: "set", path: `/modulos/${ruta.replace(/[^a-z0-9]/gi, "_")}`, value: true }] };
    case "group":
      return { collapsed: false };
    default:
      return {};
  }
}

function nodo(
  id: string,
  tipo: NodeType,
  label: string,
  posicion: { x: number; y: number; z: number },
  extra: { descripcion?: string; categoria?: string; grupo?: string; ruta?: string } = {},
): FlowNode {
  return {
    id,
    type: tipo,
    label: label.slice(0, 120),
    description: extra.descripcion ?? "",
    inputs: tipo === "trigger" || tipo === "group" ? [] : ENTRADA,
    outputs: tipo === "end" || tipo === "group" ? [] : SALIDA,
    activationMode: "each",
    durationMs: 0,
    configuration: configuracionPara(tipo, extra.ruta ?? id),
    position: posicion,
    locked: false,
    metadata: {
      ...(extra.categoria ? { category: extra.categoria.slice(0, 80) } : {}),
      ...(COLORES[tipo] ? { color: COLORES[tipo] } : {}),
      ...(extra.grupo ? { groupId: extra.grupo } : {}),
    },
  } as FlowNode;
}

export function extraerModulos(modelo: ModeloCodigo, nombre: string): FlowDefinition {
  const usados = new Set<string>();
  const idPorRuta = new Map<string, string>();
  for (const modulo of modelo.modulos) {
    const id = identificador(modulo.ruta, usados);
    usados.add(id);
    idPorRuta.set(modulo.ruta, id);
  }

  // Profundidad desde los puntos de entrada, para colocar por capas.
  const importadoPor = new Map<string, string[]>();
  const importa = new Map<string, string[]>();
  for (const referencia of modelo.referencias) {
    importa.set(referencia.desde, [...(importa.get(referencia.desde) ?? []), referencia.hacia]);
    importadoPor.set(referencia.hacia, [...(importadoPor.get(referencia.hacia) ?? []), referencia.desde]);
  }

  const raices = modelo.modulos.filter((m) => !importadoPor.has(m.ruta)).map((m) => m.ruta);
  const profundidad = new Map<string, number>();
  const cola = [...raices];
  for (const ruta of raices) profundidad.set(ruta, 1);
  while (cola.length > 0) {
    const actual = cola.shift()!;
    for (const siguiente of importa.get(actual) ?? []) {
      const nueva = (profundidad.get(actual) ?? 1) + 1;
      if (!profundidad.has(siguiente) || nueva > profundidad.get(siguiente)!) {
        profundidad.set(siguiente, nueva);
        cola.push(siguiente);
      }
    }
  }

  const directorios = [...new Set(modelo.modulos.map((m) => m.directorio).filter(Boolean))].sort();
  const idPorDirectorio = new Map<string, string>();
  for (const directorio of directorios) {
    const id = identificador(`grupo_${directorio}`, usados);
    usados.add(id);
    idPorDirectorio.set(directorio, id);
  }

  const nodes: FlowNode[] = [];
  const porFila = new Map<number, number>();

  nodes.push(nodo("entrada_del_analisis", "trigger", "Puntos de entrada", { x: -420, y: 0, z: 0 }, {
    descripcion: "Módulos que ningún otro importa.",
    categoria: "analisis",
  }));

  directorios.forEach((directorio, indice) => {
    nodes.push(nodo(idPorDirectorio.get(directorio)!, "group", directorio, { x: 0, y: indice * 180 - 300, z: -240 }, {
      descripcion: `Directorio ${directorio}`,
      categoria: directorio,
    }));
  });

  for (const modulo of modelo.modulos) {
    const capa = profundidad.get(modulo.ruta) ?? 1;
    const fila = porFila.get(capa) ?? 0;
    porFila.set(capa, fila + 1);
    nodes.push(nodo(idPorRuta.get(modulo.ruta)!, modulo.papel, modulo.nombre, {
      x: capa * 240 - 240,
      y: fila * 120 - 180,
      z: 0,
    }, {
      descripcion: modulo.motivo ? `${modulo.ruta} · usa ${modulo.motivo}` : modulo.ruta,
      categoria: "archivo",
      grupo: modulo.directorio ? idPorDirectorio.get(modulo.directorio) : undefined,
      ruta: modulo.ruta,
    }));
  }

  const hojas = modelo.modulos.filter((m) => !(importa.get(m.ruta) ?? []).length).map((m) => m.ruta);
  nodes.push(nodo("fin_del_analisis", "end", "Sin más dependencias", {
    x: (Math.max(1, ...profundidad.values()) + 1) * 240 - 240, y: 0, z: 0,
  }, { descripcion: "Módulos que no importan nada interno.", categoria: "analisis" }));

  const edges: FlowEdge[] = [];
  const arista = (desde: string, hacia: string, etiqueta?: string): void => {
    edges.push({
      id: identificador(`c_${desde}_${hacia}`, usados),
      source: desde,
      target: hacia,
      sourcePort: "salida",
      targetPort: "entrada",
      ...(etiqueta ? { label: etiqueta.slice(0, 120) } : {}),
      priority: 0,
      isDefault: false,
    } as FlowEdge);
    usados.add(edges[edges.length - 1].id);
  };

  for (const ruta of raices) arista("entrada_del_analisis", idPorRuta.get(ruta)!, "punto de entrada");
  for (const referencia of modelo.referencias) {
    arista(idPorRuta.get(referencia.desde)!, idPorRuta.get(referencia.hacia)!, "importa");
  }
  for (const ruta of hojas) arista(idPorRuta.get(ruta)!, "fin_del_analisis");

  return {
    schemaVersion: "1.0",
    name: `Módulos de ${nombre}`.slice(0, 120),
    description: `Vista de arquitectura derivada del código: ${modelo.modulos.length} módulos y ${modelo.referencias.length} importaciones internas.`,
    metadata: { tags: ["codegraph", "modules"], createdWith: "@flowverse/codegraph" },
    variables: [],
    layout: { mode: "layers" },
    nodes,
    edges,
  } as FlowDefinition;
}
