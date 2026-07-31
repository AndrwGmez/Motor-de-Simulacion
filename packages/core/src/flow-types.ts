export const NODE_TYPES = [
  "trigger",
  "process",
  "decision",
  "data",
  "integration",
  "delay",
  "end",
  "group",
] as const;

export type NodeType = (typeof NODE_TYPES)[number];
export type LayoutMode = "force" | "directional" | "layers" | "timeline" | "clusters" | "execution";
export type NodeRunStatus = "idle" | "queued" | "running" | "success" | "failed" | "skipped" | "waiting";
export type RunStatus = "idle" | "running" | "paused" | "completed" | "failed" | "cancelled";
export type Severity = "info" | "warning" | "error";

export interface Point3D {
  x: number;
  y: number;
  z: number;
}

export interface FlowPort {
  id: string;
  label: string;
}

export type ConditionOperator =
  | "equals"
  | "not_equals"
  | "greater_than"
  | "greater_than_or_equal"
  | "less_than"
  | "less_than_or_equal"
  | "contains"
  | "not_contains"
  | "exists"
  | "not_exists";

export interface ComparisonCondition {
  field: string;
  operator: ConditionOperator;
  value?: unknown;
}

export type FlowCondition =
  | ComparisonCondition
  | { operator: "and" | "or"; conditions: FlowCondition[] };

export interface FlowNode {
  id: string;
  type: NodeType;
  label: string;
  description: string;
  inputs: FlowPort[];
  outputs: FlowPort[];
  activationMode: "each" | "any" | "all";
  durationMs: number;
  configuration: Record<string, unknown>;
  position: Point3D;
  locked: boolean;
  metadata: {
    category?: string;
    color?: string;
    groupId?: string;
  };
}

export interface FlowEdge {
  id: string;
  source: string;
  target: string;
  sourcePort: string;
  targetPort: string;
  label: string;
  condition?: FlowCondition;
  isDefault: boolean;
  priority: number;
}

export interface FlowVariable {
  path: string;
  type: "string" | "number" | "integer" | "boolean" | "object" | "array" | "null";
  required: boolean;
  description?: string;
  default?: unknown;
}

export interface FlowDefinition {
  schemaVersion: "1.0";
  name: string;
  description: string;
  metadata?: {
    tags?: string[];
    createdWith?: string;
  };
  layout: {
    mode: LayoutMode;
    clusterBy?: "category" | "type" | "group";
    camera?: {
      position: Point3D;
      target: Point3D;
    };
  };
  variables: FlowVariable[];
  nodes: FlowNode[];
  edges: FlowEdge[];
}

export interface EditableFlow {
  flowId: string;
  versionId: string;
  status: "draft" | "published";
  revision: number;
  etag: string;
  publishedVersionId?: string;
  publishedVersionNumber?: number;
  draftMatchesPublished: boolean;
  updatedAt: string;
  definition: FlowDefinition;
  runHistory?: RunSummary[];
}

export interface ValidationIssue {
  id: string;
  code: string;
  severity: Severity;
  message: string;
  nodeId?: string;
  edgeId?: string;
}

export type RunEventType =
  | "run.started"
  | "node.queued"
  | "node.started"
  | "node.waiting"
  | "edge.traversed"
  | "node.completed"
  | "node.failed"
  | "node.skipped"
  | "run.paused"
  | "run.resumed"
  | "run.completed"
  | "run.failed"
  | "run.limit_exceeded"
  | "run.cancelled"
  | "run.interrupted";

export interface RunEvent {
  schemaVersion: "1.0";
  runId: string;
  sequence: number;
  occurredAt: string;
  logicalTimeMs: number;
  type: RunEventType;
  payload: {
    nodeId?: string;
    edgeId?: string;
    error?: string;
    output?: unknown;
  };
}

export interface RunSummary {
  id: string;
  flowVersionId?: string;
  createdAt?: string;
  completedAt?: string;
  status: Exclude<RunStatus, "idle" | "running" | "paused">;
  durationMs: number;
  visitedNodeIds: string[];
  eventCount: number;
}

export interface SimulationOverrides {
  failedNodeIds: string[];
  forcedEdgeIds: Record<string, string>;
}

export interface SimulationPlan {
  runId: string;
  events: RunEvent[];
  summary: RunSummary;
}

export const NODE_PRESENTATION: Record<
  NodeType,
  { label: string; shortLabel: string; color: string; icon: string; description: string }
> = {
  trigger: { label: "Inicio", shortLabel: "Inicio", color: "#7f8cff", icon: "◉", description: "Punto de entrada del flujo" },
  process: { label: "Proceso", shortLabel: "Tarea", color: "#4d8dff", icon: "□", description: "Tarea o transformación" },
  decision: { label: "Decisión", shortLabel: "Decisión", color: "#b078ff", icon: "◇", description: "Divide el flujo por condiciones" },
  data: { label: "Datos", shortLabel: "Datos", color: "#36c6d8", icon: "▱", description: "Lee o modifica información" },
  integration: { label: "Integración", shortLabel: "Servicio", color: "#f49a59", icon: "⬡", description: "Sistema externo simulado" },
  delay: { label: "Espera", shortLabel: "Espera", color: "#f2c15d", icon: "◌", description: "Avanza el tiempo lógico" },
  end: { label: "Resultado", shortLabel: "Fin", color: "#35d39d", icon: "●", description: "Finaliza una ruta" },
  group: { label: "Grupo", shortLabel: "Grupo", color: "#8993ad", icon: "▧", description: "Contenedor visual" },
};

export function createNode(type: NodeType, index: number, position?: Partial<Point3D>): FlowNode {
  const presentation = NODE_PRESENTATION[type];
  const isTrigger = type === "trigger";
  const isEnd = type === "end";
  const isGroup = type === "group";
  const id = `${type}-${Date.now().toString(36)}-${index}`;

  const configuration: Record<string, unknown> =
    type === "trigger"
      ? { eventName: "manual" }
      : type === "decision"
        ? { strategy: "first_match" }
        : type === "data"
          ? { operations: [{ op: "set", path: "/data/value", value: null }] }
          : type === "integration"
            ? { service: "Servicio simulado", latencyMs: 350, outcome: "success" }
            : type === "delay"
              ? { delayMs: 1_000 }
              : type === "end"
                ? { result: "success" }
                : type === "group"
                  ? { collapsed: false }
                  : { operations: [] };

  return {
    id,
    type,
    label: `${presentation.label} ${index}`,
    description: presentation.description,
    inputs: isTrigger || isGroup ? [] : [{ id: "input", label: "Entrada" }],
    outputs: isEnd || isGroup ? [] : [{ id: "output", label: "Salida" }],
    activationMode: "each",
    durationMs: type === "delay" ? 1_000 : isTrigger || isEnd || isGroup || type === "decision" ? 0 : 350,
    configuration,
    position: {
      x: position?.x ?? index * 42,
      y: position?.y ?? 0,
      z: position?.z ?? 0,
    },
    locked: false,
    metadata: { category: presentation.label.toLowerCase(), color: presentation.color },
  };
}
