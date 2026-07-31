const DEFAULT_NODE_COUNT = 500;
const DEFAULT_EDGE_COUNT = 1000;

function positionFor(index) {
  const layer = Math.floor(index / 25);
  const slot = index % 25;
  return {
    x: layer * 100,
    y: (slot % 5) * 100,
    z: Math.floor(slot / 5) * 100
  };
}

function makeNode(index, nodeCount) {
  const isTrigger = index === 0;
  const isEnd = index === nodeCount - 1;
  const type = isTrigger ? "trigger" : isEnd ? "end" : "process";

  return {
    id: `node${index}`,
    type,
    label: isTrigger ? "Inicio" : isEnd ? "Fin" : `Proceso ${index}`,
    inputs: isTrigger ? [] : [{ id: "input", label: "Entrada" }],
    outputs: isEnd ? [] : [{ id: "next", label: "Continuar" }],
    activationMode: "each",
    durationMs: isTrigger || isEnd ? 0 : (index % 20) + 1,
    configuration:
      type === "trigger"
        ? { eventName: "performance.started" }
        : type === "end"
          ? { result: "success" }
          : { operations: [] },
    position: positionFor(index),
    locked: false,
    metadata: { category: `cluster-${index % 10}` }
  };
}

export function generateLargeFixture(
  nodeCount = DEFAULT_NODE_COUNT,
  edgeCount = DEFAULT_EDGE_COUNT
) {
  if (!Number.isInteger(nodeCount) || nodeCount < 2 || nodeCount > 500) {
    throw new RangeError("nodeCount debe estar entre 2 y 500");
  }
  if (
    !Number.isInteger(edgeCount) ||
    edgeCount < nodeCount - 1 ||
    edgeCount > 1000
  ) {
    throw new RangeError("edgeCount debe incluir la cadena base y no superar 1000");
  }

  const nodes = Array.from({ length: nodeCount }, (_, index) =>
    makeNode(index, nodeCount)
  );
  const pairs = [];
  const seen = new Set();

  const addPair = (source, target) => {
    const key = `${source}:${target}`;
    if (source === target || seen.has(key)) {
      return false;
    }
    seen.add(key);
    pairs.push([source, target]);
    return true;
  };

  for (let index = 0; index < nodeCount - 1; index += 1) {
    addPair(index, index + 1);
  }

  let cursor = 0;
  while (pairs.length < edgeCount) {
    const source = cursor % (nodeCount - 1);
    const target = 1 + ((source * 37 + Math.floor(cursor / nodeCount) * 53) % (nodeCount - 1));
    addPair(source, target);
    cursor += 1;

    if (cursor > nodeCount * edgeCount * 2) {
      throw new Error("No fue posible generar suficientes conexiones únicas");
    }
  }

  const edges = pairs.map(([source, target], index) => ({
    id: `edge${index}`,
    source: `node${source}`,
    target: `node${target}`,
    sourcePort: "next",
    targetPort: "input",
    priority: index,
    isDefault: false
  }));

  return {
    schemaVersion: "1.0",
    name: `Fixture de rendimiento ${nodeCount}/${edgeCount}`,
    description: "Grafo determinista para pruebas de carga visual y algorítmica.",
    metadata: {
      tags: ["performance", "generated"],
      createdWith: "generate-large-fixture.mjs"
    },
    variables: [],
    layout: { mode: "force" },
    nodes,
    edges
  };
}

const invokedDirectly =
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href;

if (invokedDirectly) {
  process.stdout.write(`${JSON.stringify(generateLargeFixture(), null, 2)}\n`);
}
import path from "node:path";
import { pathToFileURL } from "node:url";

