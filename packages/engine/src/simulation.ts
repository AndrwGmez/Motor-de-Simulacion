import type {
  FlowCondition,
  FlowDefinition,
  FlowEdge,
  FlowNode,
  RunEvent,
  SimulationOverrides,
  SimulationPlan,
} from "@flowverse/core";
import { validateFlow } from "./validation";

export const SIMULATION_DEFAULT_MAX_STEPS = 10_000;
export const SIMULATION_MAX_STEPS = 100_000;
export const SIMULATION_DEFAULT_MAX_VISITS_PER_NODE = 100;
export const SIMULATION_MAX_VISITS_PER_NODE = 10_000;
export const SIMULATION_MAX_INPUT_PROPERTIES = 250;
export const SIMULATION_MAX_OVERRIDES = 100;

export interface SimulationLimits {
  maxSteps?: number;
  maxVisitsPerNode?: number;
}

/** Opciones deterministas adicionales sin alterar la firma histórica. */
export interface SimulationPlanOptions {
  runId?: string;
  startedAt?: string | Date;
  limits?: SimulationLimits;
}

export class SimulationInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SimulationInputError";
  }
}

interface ForkFrame {
  id: string;
  branchId: string;
  expected: string[];
  iteration: number;
}

interface ContextWrite {
  value?: unknown;
  deleted: boolean;
}

interface Token {
  id: string;
  nodeId: string;
  sourceEdge: string;
  context: Record<string, unknown>;
  writes: Map<string, ContextWrite>;
  lineage: ForkFrame[];
  logicalMs: number;
}

interface JoinBarrier {
  nodeId: string;
  forkId: string;
  iteration: number;
  expected: string[];
  arrivals: Map<string, Token>;
  logicalMs: number;
}

class ConditionEvaluationError extends Error {}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function cloneJSON<T>(value: T): T {
  return structuredClone(value);
}

function cloneContext(value: Record<string, unknown> | undefined): Record<string, unknown> {
  return value ? cloneJSON(value) : {};
}

function cloneWrites(value: Map<string, ContextWrite>): Map<string, ContextWrite> {
  return new Map([...value].map(([path, write]) => [path, {
    deleted: write.deleted,
    value: cloneJSON(write.value),
  }]));
}

function cloneLineage(lineage: ForkFrame[]): ForkFrame[] {
  return lineage.map((frame) => ({ ...frame, expected: [...frame.expected] }));
}

function compareText(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

function pointerParts(pointer: string): string[] {
  if (pointer === "") return [];
  const normalized = pointer.startsWith("/") ? pointer : `/${pointer.replaceAll(".", "/")}`;
  return normalized.slice(1).split("/").map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"));
}

function valueAt(root: unknown, pointer: string): unknown {
  let current = root;
  for (const part of pointerParts(pointer)) {
    if (Array.isArray(current)) {
      if (!/^[+-]?\d+$/.test(part)) throw new ConditionEvaluationError("value does not exist");
      const index = Number(part);
      if (!Number.isInteger(index) || index < 0 || index >= current.length) {
        throw new ConditionEvaluationError("value does not exist");
      }
      current = current[index];
      continue;
    }
    if (!isRecord(current) || !Object.prototype.hasOwnProperty.call(current, part)) {
      throw new ConditionEvaluationError("value does not exist");
    }
    current = current[part];
  }
  return current;
}

function setValue(root: Record<string, unknown>, pointer: string, value: unknown): void {
  const parts = pointerParts(pointer);
  if (parts.length === 0) throw new Error("set path must address a field");
  let current = root;
  for (const part of parts.slice(0, -1)) {
    if (!Object.prototype.hasOwnProperty.call(current, part)) {
      const child: Record<string, unknown> = {};
      current[part] = child;
      current = child;
      continue;
    }
    const child = current[part];
    if (!isRecord(child)) throw new Error(`path segment ${JSON.stringify(part)} is not an object`);
    current = child;
  }
  current[parts.at(-1)!] = value;
}

function deleteValue(root: Record<string, unknown>, pointer: string): boolean {
  const parts = pointerParts(pointer);
  if (parts.length === 0) throw new Error("delete path must address a field");
  let current = root;
  for (const part of parts.slice(0, -1)) {
    const child = current[part];
    if (!isRecord(child)) return false;
    current = child;
  }
  const key = parts.at(-1)!;
  if (!Object.prototype.hasOwnProperty.call(current, key)) return false;
  delete current[key];
  return true;
}

function deepEqual(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) return true;
  if (typeof left === "number" && typeof right === "number") return left === right;
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left)
      && Array.isArray(right)
      && left.length === right.length
      && left.every((value, index) => deepEqual(value, right[index]));
  }
  if (!isRecord(left) || !isRecord(right)) return false;
  const leftKeys = Object.keys(left).sort(compareText);
  const rightKeys = Object.keys(right).sort(compareText);
  return leftKeys.length === rightKeys.length
    && leftKeys.every((key, index) => key === rightKeys[index] && deepEqual(left[key], right[key]));
}

