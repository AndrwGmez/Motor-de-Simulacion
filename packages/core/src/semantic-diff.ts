import type { FlowDefinition, FlowEdge, FlowNode, FlowVariable } from "./flow-types";

export type SemanticImpact = "visual" | "behavioral" | "breaking";
export type SemanticEntity = "flow" | "layout" | "variable" | "node" | "edge";
export type SemanticOperation = "added" | "removed" | "modified";

export interface SemanticFieldChange {
  path: string;
  impact: SemanticImpact;
  before?: unknown;
  after?: unknown;
}

export interface SemanticChange {
  entity: SemanticEntity;
  id: string;
  label?: string;
  operation: SemanticOperation;
  impact: SemanticImpact;
  fields: SemanticFieldChange[];
}

export interface SemanticDiffSummary {
  added: number;
  removed: number;
  modified: number;
  visual: number;
  behavioral: number;
  breaking: number;
}

export interface FlowSemanticDiff {
  hasChanges: boolean;
  behaviorChanged: boolean;
  highestImpact: SemanticImpact | "none";
  summary: SemanticDiffSummary;
  changes: SemanticChange[];
}

const IMPACT_RANK: Record<SemanticImpact | "none", number> = {
  none: 0,
  visual: 1,
  behavioral: 2,
  breaking: 3,
};

const FLOW_IMPACT: Record<string, SemanticImpact> = {
  schemaVersion: "breaking",
  name: "visual",
  description: "visual",
  metadata: "visual",
};

const LAYOUT_IMPACT: Record<string, SemanticImpact> = {
  mode: "visual",
  clusterBy: "visual",
  camera: "visual",
};

const NODE_IMPACT: Record<string, SemanticImpact> = {
  type: "breaking",
  label: "visual",
  description: "visual",
  inputs: "breaking",
  outputs: "breaking",
  activationMode: "behavioral",
  durationMs: "behavioral",
  configuration: "behavioral",
  position: "visual",
  locked: "visual",
  metadata: "visual",
};

const EDGE_IMPACT: Record<string, SemanticImpact> = {
  source: "breaking",
  target: "breaking",
  sourcePort: "breaking",
  targetPort: "breaking",
  label: "visual",
  condition: "behavioral",
  isDefault: "behavioral",
  priority: "behavioral",
};

const VARIABLE_IMPACT: Record<string, SemanticImpact> = {
  type: "breaking",
  required: "breaking",
  default: "behavioral",
  description: "visual",
};

function stableValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, entry]) => [key, stableValue(entry)]),
    );
  }
  return value;
}

function equal(left: unknown, right: unknown): boolean {
  return JSON.stringify(stableValue(left)) === JSON.stringify(stableValue(right));
}

function highestImpact(impacts: SemanticImpact[]): SemanticImpact {
  return impacts.reduce(
    (highest, candidate) => IMPACT_RANK[candidate] > IMPACT_RANK[highest] ? candidate : highest,
    "visual" as SemanticImpact,
  );
}

function changedFields(
  before: Record<string, unknown>,
  after: Record<string, unknown>,
  impacts: Record<string, SemanticImpact>,
  resolveImpact?: (key: string, before: unknown, after: unknown) => SemanticImpact,
): SemanticFieldChange[] {
  const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])]
    .filter((key) => key !== "id")
    .sort();
  return keys.flatMap((key) => {
    if (equal(before[key], after[key])) return [];
    return [{
      path: `/${key}`,
      impact: resolveImpact?.(key, before[key], after[key]) ?? impacts[key] ?? "behavioral",
      before: before[key],
      after: after[key],
    } satisfies SemanticFieldChange];
  });
}

function record(value: unknown): Record<string, unknown> {
  return value as Record<string, unknown>;
}

function modifiedChange(
  entity: SemanticEntity,
  id: string,
  label: string | undefined,
  before: unknown,
  after: unknown,
  impacts: Record<string, SemanticImpact>,
  resolveImpact?: (key: string, before: unknown, after: unknown) => SemanticImpact,
): SemanticChange | undefined {
  const fields = changedFields(record(before), record(after), impacts, resolveImpact);
  if (fields.length === 0) return undefined;
  return {
    entity,
    id,
    label,
    operation: "modified",
    impact: highestImpact(fields.map((field) => field.impact)),
    fields,
  };
}

function diffByID<T extends { id: string }>(
  entity: "node" | "edge",
  before: T[],
  after: T[],
  impacts: Record<string, SemanticImpact>,
  labelOf: (value: T) => string | undefined,
  addedImpact: (value: T) => SemanticImpact = () => "behavioral",
  removedImpact: (value: T) => SemanticImpact = () => "breaking",
  resolveImpact?: (before: T, after: T) => (key: string, beforeValue: unknown, afterValue: unknown) => SemanticImpact,
): SemanticChange[] {
  const beforeByID = new Map(before.map((value) => [value.id, value]));
  const afterByID = new Map(after.map((value) => [value.id, value]));
  const ids = [...new Set([...beforeByID.keys(), ...afterByID.keys()])].sort();
  return ids.flatMap((id) => {
    const previous = beforeByID.get(id);
    const next = afterByID.get(id);
    if (!previous && next) {
      return [{
        entity,
        id,
        label: labelOf(next),
        operation: "added",
        impact: addedImpact(next),
        fields: [],
      } satisfies SemanticChange];
    }
    if (previous && !next) {
      return [{
        entity,
        id,
        label: labelOf(previous),
        operation: "removed",
        impact: removedImpact(previous),
        fields: [],
      } satisfies SemanticChange];
    }
    if (!previous || !next) return [];
    const change = modifiedChange(entity, id, labelOf(next), previous, next, impacts, resolveImpact?.(previous, next));
    return change ? [change] : [];
  });
}

