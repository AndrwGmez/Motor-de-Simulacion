import type {
  FlowCondition,
  FlowDefinition,
  FlowEdge,
  RunEvent,
  SimulationOverrides,
  SimulationPlan,
} from "@flowverse/core";

function valueAt(input: unknown, path: string): unknown {
  if (!path || path === "/") return input;
  const parts = path.startsWith("/")
    ? path
        .slice(1)
        .split("/")
        .map((part) => part.replace(/~1/g, "/").replace(/~0/g, "~"))
    : path.split(".");
  let current = input;
  for (const part of parts) {
    if (typeof current !== "object" || current === null || !(part in current)) return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
}

export function evaluateCondition(condition: FlowCondition, input: unknown): boolean {
  if ("conditions" in condition) {
    return condition.operator === "and"
      ? condition.conditions.every((child) => evaluateCondition(child, input))
      : condition.conditions.some((child) => evaluateCondition(child, input));
  }

  const actual = valueAt(input, condition.field);
  switch (condition.operator) {
    case "equals":
      return actual === condition.value;
    case "not_equals":
      return actual !== condition.value;
    case "greater_than":
      return typeof actual === "number" && typeof condition.value === "number" && actual > condition.value;
    case "greater_than_or_equal":
      return typeof actual === "number" && typeof condition.value === "number" && actual >= condition.value;
    case "less_than":
      return typeof actual === "number" && typeof condition.value === "number" && actual < condition.value;
    case "less_than_or_equal":
      return typeof actual === "number" && typeof condition.value === "number" && actual <= condition.value;
    case "contains":
      return typeof actual === "string"
        ? typeof condition.value === "string" && actual.includes(condition.value)
        : Array.isArray(actual) && actual.some((item) => item === condition.value);
    case "not_contains":
      return !evaluateCondition({ ...condition, operator: "contains" }, input);
    case "exists":
      return actual !== undefined;
    case "not_exists":
      return actual === undefined;
  }
}

function selectEdges(
  nodeId: string,
  flow: FlowDefinition,
  input: unknown,
  overrides: SimulationOverrides,
): FlowEdge[] {
  const outgoing = flow.edges
    .filter((edge) => edge.source === nodeId)
    .sort((a, b) => a.priority - b.priority || a.id.localeCompare(b.id));
  const forced = overrides.forcedEdgeIds[nodeId];
  if (forced) return outgoing.filter((edge) => edge.id === forced);

  const node = flow.nodes.find((candidate) => candidate.id === nodeId);
  if (node?.type !== "decision") return outgoing;

  const matching = outgoing.filter((edge) => !edge.isDefault && edge.condition && evaluateCondition(edge.condition, input));
  const mode = node.configuration.strategy === "all_matches" ? "all_matches" : "first_match";
  if (matching.length > 0) return mode === "all_matches" ? matching : [matching[0]];
  const fallback = outgoing.find((edge) => edge.isDefault);
  return fallback ? [fallback] : [];
}

export function createSimulationPlan(
  flow: FlowDefinition,
  input: unknown,
  overrides: Partial<SimulationOverrides> = {},
  triggerId?: string,
  flowVersionId = "local-draft",
): SimulationPlan {
  const normalizedOverrides: SimulationOverrides = {
    failedNodeIds: overrides.failedNodeIds ?? [],
    forcedEdgeIds: overrides.forcedEdgeIds ?? {},
  };
  const runId = crypto.randomUUID();
  const events: RunEvent[] = [];
  const start = flow.nodes.find((node) => node.id === triggerId && node.type === "trigger")
    ?? flow.nodes.find((node) => node.type === "trigger");
  let logicalTime = 0;
  let sequence = 0;
  let failed = false;
  const visited: string[] = [];
  const visitCounts = new Map<string, number>();

  const push = (type: RunEvent["type"], payload: RunEvent["payload"] = {}) => {
    sequence += 1;
    events.push({
      schemaVersion: "1.0",
      runId,
      sequence,
      occurredAt: new Date(Date.now() + logicalTime).toISOString(),
      logicalTimeMs: logicalTime,
      type,
      payload,
    });
  };

  push("run.started");
  if (!start) {
    push("run.failed", { error: "No existe un nodo de inicio." });
    return {
      runId,
      events,
      summary: {
        id: runId,
        flowVersionId,
        createdAt: events[0].occurredAt,
        completedAt: events.at(-1)?.occurredAt,
        status: "failed",
        durationMs: 0,
        visitedNodeIds: [],
        eventCount: events.length,
      },
    };
  }

  const queue: string[] = [start.id];
  push("node.queued", { nodeId: start.id });

  while (queue.length > 0 && sequence < 10_000) {
    const nodeId = queue.shift()!;
    const node = flow.nodes.find((candidate) => candidate.id === nodeId);
    if (!node || node.type === "group") continue;
    const count = (visitCounts.get(nodeId) ?? 0) + 1;
    visitCounts.set(nodeId, count);
    if (count > 100) {
      failed = true;
      push("node.failed", { nodeId, error: "Se alcanzó el límite de 100 visitas para este nodo." });
      continue;
    }

    visited.push(nodeId);
    push("node.started", { nodeId });
    logicalTime += Math.max(0, node.durationMs);

    if (normalizedOverrides.failedNodeIds.includes(nodeId)) {
      failed = true;
      push("node.failed", { nodeId, error: "Error forzado por los datos de prueba." });
      continue;
    }

    push("node.completed", { nodeId, output: node.configuration.result });
    if (node.type === "end") {
      if (node.configuration.result === "failure") failed = true;
      continue;
    }

    const selectedEdges = selectEdges(nodeId, flow, input, normalizedOverrides);
    for (const edge of selectedEdges) {
      push("edge.traversed", { edgeId: edge.id });
      queue.push(edge.target);
      push("node.queued", { nodeId: edge.target });
    }
  }

  if (sequence >= 10_000) {
    failed = true;
    push("run.failed", { error: "La simulación alcanzó el límite global de pasos." });
  } else {
    push(failed ? "run.failed" : "run.completed");
  }

  return {
    runId,
    events,
    summary: {
      id: runId,
      flowVersionId,
      createdAt: events[0].occurredAt,
      completedAt: events.at(-1)?.occurredAt,
      status: failed ? "failed" : "completed",
      durationMs: logicalTime,
      visitedNodeIds: [...new Set(visited)],
      eventCount: events.length,
    },
  };
}