function contains(container: unknown, wanted: unknown): boolean {
  if (typeof container === "string") {
    if (typeof wanted !== "string") throw new ConditionEvaluationError("string contains requires a string value");
    return container.includes(wanted);
  }
  if (Array.isArray(container)) return container.some((item) => deepEqual(item, wanted));
  if (isRecord(container)) {
    if (typeof wanted !== "string") throw new ConditionEvaluationError("object contains requires a string key");
    return Object.prototype.hasOwnProperty.call(container, wanted);
  }
  throw new ConditionEvaluationError(`contains is not supported for ${typeof container}`);
}

function evaluateConditionStrict(condition: FlowCondition, input: unknown): boolean {
  if ("conditions" in condition) {
    if (condition.conditions.length === 0) {
      throw new ConditionEvaluationError(`${condition.operator} requires at least one condition`);
    }
    if (condition.operator === "and") {
      for (const child of condition.conditions) {
        if (!evaluateConditionStrict(child, input)) return false;
      }
      return true;
    }
    // El motor productivo permite que otra rama verdadera resuelva un OR aun
    // cuando una rama anterior no pueda evaluarse.
    for (const child of condition.conditions) {
      try {
        if (evaluateConditionStrict(child, input)) return true;
      } catch {
        // Conserva la semántica de engine.EvaluateCondition para operator=or.
      }
    }
    return false;
  }

  let actual: unknown;
  try {
    actual = valueAt(input, condition.field);
  } catch (error) {
    if (condition.operator === "exists") return false;
    if (condition.operator === "not_exists") return true;
    throw error;
  }
  if (condition.operator === "exists") return true;
  if (condition.operator === "not_exists") return false;

  switch (condition.operator) {
    case "equals":
      return deepEqual(actual, condition.value);
    case "not_equals":
      return !deepEqual(actual, condition.value);
    case "greater_than":
    case "greater_than_or_equal":
    case "less_than":
    case "less_than_or_equal": {
      if (typeof actual !== "number" || typeof condition.value !== "number") {
        throw new ConditionEvaluationError(`${condition.operator} requires numeric values`);
      }
      if (condition.operator === "greater_than") return actual > condition.value;
      if (condition.operator === "greater_than_or_equal") return actual >= condition.value;
      if (condition.operator === "less_than") return actual < condition.value;
      return actual <= condition.value;
    }
    case "contains":
      return contains(actual, condition.value);
    case "not_contains":
      return !contains(actual, condition.value);
  }
}

export function evaluateCondition(condition: FlowCondition, input: unknown): boolean {
  return evaluateConditionStrict(condition, input);
}

function configString(configuration: Record<string, unknown>, key: string, fallback: string): string {
  const value = configuration[key];
  return typeof value === "string" && value !== "" ? value : fallback;
}

function configInteger(configuration: Record<string, unknown>, key: string): number {
  const value = configuration[key];
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? Math.trunc(value) : 0;
}

function effectiveDuration(node: FlowNode): number {
  let duration = node.durationMs;
  if (node.type === "integration") duration += configInteger(node.configuration, "latencyMs");
  if (node.type === "delay") duration += configInteger(node.configuration, "delayMs");
  return duration;
}

function shouldFail(node: FlowNode): boolean {
  return node.type === "integration" && configString(node.configuration, "outcome", "success") === "failure"
    || node.configuration.shouldFail === true;
}

