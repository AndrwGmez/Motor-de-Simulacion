import type { FlowDefinition, ValidationIssue } from "@flowverse/core";

function issue(
  code: string,
  severity: ValidationIssue["severity"],
  message: string,
  context: Pick<ValidationIssue, "nodeId" | "edgeId"> = {},
): ValidationIssue {
  return { id: `${code}-${context.nodeId ?? context.edgeId ?? "flow"}`, code, severity, message, ...context };
}

function reachableFrom(flow: FlowDefinition, starts: string[]): Set<string> {
  const outgoing = new Map<string, string[]>();
  for (const edge of flow.edges) {
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge.target]);
  }
  const visited = new Set<string>();
  const queue = [...starts];
  while (queue.length) {
    const current = queue.shift()!;
    if (visited.has(current)) continue;
    visited.add(current);
    queue.push(...(outgoing.get(current) ?? []));
  }
  return visited;
}

function stronglyConnectedComponents(flow: FlowDefinition): string[][] {
  const nodeIds = flow.nodes.filter((node) => node.type !== "group").map((node) => node.id);
  const edges = new Map<string, string[]>();
  for (const id of nodeIds) edges.set(id, []);
  for (const edge of flow.edges) edges.get(edge.source)?.push(edge.target);

  let index = 0;
  const indexes = new Map<string, number>();
  const lowLinks = new Map<string, number>();
  const stack: string[] = [];
  const inStack = new Set<string>();
  const result: string[][] = [];

  const visit = (id: string) => {
    indexes.set(id, index);
    lowLinks.set(id, index);
    index += 1;
    stack.push(id);
    inStack.add(id);

    for (const target of edges.get(id) ?? []) {
      if (!indexes.has(target)) {
        visit(target);
        lowLinks.set(id, Math.min(lowLinks.get(id)!, lowLinks.get(target)!));
      } else if (inStack.has(target)) {
        lowLinks.set(id, Math.min(lowLinks.get(id)!, indexes.get(target)!));
      }
    }

    if (lowLinks.get(id) === indexes.get(id)) {
      const component: string[] = [];
      let current: string;
      do {
        current = stack.pop()!;
        inStack.delete(current);
        component.push(current);
      } while (current !== id);
      result.push(component);
    }
  };

  for (const id of nodeIds) if (!indexes.has(id)) visit(id);
  return result;
}

