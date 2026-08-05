import { apiFetch, hasConfiguredApi, httpError } from "./api-client";

export type IncidentFrameCategory = "node" | "edge" | "control" | "run" | "system";
export type IncidentNodeState = "queued" | "running" | "waiting" | "completed" | "failed" | "skipped";

export interface IncidentTimelineFrame {
  sequence: number;
  occurredAt: string;
  logicalTimeMs: number;
  type: string;
  category: IncidentFrameCategory;
  nodeId?: string;
  edgeId?: string;
  message?: string;
  payload: Record<string, unknown>;
}

export interface IncidentRootCause {
  sequence: number;
  type: string;
  nodeId?: string;
  code?: string;
  message: string;
}

export interface IncidentIntegrity {
  complete: boolean;
  firstSequence: number;
  lastSequence: number;
  missingSequences: number[];
  duplicateSequences: number[];
}

export interface IncidentSummary {
  eventCount: number;
  logicalDurationMs: number;
  visitedNodeIds: string[];
  traversedEdgeIds: string[];
  failedNodeIds: string[];
}

export interface IncidentReport {
  schemaVersion: "1.0";
  runId: string;
  traceId?: string;
  flowId: string;
  flowVersionId?: string;
  definitionEtag?: string;
  status: string;
  createdAt: string;
  startedAt?: string;
  completedAt?: string;
  error?: string;
  summary: IncidentSummary;
  integrity: IncidentIntegrity;
  rootCause?: IncidentRootCause;
  timeline: IncidentTimelineFrame[];
}

export interface IncidentReplayState {
  sequence: number;
  logicalTimeMs: number;
  appliedEvents: number;
  runStatus: string;
  nodeStates: Record<string, IncidentNodeState>;
  nodeVisits: IncidentNodeVisitState[];
  traversedEdgeIds: string[];
}

export interface IncidentNodeVisitState {
  id: string;
  tokenId?: string;
  nodeId: string;
  visit: number;
  state: IncidentNodeState;
  lastSequence: number;
}

const CATEGORIES = new Set<IncidentFrameCategory>(["node", "edge", "control", "run", "system"]);
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const TRACE_PATTERN = /^[0-9a-f]{32}$/;

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function requiredString(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  if (typeof value !== "string" || value.length === 0) throw invalidReport();
  return value;
}

function optionalString(record: Record<string, unknown>, key: string): string | undefined {
  const value = record[key];
  if (value === undefined) return undefined;
  if (typeof value !== "string") throw invalidReport();
  return value;
}

function integer(record: Record<string, unknown>, key: string, minimum = 0): number {
  const value = record[key];
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < minimum) throw invalidReport();
  return value;
}

function stringList(record: Record<string, unknown>, key: string): string[] {
  const value = record[key];
  if (!Array.isArray(value) || value.some((entry) => typeof entry !== "string")) throw invalidReport();
  return [...value] as string[];
}

function integerList(record: Record<string, unknown>, key: string): number[] {
  const value = record[key];
  if (!Array.isArray(value) || value.some((entry) => typeof entry !== "number" || !Number.isSafeInteger(entry) || entry < 1)) {
    throw invalidReport();
  }
  return [...value] as number[];
}

function timestamp(record: Record<string, unknown>, key: string, optional = false): string | undefined {
  const value = optional ? optionalString(record, key) : requiredString(record, key);
  if (value !== undefined && !Number.isFinite(Date.parse(value))) throw invalidReport();
  return value;
}

function invalidReport(): Error {
  return new Error("La API devolvió un informe de incidente inválido.");
}

function parseFrame(value: unknown): IncidentTimelineFrame {
  const frame = asRecord(value);
  if (!frame) throw invalidReport();
  const category = requiredString(frame, "category") as IncidentFrameCategory;
  if (!CATEGORIES.has(category)) throw invalidReport();
  const payload = frame.payload === undefined ? {} : asRecord(frame.payload);
  if (!payload) throw invalidReport();
  return {
    sequence: integer(frame, "sequence", 1),
    occurredAt: timestamp(frame, "occurredAt")!,
    logicalTimeMs: integer(frame, "logicalTimeMs"),
    type: requiredString(frame, "type"),
    category,
    nodeId: optionalString(frame, "nodeId"),
    edgeId: optionalString(frame, "edgeId"),
    message: optionalString(frame, "message"),
    payload: { ...payload },
  };
}

function parseSummary(value: unknown): IncidentSummary {
  const summary = asRecord(value);
  if (!summary) throw invalidReport();
  return {
    eventCount: integer(summary, "eventCount"),
    logicalDurationMs: integer(summary, "logicalDurationMs"),
    visitedNodeIds: stringList(summary, "visitedNodeIds"),
    traversedEdgeIds: stringList(summary, "traversedEdgeIds"),
    failedNodeIds: stringList(summary, "failedNodeIds"),
  };
}

function parseIntegrity(value: unknown): IncidentIntegrity {
  const integrity = asRecord(value);
  if (!integrity || typeof integrity.complete !== "boolean") throw invalidReport();
  return {
    complete: integrity.complete,
    firstSequence: integer(integrity, "firstSequence"),
    lastSequence: integer(integrity, "lastSequence"),
    missingSequences: integerList(integrity, "missingSequences"),
    duplicateSequences: integerList(integrity, "duplicateSequences"),
  };
}

function parseRootCause(value: unknown): IncidentRootCause | undefined {
  if (value === undefined) return undefined;
  const rootCause = asRecord(value);
  if (!rootCause) throw invalidReport();
  return {
    sequence: integer(rootCause, "sequence"),
    type: requiredString(rootCause, "type"),
    nodeId: optionalString(rootCause, "nodeId"),
    code: optionalString(rootCause, "code"),
    message: requiredString(rootCause, "message"),
  };
}