function withVariableDefaults(flow: FlowDefinition, input?: Record<string, unknown>): Record<string, unknown> {
  const result = cloneContext(input);
  for (const definition of [...flow.variables].sort((left, right) => compareText(left.path, right.path))) {
    try {
      valueAt(result, definition.path);
    } catch {
      // Go omite defaults JSON null (decodificados como nil).
      if (definition.default !== undefined && definition.default !== null) {
        setValue(result, definition.path, cloneJSON(definition.default));
      }
    }
  }
  return result;
}

function applyOperations(
  node: FlowNode,
  context: Record<string, unknown>,
  writes: Map<string, ContextWrite>,
): void {
  if (!("operations" in node.configuration)) return;
  const operations = node.configuration.operations;
  if (!Array.isArray(operations)) throw new Error("invalid operations");
  for (const rawOperation of operations) {
    if (!isRecord(rawOperation)) throw new Error("invalid operations");
    const operation = typeof rawOperation.op === "string" ? rawOperation.op : "";
    const path = typeof rawOperation.path === "string" ? rawOperation.path : "";
    switch (operation) {
      case "set": {
        const value = cloneJSON(rawOperation.value === undefined ? null : rawOperation.value);
        setValue(context, path, value);
        writes.set(path, { value, deleted: false });
        break;
      }
      case "copy": {
        const from = typeof rawOperation.from === "string" ? rawOperation.from : "";
        let value: unknown;
        try {
          value = cloneJSON(valueAt(context, from));
        } catch (error) {
          throw new Error(`copy from ${from}: ${error instanceof Error ? error.message : String(error)}`);
        }
        setValue(context, path, value);
        writes.set(path, { value, deleted: false });
        break;
      }
      case "delete":
        deleteValue(context, path);
        writes.set(path, { deleted: true });
        break;
      default:
        throw new Error(`unsupported operation ${JSON.stringify(operation)}`);
    }
  }
}