export function validateFlow(flow: FlowDefinition): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const nodeIds = new Set<string>();
  for (const node of flow.nodes) {
    if (nodeIds.has(node.id)) {
      issues.push(issue("node.duplicate_id", "error", `El identificador “${node.id}” está duplicado.`, { nodeId: node.id }));
    }
    nodeIds.add(node.id);
    if (!node.label.trim()) {
      issues.push(issue("node.missing_label", "warning", "El nodo no tiene un nombre visible.", { nodeId: node.id }));
    }
  }

  const triggers = flow.nodes.filter((node) => node.type === "trigger");
  const ends = flow.nodes.filter((node) => node.type === "end");
  if (triggers.length === 0) issues.push(issue("flow.no_trigger", "error", "El flujo necesita al menos un nodo de inicio."));
  if (ends.length === 0) issues.push(issue("flow.no_end", "error", "El flujo necesita al menos un resultado final."));

  const edgeIds = new Set<string>();
  for (const edge of flow.edges) {
    if (edgeIds.has(edge.id)) {
      issues.push(issue("edge.duplicate_id", "error", `La conexión “${edge.id}” está duplicada.`, { edgeId: edge.id }));
    }
    edgeIds.add(edge.id);
    const source = flow.nodes.find((node) => node.id === edge.source);
    const target = flow.nodes.find((node) => node.id === edge.target);
    if (!source || !target) {
      issues.push(issue("edge.missing_node", "error", "La conexión apunta a un nodo inexistente.", { edgeId: edge.id }));
      continue;
    }
    if (source.type === "group" || target.type === "group") {
      issues.push(issue("edge.group_connection", "error", "Los grupos son contenedores visuales y no admiten conexiones.", { edgeId: edge.id }));
    }
    if (edge.sourcePort && !source.outputs.some((port) => port.id === edge.sourcePort)) {
      issues.push(issue("edge.missing_source_port", "error", `El puerto de salida “${edge.sourcePort}” no existe.`, { edgeId: edge.id }));
    }
    if (edge.targetPort && !target.inputs.some((port) => port.id === edge.targetPort)) {
      issues.push(issue("edge.missing_target_port", "error", `El puerto de entrada “${edge.targetPort}” no existe.`, { edgeId: edge.id }));
    }
  }

  const reachable = reachableFrom(flow, triggers.map((node) => node.id));
  for (const node of flow.nodes) {
    if (node.type !== "group" && !reachable.has(node.id)) {
      issues.push(issue("node.unreachable", "warning", `“${node.label}” nunca puede alcanzarse desde un inicio.`, { nodeId: node.id }));
    }
  }

  if (triggers.length > 0 && ends.length > 0 && !ends.some((node) => reachable.has(node.id))) {
    issues.push(issue("flow.no_reachable_end", "error", "Ningún resultado final es alcanzable desde los nodos de inicio."));
  }

  for (const decision of flow.nodes.filter((node) => node.type === "decision")) {
    const outgoing = flow.edges.filter((edge) => edge.source === decision.id);
    const defaults = outgoing.filter((edge) => edge.isDefault);
    if (outgoing.length < 2) {
      issues.push(issue("decision.insufficient_paths", "error", `“${decision.label}” necesita al menos dos caminos de salida.`, { nodeId: decision.id }));
    }
    if (defaults.length !== 1) {
      issues.push(
        issue(
          "decision.default_path",
          "error",
          defaults.length === 0
            ? `“${decision.label}” necesita un camino predeterminado.`
            : `“${decision.label}” tiene más de un camino predeterminado.`,
          { nodeId: decision.id },
        ),
      );
    }
    for (const edge of outgoing.filter((candidate) => !candidate.isDefault && !candidate.condition)) {
      issues.push(issue("decision.missing_condition", "error", `La salida “${edge.label || edge.id}” necesita una condición.`, { edgeId: edge.id }));
    }
  }

  for (const component of stronglyConnectedComponents(flow)) {
    const selfLoop = component.length === 1 && flow.edges.some((edge) => edge.source === component[0] && edge.target === component[0]);
    if (component.length === 1 && !selfLoop) continue;
    const members = new Set(component);
    const hasExit = flow.edges.some((edge) => members.has(edge.source) && !members.has(edge.target));
    issues.push(
      issue(
        hasExit ? "flow.cycle" : "flow.infinite_cycle",
        hasExit ? "warning" : "error",
        hasExit
          ? `Se detectó un ciclo con salida potencial entre ${component.length} nodo(s).`
          : `Se detectó un ciclo sin salida demostrable entre ${component.length} nodo(s).`,
        { nodeId: component[0] },
      ),
    );
  }

  return issues.sort((a, b) => {
    const rank = { error: 0, warning: 1, info: 2 };
    return rank[a.severity] - rank[b.severity] || a.message.localeCompare(b.message);
  });
}

export function flowMetrics(flow: FlowDefinition, issues = validateFlow(flow)) {
  const executableNodes = flow.nodes.filter((node) => node.type !== "group");
  const triggers = executableNodes.filter((node) => node.type === "trigger");
  const reachable = reachableFrom(flow, triggers.map((node) => node.id));
  const cycleCount = issues.filter((item) => item.code === "flow.cycle" || item.code === "flow.infinite_cycle").length;
  return {
    nodeCount: executableNodes.length,
    edgeCount: flow.edges.length,
    reachableCount: reachable.size,
    coveragePercent: executableNodes.length ? Math.round((reachable.size / executableNodes.length) * 100) : 0,
    complexity: Math.max(1, flow.edges.length - executableNodes.length + Math.max(1, triggers.length) + cycleCount),
    cycleCount,
    errors: issues.filter((item) => item.severity === "error").length,
    warnings: issues.filter((item) => item.severity === "warning").length,
  };
}