/** Valida y normaliza el informe. El orden no depende del orden de llegada de la API. */
export function parseIncidentReport(value: unknown): IncidentReport {
  const outer = asRecord(value);
  const report = asRecord(outer?.data) ?? outer;
  if (!report || report.schemaVersion !== "1.0") throw invalidReport();
  const runId = requiredString(report, "runId");
  const flowId = requiredString(report, "flowId");
  const flowVersionId = optionalString(report, "flowVersionId");
  const traceId = optionalString(report, "traceId");
  if (!UUID_PATTERN.test(runId) || !UUID_PATTERN.test(flowId) || (flowVersionId && !UUID_PATTERN.test(flowVersionId))) {
    throw invalidReport();
  }
  if (traceId && !TRACE_PATTERN.test(traceId)) throw invalidReport();
  if (!Array.isArray(report.timeline)) throw invalidReport();
  const timeline = report.timeline
    .map((entry, index) => ({ frame: parseFrame(entry), index }))
    .sort((left, right) => (
      left.frame.sequence - right.frame.sequence
      || Date.parse(left.frame.occurredAt) - Date.parse(right.frame.occurredAt)
      || left.index - right.index
    ))
    .map(({ frame }) => frame);
  return {
    schemaVersion: "1.0",
    runId,
    traceId,
    flowId,
    flowVersionId,
    definitionEtag: optionalString(report, "definitionEtag"),
    status: requiredString(report, "status"),
    createdAt: timestamp(report, "createdAt")!,
    startedAt: timestamp(report, "startedAt", true),
    completedAt: timestamp(report, "completedAt", true),
    error: optionalString(report, "error"),
    summary: parseSummary(report.summary),
    integrity: parseIntegrity(report.integrity),
    rootCause: parseRootCause(report.rootCause),
    timeline,
  };
}

export async function getRunIncident(runId: string): Promise<IncidentReport> {
  if (!hasConfiguredApi) throw new Error("Incident Time Machine requiere una API configurada.");
  if (!UUID_PATTERN.test(runId)) throw new Error("La ejecución no existe o su identificador no es válido.");
  const response = await apiFetch(`/v1/runs/${encodeURIComponent(runId)}/incident`);
  if (!response.ok) throw await httpError(response, "No se pudo reconstruir el incidente.");
  const report = parseIncidentReport(await response.json());
  if (report.runId !== runId) throw invalidReport();
  return report;
}

/** Aplica los deltas de la línea temporal hasta la secuencia elegida, incluida. */
export function reconstructIncidentState(
  timeline: IncidentTimelineFrame[],
  selectedSequence: number,
  selectedFrameIndex?: number,
): IncidentReplayState {
  const state: IncidentReplayState = {
    sequence: 0,
    logicalTimeMs: 0,
    appliedEvents: 0,
    runStatus: "created",
    nodeStates: {},
    nodeVisits: [],
    traversedEdgeIds: [],
  };
  const activeVisits = new Map<string, number>();
  const visitCounts = new Map<string, number>();
  for (const [frameIndex, frame] of timeline.entries()) {
    if (selectedFrameIndex !== undefined ? frameIndex > selectedFrameIndex : frame.sequence > selectedSequence) break;
    state.sequence = frame.sequence;
    state.logicalTimeMs = Math.max(state.logicalTimeMs, frame.logicalTimeMs);
    state.appliedEvents += 1;
    if (frame.nodeId) {
      const nodeState = nodeStateFromEvent(frame.type);
      if (nodeState) {
        state.nodeStates[frame.nodeId] = nodeState;
        const tokenId = typeof frame.payload.tokenId === "string" && frame.payload.tokenId
          ? frame.payload.tokenId
          : undefined;
        const correlation = `${tokenId ?? "legacy"}\u0000${frame.nodeId}`;
        let visitIndex = activeVisits.get(correlation);
        if (frame.type === "node.queued" || visitIndex === undefined) {
          const visit = (visitCounts.get(correlation) ?? 0) + 1;
          visitCounts.set(correlation, visit);
          visitIndex = state.nodeVisits.length;
          activeVisits.set(correlation, visitIndex);
          state.nodeVisits.push({
            id: `${correlation}\u0000${visit}`,
            tokenId,
            nodeId: frame.nodeId,
            visit,
            state: nodeState,
            lastSequence: frame.sequence,
          });
        } else {
          state.nodeVisits[visitIndex] = {
            ...state.nodeVisits[visitIndex]!,
            state: nodeState,
            lastSequence: frame.sequence,
          };
        }
        if (nodeState === "completed" || nodeState === "failed" || nodeState === "skipped") {
          activeVisits.delete(correlation);
        }
      }
    }
    if (frame.type === "edge.traversed" && frame.edgeId) {
      state.traversedEdgeIds.push(frame.edgeId);
    }
    state.runStatus = runStateFromEvent(frame.type, state.runStatus);
  }
  return state;
}

function nodeStateFromEvent(type: string): IncidentNodeState | undefined {
  const states: Record<string, IncidentNodeState> = {
    "node.queued": "queued",
    "node.started": "running",
    "node.waiting": "waiting",
    "node.completed": "completed",
    "node.failed": "failed",
    "node.skipped": "skipped",
  };
  return states[type];
}

function runStateFromEvent(type: string, current: string): string {
  const states: Record<string, string> = {
    "run.started": "running",
    "run.paused": "paused",
    "run.resumed": "running",
    "run.completed": "completed",
    "run.failed": "failed",
    "run.limit_exceeded": "failed",
    "run.interrupted": "failed",
    "run.cancelled": "cancelled",
  };
  return states[type] ?? current;
}
