import type { FlowDefinition, LayoutMode, Point3D } from "./flow-types";

function stageByNode(flow: FlowDefinition): Map<string, number> {
  const stages = new Map<string, number>();
  const queue = flow.nodes.filter((node) => node.type === "trigger").map((node) => ({ id: node.id, stage: 0 }));
  let guard = 0;
  while (queue.length > 0 && guard < flow.nodes.length * 4) {
    guard += 1;
    const current = queue.shift()!;
    const previous = stages.get(current.id);
    if (previous !== undefined && previous >= current.stage) continue;
    stages.set(current.id, current.stage);
    for (const edge of flow.edges.filter((candidate) => candidate.source === current.id)) {
      if (edge.target !== current.id) queue.push({ id: edge.target, stage: current.stage + 1 });
    }
  }
  return stages;
}

export function positionsForLayout(
  flow: FlowDefinition,
  mode: LayoutMode,
  executionPath: string[] = [],
): Map<string, Point3D> {
  const result = new Map<string, Point3D>();
  const executable = flow.nodes.filter((node) => node.type !== "group");
  if (mode === "force") {
    for (const node of flow.nodes) result.set(node.id, node.position);
    return result;
  }

  if (mode === "clusters") {
    const categories = [...new Set(executable.map((node) => node.metadata.category ?? node.type))].sort();
    const centers = new Map(categories.map((category, index) => {
      const angle = (index / Math.max(1, categories.length)) * Math.PI * 2;
      return [category, { x: Math.cos(angle) * 260, y: Math.sin(angle) * 180, z: (index % 3 - 1) * 90 }] as const;
    }));
    const offsets = new Map<string, number>();
    for (const node of executable) {
      const category = node.metadata.category ?? node.type;
      const index = offsets.get(category) ?? 0;
      offsets.set(category, index + 1);
      const center = centers.get(category)!;
      const angle = index * 2.4;
      const radius = 38 + Math.sqrt(index) * 22;
      result.set(node.id, {
        x: center.x + Math.cos(angle) * radius,
        y: center.y + Math.sin(angle) * radius,
        z: center.z + (index % 3 - 1) * 26,
      });
    }
  } else if (mode === "execution") {
    const path = executionPath.length > 0 ? executionPath : executable.map((node) => node.id);
    path.forEach((id, index) => result.set(id, { x: (index - path.length / 2) * 120, y: 0, z: 0 }));
    executable.filter((node) => !result.has(node.id)).forEach((node, index) => {
      result.set(node.id, { x: 0, y: -280 - index * 12, z: -180 });
    });
  } else {
    const stages = stageByNode(flow);
    const grouped = new Map<number, typeof executable>();
    for (const node of executable) {
      const stage = stages.get(node.id) ?? 0;
      grouped.set(stage, [...(grouped.get(stage) ?? []), node]);
    }
    for (const [stage, nodes] of grouped) {
      nodes.sort((a, b) => a.label.localeCompare(b.label));
      nodes.forEach((node, row) => {
        const centered = row - (nodes.length - 1) / 2;
        if (mode === "layers") {
          result.set(node.id, { x: centered * 125, y: 0, z: stage * -120 });
        } else if (mode === "timeline") {
          result.set(node.id, { x: (stage - grouped.size / 2) * 135, y: centered * 95, z: stage * -18 });
        } else {
          result.set(node.id, { x: (stage - grouped.size / 2) * 135, y: centered * 115, z: 0 });
        }
      });
    }
  }

  for (const group of flow.nodes.filter((node) => node.type === "group")) result.set(group.id, group.position);
  return result;
}

export function applyLayout(flow: FlowDefinition, mode: LayoutMode, executionPath: string[] = []): FlowDefinition {
  const positions = positionsForLayout(flow, mode, executionPath);
  return {
    ...flow,
    layout: { ...flow.layout, mode },
    nodes: flow.nodes.map((node) => node.locked ? node : { ...node, position: positions.get(node.id) ?? node.position }),
  };
}
