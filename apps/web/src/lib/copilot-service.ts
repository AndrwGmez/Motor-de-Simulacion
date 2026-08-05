import { apiFetch, hasConfiguredApi, httpError } from "./api-client";

export type CopilotProvider = "mock" | "openai";
export type CopilotSeverity = "info" | "warning" | "critical";
export type CopilotConfidence = "low" | "medium" | "high";
export type CopilotActionKind = "inspect_node" | "inspect_edge" | "open_incident" | "none";
export type CopilotEvidenceKind = "flow" | "analysis" | "validation" | "node" | "edge" | "diff" | "incident" | "event";

export interface EvidenceCopilotRequest {
  question: string;
  baseVersionId?: string;
  runId?: string;
}

export interface CopilotAction {
  kind: CopilotActionKind;
  targetId: string | null;
  label: string;
}

export interface CopilotSuggestion {
  title: string;
  explanation: string;
  severity: CopilotSeverity;
  confidence: CopilotConfidence;
  evidenceIds: string[];
  actions: CopilotAction[];
}

export interface CopilotEvidenceItem {
  id: string;
  kind: CopilotEvidenceKind;
  summary: string;
  nodeId?: string;
  edgeId?: string;
  facts: Record<string, unknown>;
}

export interface CopilotEvidenceBundle {
  schemaVersion: "1.0";
  flowId: string;
  items: CopilotEvidenceItem[];
  truncated: boolean;
}