function selectEdges(
  node: FlowNode,
  edges: FlowEdge[],
  context: Record<string, unknown>,
  forcedEdge: string | undefined,
): FlowEdge[] {
  if (forcedEdge) {
    const selected = edges.find((edge) => edge.id === forcedEdge);
    if (!selected) throw new Error(`forced edge ${JSON.stringify(forcedEdge)} is not outgoing from node ${JSON.stringify(node.id)}`);
    return [selected];
  }
  if (node.type !== "decision") {
    const selected: FlowEdge[] = [];
    for (const edge of edges) {
      if (!edge.condition) {
        selected.push(edge);
        continue;
      }
      try {
        if (evaluateConditionStrict(edge.condition, context)) selected.push(edge);
      } catch (error) {
        throw new Error(`edge ${edge.id}: ${error instanceof Error ? error.message : String(error)}`);
      }
    }
    return selected;
  }

  const mode = configString(
    node.configuration,
    "strategy",
    configString(node.configuration, "mode", "first_match"),
  );
  const matched: FlowEdge[] = [];
  let fallback: FlowEdge | undefined;
  for (const edge of edges) {
    if (edge.isDefault) {
      fallback = edge;
      continue;
    }
    if (!edge.condition) continue;
    let conditionMatched: boolean;
    try {
      conditionMatched = evaluateConditionStrict(edge.condition, context);
    } catch (error) {
      throw new Error(`edge ${edge.id}: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (conditionMatched) {
      matched.push(edge);
      if (mode !== "all_matches") break;
    }
  }
  if (matched.length === 0 && fallback) matched.push(fallback);
  if (matched.length === 0) throw new Error(`decision ${JSON.stringify(node.id)} did not select an edge`);
  return matched;
}

function mergeTokens(
  input: Record<string, unknown>,
  defaults: Record<string, unknown>,
  tokens: Token[],
): { context: Record<string, unknown>; writes: Map<string, ContextWrite> } {
  const context = cloneContext(defaults);
  for (const [key, value] of Object.entries(cloneContext(input))) context[key] = value;
  const writes = new Map<string, ContextWrite>();
  for (const token of tokens) {
    for (const path of [...token.writes.keys()].sort(compareText)) {
      const write = token.writes.get(path)!;
      const previous = writes.get(path);
      if (previous && (previous.deleted !== write.deleted || !deepEqual(previous.value, write.value))) {
        throw new Error(`context.merge_conflict at ${path}`);
      }
      writes.set(path, { deleted: write.deleted, value: cloneJSON(write.value) });
    }
  }
  for (const [path, write] of [...writes].sort(([left], [right]) => compareText(left, right))) {
    if (write.deleted) deleteValue(context, path);
    else setValue(context, path, cloneJSON(write.value));
  }
  return { context, writes };
}

function mergeOutputs(outputs: Record<string, unknown>[]): Record<string, unknown> {
  if (outputs.length === 0) return {};
  if (outputs.length === 1) return outputs[0]!;
  return { branches: outputs };
}

function normalizeLimits(limits: SimulationLimits | undefined): Required<SimulationLimits> {
  const maxSteps = limits?.maxSteps ?? 0;
  const maxVisitsPerNode = limits?.maxVisitsPerNode ?? 0;
  if (!Number.isInteger(maxSteps) || maxSteps < 0 || maxSteps > SIMULATION_MAX_STEPS) {
    throw new SimulationInputError(`maxSteps must be between 1 and ${SIMULATION_MAX_STEPS} when provided`);
  }
  if (!Number.isInteger(maxVisitsPerNode) || maxVisitsPerNode < 0 || maxVisitsPerNode > SIMULATION_MAX_VISITS_PER_NODE) {
    throw new SimulationInputError(
      `maxVisitsPerNode must be between 1 and ${SIMULATION_MAX_VISITS_PER_NODE} when provided`,
    );
  }
  return {
    maxSteps: maxSteps || SIMULATION_DEFAULT_MAX_STEPS,
    maxVisitsPerNode: maxVisitsPerNode || SIMULATION_DEFAULT_MAX_VISITS_PER_NODE,
  };
}

function barrierKey(nodeId: string, frame: ForkFrame): string {
  return `${nodeId}\u0000${frame.id}\u0000${frame.iteration}`;
}

function orderedArrivals(barrier: JoinBarrier): Token[] {
  return barrier.expected.flatMap((branchId) => {
    const arrival = barrier.arrivals.get(branchId);
    return arrival ? [arrival] : [];
  });
}

function missingBranches(barrier: JoinBarrier): string[] {
  return barrier.expected.filter((branchId) => !barrier.arrivals.has(branchId));
}

function isBarrierComplete(barrier: JoinBarrier): boolean {
  return barrier.expected.every((branchId) => barrier.arrivals.has(branchId));
}

/**
 * Construye el mismo plan lógico que engine.Simulator.Run. Los cinco primeros
 * argumentos conservan la API anterior; el sexto permite IDs/tiempo/límites
 * reproducibles para CLI, Scenario Lab y el corpus de conformidad.
 */
export function createSimulationPlan(
  flow: FlowDefinition,
  input: unknown,
  overrides: Partial<SimulationOverrides> = {},
  triggerId?: string,
  flowVersionId = "local-draft",
  planOptions: SimulationPlanOptions = {},
): SimulationPlan {
  const validation = validateFlow(flow);
  if (validation.some((issue) => issue.severity === "error")) {
    throw new SimulationInputError("flow is invalid");
  }
  if (!isRecord(input)) throw new SimulationInputError("input must be a JSON object");
  if (Object.keys(input).length > SIMULATION_MAX_INPUT_PROPERTIES) {
    throw new SimulationInputError(`input must contain at most ${SIMULATION_MAX_INPUT_PROPERTIES} properties`);
  }
  const failedNodeIds = new Set(overrides.failedNodeIds ?? []);
  const forcedEdgeIds = { ...(overrides.forcedEdgeIds ?? {}) };
  if (failedNodeIds.size + Object.keys(forcedEdgeIds).length > SIMULATION_MAX_OVERRIDES) {
    throw new SimulationInputError(`overrides must contain at most ${SIMULATION_MAX_OVERRIDES} entries`);
  }
  const limits = normalizeLimits(planOptions.limits);
  const nodes = new Map(flow.nodes.map((node) => [node.id, node]));
  const outgoing = new Map<string, FlowEdge[]>();
  for (const edge of flow.edges) outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge]);
  for (const edges of outgoing.values()) {
    edges.sort((left, right) => left.priority - right.priority || compareText(left.id, right.id));
  }
  const triggers = flow.nodes.filter((node) => node.type === "trigger").map((node) => node.id).sort(compareText);
  let selectedTrigger = triggerId ?? "";
  if (!selectedTrigger && triggers.length === 1) selectedTrigger = triggers[0]!;
  if (nodes.get(selectedTrigger)?.type !== "trigger") {
    throw new SimulationInputError("a valid triggerId must be selected");
  }

  const runId = planOptions.runId ?? crypto.randomUUID();
  const startedAt = planOptions.startedAt instanceof Date
    ? new Date(planOptions.startedAt)
    : new Date(planOptions.startedAt ?? Date.now());
  if (Number.isNaN(startedAt.getTime())) throw new SimulationInputError("startedAt must be a valid date");
  const events: RunEvent[] = [];
  let sequence = 0;
  const emit = (type: RunEvent["type"], logicalMs: number, payload: RunEvent["payload"] = {}) => {
    sequence += 1;
    events.push({
      schemaVersion: "1.0",
      runId,
      sequence,
      occurredAt: new Date(startedAt.getTime() + logicalMs).toISOString(),
      logicalTimeMs: logicalMs,
      type,
      payload,
    });
  };

  const queue: Token[] = [];
  let nextToken = 0;
  let nextFork = 0;
  const enqueue = (
    nodeId: string,
    sourceEdge: string,
    context: Record<string, unknown>,
    writes: Map<string, ContextWrite>,
    lineage: ForkFrame[],
    logicalMs: number,
  ) => {
    nextToken += 1;
    const token: Token = {
      id: `token-${String(nextToken).padStart(6, "0")}`,
      nodeId,
      sourceEdge,
      context: cloneContext(context),
      writes: cloneWrites(writes),
      lineage: cloneLineage(lineage),
      logicalMs,
    };
    queue.push(token);
    emit("node.queued", logicalMs, { nodeId, tokenId: token.id });
  };

  const initialContext = withVariableDefaults(flow, input);
  emit("run.started", 0, { triggerId: selectedTrigger });
  enqueue(selectedTrigger, "", initialContext, new Map(), [], 0);

  const visits = new Map<string, number>();
  const joinBarriers = new Map<string, JoinBarrier>();
  const anyResolved = new Set<string>();
  const visitedPath: string[] = [];
  const edgeCounts: Record<string, number> = {};
  const nodeTimesMs: Record<string, number> = {};
  const outputs: Record<string, unknown>[] = [];
  let steps = 0;
  let failed = false;
  let limitExceeded = false;
  let runError = "";
  let runCode = "";
  const markFailure = (code: string, message: string) => {
    failed = true;
    if (!runError) {
      runError = message;
      runCode = code;
    }
  };

  while (queue.length > 0) {
    queue.sort((left, right) => left.logicalMs - right.logicalMs
      || compareText(left.nodeId, right.nodeId)
      || compareText(left.id, right.id));
    const current = queue.shift()!;
    const node = nodes.get(current.nodeId)!;
    const activationMode = node.activationMode || "each";

    if (activationMode === "any" || activationMode === "all") {
      const frame = current.lineage.at(-1);
      if (!frame) {
        const message = `join ${JSON.stringify(node.id)} received an uncorrelated token`;
        markFailure("join.uncorrelated", message);
        emit("node.failed", current.logicalMs, {
          nodeId: node.id,
          tokenId: current.id,
          code: "join.uncorrelated",
          error: message,
        });
        continue;
      }
      const key = barrierKey(node.id, frame);
      if (activationMode === "any") {
        if (anyResolved.has(key)) {
          emit("node.skipped", current.logicalMs, {
            nodeId: node.id,
            tokenId: current.id,
            forkId: frame.id,
            reason: "join.any_already_resolved",
          });
          continue;
        }
        anyResolved.add(key);
        current.lineage = cloneLineage(current.lineage.slice(0, -1));
      } else {
        let barrier = joinBarriers.get(key);
        if (!barrier) {
          barrier = {
            nodeId: node.id,
            forkId: frame.id,
            iteration: frame.iteration,
            expected: [...frame.expected],
            arrivals: new Map(),
            logicalMs: 0,
          };
          joinBarriers.set(key, barrier);
        }
        if (barrier.arrivals.has(frame.branchId)) {
          emit("node.skipped", current.logicalMs, {
            nodeId: node.id,
            tokenId: current.id,
            forkId: frame.id,
            branchId: frame.branchId,
            reason: "join.all_duplicate_branch",
          });
          continue;
        }
        barrier.arrivals.set(frame.branchId, current);
        barrier.logicalMs = Math.max(barrier.logicalMs, current.logicalMs);
        if (!isBarrierComplete(barrier)) {
          emit("node.waiting", current.logicalMs, {
            nodeId: node.id,
            tokenId: current.id,
            forkId: frame.id,
            branchId: frame.branchId,
            received: barrier.arrivals.size,
            expected: barrier.expected.length,
          });
          continue;
        }
        joinBarriers.delete(key);
        try {
          const merged = mergeTokens(input, withVariableDefaults(flow), orderedArrivals(barrier));
          current.context = merged.context;
          current.writes = merged.writes;
          current.lineage = cloneLineage(current.lineage.slice(0, -1));
          current.logicalMs = barrier.logicalMs;
        } catch (error) {
          const message = error instanceof Error ? error.message : String(error);
          markFailure("context.merge_conflict", message);
          emit("node.failed", barrier.logicalMs, {
            nodeId: node.id,
            tokenId: current.id,
            forkId: frame.id,
            code: "context.merge_conflict",
            error: message,
          });
          continue;
        }
      }
    }

    const nextStep = steps + 1;
    const nextVisit = (visits.get(node.id) ?? 0) + 1;
    if (nextStep > limits.maxSteps) {
      runError = `maximum step count ${limits.maxSteps} exceeded`;
      runCode = "run.max_steps";
      failed = true;
      limitExceeded = true;
      emit("run.limit_exceeded", current.logicalMs, {
        code: runCode,
        limit: "maxSteps",
        maximum: limits.maxSteps,
        actual: nextStep,
        nodeId: node.id,
      });
      queue.length = 0;
      break;
    }
    if (nextVisit > limits.maxVisitsPerNode) {
      runError = `maximum visits per node ${limits.maxVisitsPerNode} exceeded for ${JSON.stringify(node.id)}`;
      runCode = "run.max_visits_per_node";
      failed = true;
      limitExceeded = true;
      emit("run.limit_exceeded", current.logicalMs, {
        code: runCode,
        limit: "maxVisitsPerNode",
        maximum: limits.maxVisitsPerNode,
        actual: nextVisit,
        nodeId: node.id,
      });
      queue.length = 0;
      break;
    }
    steps = nextStep;
    visits.set(node.id, nextVisit);
    visitedPath.push(node.id);
    const started = current.logicalMs;
    emit("node.started", started, { nodeId: node.id, tokenId: current.id });

    if (failedNodeIds.has(node.id)) {
      const message = "forced node failure";
      markFailure("node.forced_failure", message);
      emit("node.failed", started, {
        nodeId: node.id,
        tokenId: current.id,
        code: "node.forced_failure",
        error: message,
      });
      continue;
    }
    if (shouldFail(node)) {
      const errorCode = configString(node.configuration, "errorCode", "integration.simulated_failure");
      const message = `simulated integration failure (${errorCode})`;
      markFailure("integration.simulated_failure", message);
      emit("node.failed", started, {
        nodeId: node.id,
        tokenId: current.id,
        code: "integration.simulated_failure",
        errorCode,
        error: message,
      });
      continue;
    }

    const nextContext = cloneContext(current.context);
    const nextWrites = cloneWrites(current.writes);
    try {
      applyOperations(node, nextContext, nextWrites);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      markFailure("operation.invalid", message);
      emit("node.failed", started, {
        nodeId: node.id,
        tokenId: current.id,
        code: "operation.invalid",
        error: message,
      });
      continue;
    }
    current.context = nextContext;
    current.writes = nextWrites;

    const duration = effectiveDuration(node);
    const completed = started + duration;
    nodeTimesMs[node.id] = (nodeTimesMs[node.id] ?? 0) + duration;
    emit("node.completed", completed, { nodeId: node.id, tokenId: current.id, durationMs: duration });

    if (node.type === "end") {
      if (Object.prototype.hasOwnProperty.call(node.configuration, "output")) {
        const configured = node.configuration.output;
        if (typeof configured === "string") {
          try {
            current.context = { result: cloneJSON(valueAt(current.context, configured)) };
          } catch {
            // El motor Go conserva el contexto si el pointer configurado no existe.
          }
        } else {
          current.context = { result: cloneJSON(configured) };
        }
      }
      outputs.push(cloneContext(current.context));
      if (configString(node.configuration, "result", "success") === "failure") {
        markFailure("end.failure", `end node ${JSON.stringify(node.id)} reported failure`);
      }
      continue;
    }

    let selected: FlowEdge[];
    try {
      selected = selectEdges(node, outgoing.get(node.id) ?? [], current.context, forcedEdgeIds[node.id]);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      markFailure("condition.invalid", message);
      emit("node.failed", completed, {
        nodeId: node.id,
        tokenId: current.id,
        code: "condition.invalid",
        error: message,
      });
      continue;
    }

    let forkTemplate: Omit<ForkFrame, "branchId"> | undefined;
    if (selected.length > 1) {
      nextFork += 1;
      forkTemplate = {
        id: `fork-${String(nextFork).padStart(6, "0")}`,
        expected: selected.map((edge) => edge.id),
        iteration: visits.get(node.id)!,
      };
    }
    for (const edge of selected) {
      edgeCounts[edge.id] = (edgeCounts[edge.id] ?? 0) + 1;
      emit("edge.traversed", completed, {
        edgeId: edge.id,
        source: edge.source,
        target: edge.target,
        tokenId: current.id,
      });
      const lineage = cloneLineage(current.lineage);
      if (forkTemplate) lineage.push({ ...forkTemplate, branchId: edge.id, expected: [...forkTemplate.expected] });
      enqueue(edge.target, edge.id, current.context, current.writes, lineage, completed);
    }
  }

  if (!limitExceeded && joinBarriers.size > 0) {
    const barriers = [...joinBarriers.values()].sort((left, right) => compareText(left.nodeId, right.nodeId)
      || compareText(left.forkId, right.forkId)
      || left.iteration - right.iteration);
    for (const barrier of barriers) {
      const missing = missingBranches(barrier);
      const message = `join ${JSON.stringify(barrier.nodeId)} cannot resolve fork ${JSON.stringify(barrier.forkId)}; missing branches: ${missing.join(",")}`;
      markFailure("run.deadlock", message);
      const tokenId = orderedArrivals(barrier)[0]?.id ?? "";
      emit("node.failed", barrier.logicalMs, {
        nodeId: barrier.nodeId,
        tokenId,
        forkId: barrier.forkId,
        code: "run.deadlock",
        missingBranches: missing,
        error: message,
      });
    }
  }

  if (!limitExceeded && outputs.length === 0 && !failed) {
    markFailure("run.no_end_reached", "no end node was reached");
  }
  const output = mergeOutputs(outputs);
  if (!limitExceeded) {
    const lastLogical = events.reduce((maximum, event) => Math.max(maximum, event.logicalTimeMs), 0);
    if (failed) emit("run.failed", lastLogical, { code: runCode, error: runError });
    else emit("run.completed", lastLogical, { outputCount: outputs.length });
  }

  const durationMs = events.reduce((maximum, event) => Math.max(maximum, event.logicalTimeMs), 0);
  const uniqueVisited = [...new Set(visitedPath)];
  return {
    runId,
    events,
    summary: {
      id: runId,
      flowVersionId,
      createdAt: events[0]?.occurredAt,
      completedAt: events.at(-1)?.occurredAt,
      status: failed ? "failed" : "completed",
      durationMs,
      visitedNodeIds: uniqueVisited,
      eventCount: events.length,
    },
    output,
    error: runError || undefined,
    visitedPath,
    edgeCounts,
    nodeTimesMs,
  };
}