function diffVariables(before: FlowVariable[], after: FlowVariable[]): SemanticChange[] {
  const beforeByPath = new Map(before.map((value) => [value.path, value]));
  const afterByPath = new Map(after.map((value) => [value.path, value]));
  const paths = [...new Set([...beforeByPath.keys(), ...afterByPath.keys()])].sort();
  return paths.flatMap((path) => {
    const previous = beforeByPath.get(path);
    const next = afterByPath.get(path);
    if (!previous && next) {
      return [{
        entity: "variable",
        id: path,
        label: path,
        operation: "added",
        impact: next.required ? "breaking" : "behavioral",
        fields: [],
      } satisfies SemanticChange];
    }
    if (previous && !next) {
      return [{
        entity: "variable",
        id: path,
        label: path,
        operation: "removed",
        impact: "breaking",
        fields: [],
      } satisfies SemanticChange];
    }
    if (!previous || !next) return [];
    const change = modifiedChange(
      "variable",
      path,
      path,
      previous,
      next,
      VARIABLE_IMPACT,
      (key, beforeValue, afterValue) => key === "required"
        ? beforeValue === false && afterValue === true ? "breaking" : "behavioral"
        : VARIABLE_IMPACT[key] ?? "behavioral",
    );
    return change ? [change] : [];
  });
}

function portChangeImpact(before: unknown, after: unknown): SemanticImpact {
  const beforeIds = new Set((before as FlowNode["inputs"]).map((port) => port.id));
  const afterIds = new Set((after as FlowNode["inputs"]).map((port) => port.id));
  if ([...beforeIds].some((id) => !afterIds.has(id))) return "breaking";
  if ([...afterIds].some((id) => !beforeIds.has(id))) return "behavioral";
  return "visual";
}

function nodeFieldImpact(before: FlowNode, after: FlowNode) {
  return (key: string, beforeValue: unknown, afterValue: unknown): SemanticImpact => {
    if (key === "inputs" || key === "outputs") return portChangeImpact(beforeValue, afterValue);
    if (key === "configuration") {
      if (before.type === "trigger" || after.type === "trigger") return "breaking";
      if (before.type === "group" && after.type === "group") return "visual";
    }
    return NODE_IMPACT[key] ?? "behavioral";
  };
}

/**
 * Compares stable flow identities instead of array positions. Pure canvas
 * changes stay visual, execution changes are behavioral, and contract or
 * connectivity changes are marked as breaking.
 */
export function diffFlowDefinitions(before: FlowDefinition, after: FlowDefinition): FlowSemanticDiff {
  const flowBefore = {
    schemaVersion: before.schemaVersion,
    name: before.name,
    description: before.description,
    metadata: before.metadata,
  };
  const flowAfter = {
    schemaVersion: after.schemaVersion,
    name: after.name,
    description: after.description,
    metadata: after.metadata,
  };
  const flowChange = modifiedChange("flow", "flow", after.name, flowBefore, flowAfter, FLOW_IMPACT);
  const layoutChange = modifiedChange("layout", "layout", after.layout.mode, before.layout, after.layout, LAYOUT_IMPACT);
  const changes = [
    ...(flowChange ? [flowChange] : []),
    ...(layoutChange ? [layoutChange] : []),
    ...diffVariables(before.variables, after.variables),
    ...diffByID<FlowNode>(
      "node",
      before.nodes,
      after.nodes,
      NODE_IMPACT,
      (node) => node.label,
      (node) => node.type === "group" ? "visual" : "behavioral",
      (node) => node.type === "group" ? "visual" : "breaking",
      nodeFieldImpact,
    ),
    ...diffByID<FlowEdge>("edge", before.edges, after.edges, EDGE_IMPACT, (edge) => edge.label || `${edge.source} → ${edge.target}`),
  ];
  const summary: SemanticDiffSummary = {
    added: changes.filter((change) => change.operation === "added").length,
    removed: changes.filter((change) => change.operation === "removed").length,
    modified: changes.filter((change) => change.operation === "modified").length,
    visual: changes.filter((change) => change.impact === "visual").length,
    behavioral: changes.filter((change) => change.impact === "behavioral").length,
    breaking: changes.filter((change) => change.impact === "breaking").length,
  };
  const impact = changes.length
    ? highestImpact(changes.map((change) => change.impact))
    : "none";
  return {
    hasChanges: changes.length > 0,
    behaviorChanged: summary.behavioral + summary.breaking > 0,
    highestImpact: impact,
    summary,
    changes,
  };
}