export interface EvidenceCopilotResponse {
  schemaVersion: "1.0";
  provider: CopilotProvider;
  summary: string;
  suggestions: CopilotSuggestion[];
  limitations: string[];
  evidence: CopilotEvidenceBundle;
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const PROVIDERS = new Set<CopilotProvider>(["mock", "openai"]);
const SEVERITIES = new Set<CopilotSeverity>(["info", "warning", "critical"]);
const CONFIDENCES = new Set<CopilotConfidence>(["low", "medium", "high"]);
const ACTION_KINDS = new Set<CopilotActionKind>(["inspect_node", "inspect_edge", "open_incident", "none"]);
const EVIDENCE_KINDS = new Set<CopilotEvidenceKind>(["flow", "analysis", "validation", "node", "edge", "diff", "incident", "event"]);

export const isEvidenceCopilotAvailable = hasConfiguredApi;

function invalidResponse(): Error {
  return new Error("La API devolvió una respuesta inválida del Copiloto con Evidencia.");
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function requiredString(record: Record<string, unknown>, key: string, nonEmpty = false): string {
  const value = record[key];
  if (typeof value !== "string" || (nonEmpty && value.trim().length === 0)) throw invalidResponse();
  return value;
}

function optionalString(record: Record<string, unknown>, key: string): string | undefined {
  const value = record[key];
  if (value === undefined) return undefined;
  if (typeof value !== "string") throw invalidResponse();
  return value;
}

function stringList(record: Record<string, unknown>, key: string, requireItems = false): string[] {
  const value = record[key];
  if (!Array.isArray(value) || (requireItems && value.length === 0)) throw invalidResponse();
  if (value.some((entry) => typeof entry !== "string" || (requireItems && entry.trim().length === 0))) {
    throw invalidResponse();
  }
  return [...value] as string[];
}

function parseAction(value: unknown): CopilotAction {
  const action = asRecord(value);
  if (!action) throw invalidResponse();
  const kind = requiredString(action, "kind", true) as CopilotActionKind;
  if (!ACTION_KINDS.has(kind)) throw invalidResponse();
  const targetId = action.targetId;
  if (targetId !== null && typeof targetId !== "string") throw invalidResponse();
  if (kind === "none" ? targetId !== null : typeof targetId !== "string" || targetId.length === 0) {
    throw invalidResponse();
  }
  return {
    kind,
    targetId,
    label: requiredString(action, "label", true),
  };
}

function parseSuggestion(value: unknown): CopilotSuggestion {
  const suggestion = asRecord(value);
  if (!suggestion) throw invalidResponse();
  const severity = requiredString(suggestion, "severity", true) as CopilotSeverity;
  const confidence = requiredString(suggestion, "confidence", true) as CopilotConfidence;
  if (!SEVERITIES.has(severity) || !CONFIDENCES.has(confidence)) throw invalidResponse();
  const evidenceIds = stringList(suggestion, "evidenceIds", true);
  if (new Set(evidenceIds).size !== evidenceIds.length || !Array.isArray(suggestion.actions)) {
    throw invalidResponse();
  }
  return {
    title: requiredString(suggestion, "title", true),
    explanation: requiredString(suggestion, "explanation", true),
    severity,
    confidence,
    evidenceIds,
    actions: suggestion.actions.map(parseAction),
  };
}

function parseEvidenceItem(value: unknown): CopilotEvidenceItem {
  const item = asRecord(value);
  const facts = asRecord(item?.facts);
  if (!item || !facts) throw invalidResponse();
  const kind = requiredString(item, "kind", true) as CopilotEvidenceKind;
  if (!EVIDENCE_KINDS.has(kind)) throw invalidResponse();
  return {
    id: requiredString(item, "id", true),
    kind,
    summary: requiredString(item, "summary"),
    nodeId: optionalString(item, "nodeId"),
    edgeId: optionalString(item, "edgeId"),
    facts: structuredClone(facts),
  };
}

function actionIsGrounded(action: CopilotAction, evidence: Map<string, CopilotEvidenceItem>): boolean {
  if (action.kind === "none") return action.targetId === null;
  if (!action.targetId) return false;
  if (action.kind === "inspect_node") return evidence.has(`node:${action.targetId}`);
  if (action.kind === "inspect_edge") return evidence.has(`edge:${action.targetId}`);
  const incident = evidence.get("incident:summary");
  return incident?.facts.runId === action.targetId;
}

/** Valida tanto el JSON Schema como la resolución local de citas y acciones. */
export function parseEvidenceCopilotResponse(value: unknown): EvidenceCopilotResponse {
  const outer = asRecord(value);
  const payload = asRecord(outer?.data) ?? outer;
  if (!payload || payload.schemaVersion !== "1.0") throw invalidResponse();
  const provider = requiredString(payload, "provider", true) as CopilotProvider;
  if (!PROVIDERS.has(provider) || !Array.isArray(payload.suggestions) || !Array.isArray(payload.limitations)) {
    throw invalidResponse();
  }
  const rawEvidence = asRecord(payload.evidence);
  if (!rawEvidence || rawEvidence.schemaVersion !== "1.0" || typeof rawEvidence.truncated !== "boolean" || !Array.isArray(rawEvidence.items)) {
    throw invalidResponse();
  }
  const flowId = requiredString(rawEvidence, "flowId", true);
  if (!UUID_PATTERN.test(flowId)) throw invalidResponse();
  const items = rawEvidence.items.map(parseEvidenceItem);
  const evidenceById = new Map(items.map((item) => [item.id, item]));
  if (evidenceById.size !== items.length) throw invalidResponse();
  const suggestions = payload.suggestions.map(parseSuggestion);
  if (suggestions.some((suggestion) => (
    suggestion.evidenceIds.some((id) => !evidenceById.has(id))
    || suggestion.actions.some((action) => !actionIsGrounded(action, evidenceById))
  ))) {
    throw invalidResponse();
  }
  const limitations = stringList(payload, "limitations");
  return {
    schemaVersion: "1.0",
    provider,
    summary: requiredString(payload, "summary"),
    suggestions,
    limitations,
    evidence: {
      schemaVersion: "1.0",
      flowId,
      items,
      truncated: rawEvidence.truncated,
    },
  };
}

function optionalUuid(value: string | undefined, label: string): string | undefined {
  const normalized = value?.trim();
  if (!normalized) return undefined;
  if (!UUID_PATTERN.test(normalized)) throw new Error(`${label} no tiene un identificador válido.`);
  return normalized;
}

export async function askEvidenceCopilot(
  flowId: string,
  request: EvidenceCopilotRequest,
): Promise<EvidenceCopilotResponse> {
  if (!hasConfiguredApi) throw new Error("El Copiloto con Evidencia requiere una API configurada.");
  if (!UUID_PATTERN.test(flowId)) throw new Error("El flujo no existe o su identificador no es válido.");
  const question = request.question.trim();
  const questionLength = Array.from(question).length;
  if (questionLength < 3 || questionLength > 4_000) {
    throw new Error("La pregunta debe contener entre 3 y 4.000 caracteres.");
  }
  const baseVersionId = optionalUuid(request.baseVersionId, "La versión base");
  const runId = optionalUuid(request.runId, "La ejecución");
  const response = await apiFetch(`/v1/flows/${encodeURIComponent(flowId)}/copilot`, {
    method: "POST",
    body: JSON.stringify({
      question,
      ...(baseVersionId ? { baseVersionId } : {}),
      ...(runId ? { runId } : {}),
    }),
  });
  if (!response.ok) throw await httpError(response, "El Copiloto no pudo analizar este flujo.");
  const result = parseEvidenceCopilotResponse(await response.json());
  if (result.evidence.flowId !== flowId) throw invalidResponse();
  return result;
}
