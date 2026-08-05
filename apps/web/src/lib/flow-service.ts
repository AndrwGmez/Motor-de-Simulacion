import { ApiHttpError, apiBaseUrl, apiFetch, apiHeaders, csrfToken, hasConfiguredApi, httpError } from "./api-client";
import { DEMO_DOCUMENT } from "@flowverse/core";
import { createSimulationPlan } from "@flowverse/engine";
import { isFlowDefinition, parseImportedFlow } from "./flow-contract";
import { recordLocalFlowVersion } from "./version-service";
import type {
  EditableFlow,
  FlowDefinition,
  FlowVersion,
  RunEvent,
  RunSummary,
  SimulationOverrides,
  SimulationPlan,
} from "@flowverse/core";

const WS_URL = process.env.NEXT_PUBLIC_WS_URL?.replace(/\/$/, "");
const STORAGE_PREFIX = "flowverse:flow:";

function revisionFromEtag(etag: string | null | undefined, fallback = 1): number {
  const parsed = Number(etag?.replaceAll('"', "").replace("W/", ""));
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
}

function normalizedChecksum(value: string | null | undefined): string {
  return (value ?? "").replace(/^W\//, "").replace(/^"|"$/g, "");
}

function requireUuid(value: string, resource: string): void {
  if (!isUuid(value)) {
    throw new ApiHttpError(404, `${resource} no existe o su identificador no es válido.`);
  }
}

function terminalStatus(status: string): RunSummary["status"] | undefined {
  if (status === "completed" || status === "failed" || status === "cancelled") return status;
  if (status === "interrupted" || status === "limit_exceeded") return "failed";
  return undefined;
}

interface ApiRun {
  id?: string;
  versionId?: string;
  flowVersionId?: string;
  status?: string;
  createdAt?: string;
  completedAt?: string;
  finishedAt?: string;
  logicalTimeMs?: number;
  events?: Array<{ logicalTimeMs?: number }>;
  nodeRuns?: Array<{ nodeId?: string; status?: string; startedMs?: number; completedMs?: number }>;
}

function authenticatedRunSummary(value: ApiRun): RunSummary | undefined {
  const status = terminalStatus(value.status ?? "");
  if (!value.id || !status) return undefined;
  const events = Array.isArray(value.events) ? value.events : [];
  const nodeRuns = Array.isArray(value.nodeRuns) ? value.nodeRuns : [];
  const durationMs = value.logicalTimeMs
    ?? events.at(-1)?.logicalTimeMs
    ?? Math.max(0, ...nodeRuns.map((node) => node.completedMs ?? 0));
  return {
    id: value.id,
    flowVersionId: value.versionId ?? value.flowVersionId,
    createdAt: value.createdAt,
    completedAt: value.completedAt ?? value.finishedAt,
    status,
    durationMs,
    visitedNodeIds: nodeRuns
      .filter((node) => node.status !== "skipped" && typeof node.nodeId === "string")
      .map((node) => node.nodeId!),
    eventCount: events.length,
  };
}

interface PublicRun {
  id?: string;
  status?: string;
  path?: unknown;
  timings?: unknown;
}

function publicRunSummary(value: PublicRun, versionId: string): RunSummary | undefined {
  const status = terminalStatus(value.status ?? "");
  if (!value.id || !status) return undefined;
  const path = Array.isArray(value.path)
    ? value.path.filter((nodeId): nodeId is string => typeof nodeId === "string")
    : [];
  const timings = value.timings && typeof value.timings === "object"
    ? Object.values(value.timings as Record<string, unknown>)
      .filter((duration): duration is number => typeof duration === "number" && Number.isFinite(duration))
    : [];
  return {
    id: value.id,
    flowVersionId: versionId,
    status,
    durationMs: timings.reduce((total, duration) => total + Math.max(0, duration), 0),
    visitedNodeIds: path,
    eventCount: 0,
  };
}

export { parseImportedFlow } from "./flow-contract";

function isEditableFlow(value: unknown): value is EditableFlow {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<EditableFlow>;
  return (
    typeof candidate.flowId === "string"
    && typeof candidate.versionId === "string"
    && typeof candidate.etag === "string"
    && typeof candidate.draftMatchesPublished === "boolean"
    && isFlowDefinition(candidate.definition)
  );
}

export async function loadFlow(flowId: string): Promise<EditableFlow> {
  if (hasConfiguredApi) {
    requireUuid(flowId, "El flujo");
    const response = await apiFetch(`/v1/flows/${encodeURIComponent(flowId)}/draft`, {});
    if (!response.ok) throw await httpError(response, "No se pudo cargar el borrador.");
    const definition = parseImportedFlow(await response.json() as unknown);
    const etag = response.headers.get("ETag") ?? '"api-draft"';
    const [versionsResponse, runsResponse] = await Promise.all([
      apiFetch(`/v1/flows/${encodeURIComponent(flowId)}/versions`, {}),
      apiFetch(`/v1/flows/${encodeURIComponent(flowId)}/runs`, {}),
    ]);
    if (!versionsResponse.ok) throw await httpError(versionsResponse, "No se pudieron cargar las versiones.");
    if (!runsResponse.ok) throw await httpError(runsResponse, "No se pudo cargar el historial de ejecuciones.");
    const versionsPayload = await versionsResponse.json() as
      | PublishedVersion[]
      | { data?: PublishedVersion[]; items?: PublishedVersion[] };
    const versions = Array.isArray(versionsPayload)
      ? versionsPayload
      : Array.isArray(versionsPayload.data)
        ? versionsPayload.data
        : Array.isArray(versionsPayload.items)
          ? versionsPayload.items
          : [];
    const latestVersion = [...versions].sort((a, b) => b.number - a.number)[0];
    const runsPayload = await runsResponse.json() as
      | ApiRun[]
      | { data?: ApiRun[]; items?: ApiRun[] };
    const apiRuns = Array.isArray(runsPayload)
      ? runsPayload
      : Array.isArray(runsPayload.data)
        ? runsPayload.data
        : Array.isArray(runsPayload.items)
          ? runsPayload.items
          : [];
    return {
      flowId,
      versionId: `${flowId}-draft`,
      status: "draft",
      revision: revisionFromEtag(etag),
      etag,
      publishedVersionId: latestVersion?.id,
      publishedVersionNumber: latestVersion?.number,
      draftMatchesPublished: Boolean(
        latestVersion
        && normalizedChecksum(latestVersion.checksum) === normalizedChecksum(etag),
      ),
      updatedAt: new Date().toISOString(),
      definition,
      runHistory: apiRuns.flatMap((run) => {
        const summary = authenticatedRunSummary(run);
        return summary ? [summary] : [];
      }),
    };
  }
  const saved = globalThis.localStorage?.getItem(`${STORAGE_PREFIX}${flowId}`);
  if (saved) {
    try {
      const value = JSON.parse(saved) as unknown;
      if (isEditableFlow(value)) return value;
      return { ...structuredClone(DEMO_DOCUMENT), flowId, definition: parseImportedFlow(value) };
    } catch {
      globalThis.localStorage?.removeItem(`${STORAGE_PREFIX}${flowId}`);
    }
  }
  return flowId === DEMO_DOCUMENT.flowId
    ? structuredClone(DEMO_DOCUMENT)
    : {
        ...structuredClone(DEMO_DOCUMENT),
        flowId,
        versionId: `${flowId}-draft`,
      };
}

export async function saveFlow(
  document: EditableFlow,
  options: { keepalive?: boolean } = {},
): Promise<{ revision: number; etag: string; source: "api" | "local" }> {
  const nextRevision = document.revision + 1;
  const next = {
    ...document,
    revision: nextRevision,
    etag: `"demo-${nextRevision}"`,
    updatedAt: new Date().toISOString(),
  };
  if (hasConfiguredApi) {
    requireUuid(document.flowId, "El flujo");
    const csrf = csrfToken();
    const response = await apiFetch(`/v1/flows/${encodeURIComponent(document.flowId)}/draft`, {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        "If-Match": document.etag,
        ...(csrf ? { "X-CSRF-Token": csrf } : {}),
      },
      body: JSON.stringify(document.definition),
      keepalive: options.keepalive,
    });
    if (response.ok) {
      const etag = response.headers.get("ETag") ?? document.etag;
      return { revision: revisionFromEtag(etag, next.revision), etag, source: "api" };
    }
    if (response.status === 409 || response.status === 412) throw new Error("conflict");
    throw await httpError(response, "No se pudo guardar el borrador.");
  }
  globalThis.localStorage?.setItem(`${STORAGE_PREFIX}${document.flowId}`, JSON.stringify(next));
  return { revision: next.revision, etag: next.etag, source: "local" };
}

function localTextToFlow(text: string): FlowDefinition {
  const cleanLines = text
    .split(/\n|(?<=[.!?])\s+/)
    .map((line) => line.replace(/^\s*\d+[.)]\s*/, "").trim())
    .filter((line) => line.length > 2)
    .slice(0, 24);
  const steps = cleanLines.length > 0 ? cleanLines : ["Iniciar proceso", "Realizar tarea", "Finalizar proceso"];
  const nodes: FlowDefinition["nodes"] = steps.map((line, index) => {
    const lower = line.toLocaleLowerCase("es");
    const type = index === 0
      ? "trigger"
      : index === steps.length - 1
        ? "end"
        : lower.startsWith("si ") || lower.includes("¿")
          ? "decision"
          : "process";
    return {
      id: `ai-${index + 1}`,
      type,
      label: line.replace(/[.:]$/, "").slice(0, 72),
      description: "Nodo propuesto desde lenguaje natural.",
      inputs: type === "trigger" ? [] : [{ id: "input", label: "Entrada" }],
      outputs: type === "end" ? [] : [{ id: "output", label: "Salida" }],
      activationMode: "each",
      durationMs: type === "process" ? 350 : 0,
      configuration:
        type === "trigger"
          ? { eventName: "text.proposal" }
          : type === "decision"
            ? { strategy: "first_match" }
            : type === "end"
              ? { result: "success" }
              : { operations: [] },
      position: { x: (index - steps.length / 2) * 130, y: 0, z: 0 },
      locked: false,
      metadata: { category: "propuesta" },
    };
  });
  const edges = nodes.slice(0, -1).map((source, index) => ({
    id: `ai-edge-${index + 1}`,
    source: source.id,
    target: nodes[index + 1].id,
    sourcePort: "output",
    targetPort: "input",
    label: "",
    priority: 1,
    isDefault: false,
  }));
  return {
    schemaVersion: "1.0",
    name: "Flujo propuesto",
    description: "Borrador generado a partir de una descripción.",
    metadata: { tags: ["propuesta"], createdWith: "FlowVerse 3D local parser" },
    layout: {
      mode: "directional",
      camera: {
        position: { x: 0, y: 40, z: 520 },
        target: { x: 0, y: 0, z: 0 },
      },
    },
    variables: [],
    nodes,
    edges,
  };
}

export async function parseTextToFlow(text: string): Promise<{ flow: FlowDefinition; source: "api" | "local"; warnings: string[] }> {
  if (hasConfiguredApi) {
    const response = await apiFetch(`/v1/flows/parse-text`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(csrfToken() ? { "X-CSRF-Token": csrfToken()! } : {}),
      },
      body: JSON.stringify({ text }),
    });
    if (!response.ok) throw await httpError(response, "No se pudo interpretar el proceso.");
    const payload = await response.json() as {
      proposal: unknown;
      warnings?: string[];
      ambiguities?: Array<{ question: string; suggestedResolution: string }>;
      provider?: "mock" | "openai";
    };
    return {
      flow: parseImportedFlow(payload.proposal),
      source: "api",
      warnings: [
        ...(payload.warnings ?? []),
        ...(payload.ambiguities ?? []).map((item) => `${item.question} Sugerencia: ${item.suggestedResolution}`),
      ],
    };
  }
  return {
    flow: localTextToFlow(text),
    source: "local",
    warnings: ["Propuesta creada por el intérprete local. Revisa las decisiones antes de guardarla."],
  };
}

function isUuid(value: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
}

export { ApiHttpError } from "./api-client";

export type RunStartResult =
  | { source: "local"; plan: SimulationPlan }
  | { source: "api"; runId: string };

export async function startRun(
  document: EditableFlow,
  input: Record<string, unknown>,
  overrides: SimulationOverrides,
): Promise<RunStartResult> {
  const trigger = document.definition.nodes.find((node) => node.type === "trigger");
  if (!trigger) throw new Error("El flujo no contiene un nodo de inicio.");

  if (hasConfiguredApi) {
    if (!isUuid(document.versionId)) requireUuid(document.flowId, "El flujo");
    const apiOverrides = [
      ...overrides.failedNodeIds.map((nodeId) => ({
        type: "fail_node",
        nodeId,
        code: "TEST_FORCED_FAILURE",
        message: "Error forzado desde FlowVerse Web.",
      })),
      ...Object.values(overrides.forcedEdgeIds).map((edgeId) => ({ type: "force_edge", edgeId })),
    ];
    const runEndpoint = isUuid(document.versionId)
      ? `${apiBaseUrl}/v1/flow-versions/${encodeURIComponent(document.versionId)}/runs`
      : `${apiBaseUrl}/v1/flows/${encodeURIComponent(document.flowId)}/runs`;
    const response = await apiFetch(
      runEndpoint,
      {
        method: "POST",
        headers: apiHeaders({ "Idempotency-Key": crypto.randomUUID() }),
        body: JSON.stringify({
          triggerNodeId: trigger.id,
          input,
          overrides: apiOverrides,
        }),
      },
    );
    if (response.ok) {
      const payload = await response.json() as { id?: string; data?: { id?: string } };
      const runId = payload.id ?? payload.data?.id;
      if (runId && isUuid(runId)) return { source: "api", runId };
      throw new Error("La API no devolvió un identificador válido para la ejecución.");
    }
    if (response.status === 409 || response.status === 422) {
      throw await httpError(response, "La API rechazó la simulación.");
    }
    throw await httpError(response, "No se pudo crear la ejecución.");
  }

  return {
    source: "local",
    plan: createSimulationPlan(
      document.definition,
      input,
      overrides,
      trigger.id,
      document.versionId,
    ),
  };
}

export async function controlRun(
  runId: string,
  action: "pause" | "resume" | "step" | "cancel",
): Promise<void> {
  if (!hasConfiguredApi) return;
  requireUuid(runId, "La ejecución");
  const response = await apiFetch(`/v1/runs/${encodeURIComponent(runId)}/${action}`, {
    method: "POST",
    headers: apiHeaders(),
  });
  if (!response.ok) throw await httpError(response, `No se pudo ejecutar la acción ${action}.`);
}

export async function changeRunSpeed(runId: string, multiplier: number): Promise<void> {
  if (!hasConfiguredApi) return;
  requireUuid(runId, "La ejecución");
  const response = await apiFetch(`/v1/runs/${encodeURIComponent(runId)}/speed`, {
    method: "PATCH",
    headers: apiHeaders(),
    body: JSON.stringify({ multiplier }),
  });
  if (!response.ok) throw await httpError(response, "No se pudo cambiar la velocidad.");
}

export function connectRunEvents(
  runId: string,
  afterSequence: number,
  onEvent: (event: RunEvent) => void,
  onStatus: (status: "connected" | "reconnecting" | "closed") => void,
): () => void {
  let stopped = false;
  let socket: WebSocket | undefined;
  let retryTimer: number | undefined;
  let lastSequence = afterSequence;
  let attempts = 0;

  const connect = async () => {
    if (stopped || !hasConfiguredApi) return;
    try {
      const response = await apiFetch(`/v1/runs/${encodeURIComponent(runId)}/ws-ticket`, {
        method: "POST",
        headers: apiHeaders(),
      });
      if (!response.ok) throw new Error("ticket");
      const ticket = await response.json() as { ticket: string; url?: string };
      const base = ticket.url
        ?? (WS_URL ? `${WS_URL}/v1/runs/${encodeURIComponent(runId)}/live` : `${apiBaseUrl.replace(/^http/, "ws")}/v1/runs/${encodeURIComponent(runId)}/live`);
      const url = new URL(base, globalThis.location?.origin);
      if (!url.searchParams.has("ticket")) url.searchParams.set("ticket", ticket.ticket);
      url.searchParams.set("afterSequence", String(lastSequence));
      socket = new WebSocket(url);
      socket.onopen = () => {
        attempts = 0;
        onStatus("connected");
      };
      socket.onmessage = (message) => {
        try {
          const raw = JSON.parse(String(message.data)) as unknown;
          const envelope = raw && typeof raw === "object" ? raw as { data?: unknown } : undefined;
          const event = (envelope?.data ?? raw) as RunEvent;
          if (!event || typeof event.sequence !== "number") return;
          if (event.sequence <= lastSequence) return;
          lastSequence = event.sequence;
          onEvent(event);
        } catch {
          // Un mensaje mal formado se ignora; la secuencia persistida permite recuperarlo al reconectar.
        }
      };
      socket.onclose = () => {
        if (stopped) return;
        attempts += 1;
        if (attempts > 5) {
          onStatus("closed");
          return;
        }
        onStatus("reconnecting");
        retryTimer = window.setTimeout(connect, Math.min(5_000, 400 * 2 ** attempts));
      };
    } catch {
      if (!stopped) {
        attempts += 1;
        onStatus(attempts > 5 ? "closed" : "reconnecting");
        if (attempts <= 5) retryTimer = window.setTimeout(connect, Math.min(5_000, 400 * 2 ** attempts));
      }
    }
  };
  void connect();
  return () => {
    stopped = true;
    if (retryTimer) window.clearTimeout(retryTimer);
    socket?.close();
  };
}

export async function loadPublicShare(token: string): Promise<EditableFlow> {
  if (hasConfiguredApi) {
    const response = await apiFetch(`/public/v1/shares/${encodeURIComponent(token)}`);
    if (!response.ok) throw await httpError(response, "El enlace público no existe o ya no está activo.");
    const payload = await response.json() as { definition?: unknown; runs?: PublicRun[] };
    const versionId = `public-${token}`;
    return {
      flowId: `public-${token}`,
      versionId,
      status: "published",
      revision: 1,
      etag: '"public"',
      draftMatchesPublished: true,
      updatedAt: new Date().toISOString(),
      definition: parseImportedFlow(payload),
      runHistory: (Array.isArray(payload.runs) ? payload.runs : []).flatMap((run) => {
        const summary = publicRunSummary(run, versionId);
        return summary ? [summary] : [];
      }),
    };
  }
  return {
    ...structuredClone(DEMO_DOCUMENT),
    flowId: "public-demo-pedidos",
    versionId: "public-demo-pedidos",
    status: "published",
    draftMatchesPublished: true,
  };
}

export interface CreatedShare {
  id: string;
  url: string;
  source: "api" | "local";
}

export type PublishedVersion = FlowVersion;

export async function publishFlow(
  document: EditableFlow,
): Promise<{ version: PublishedVersion; etag: string }> {
  if (!hasConfiguredApi) {
    const number = (document.publishedVersionNumber ?? 0) + 1;
    const version: FlowVersion = {
      id: crypto.randomUUID(),
      flowId: document.flowId,
      number,
      checksum: document.etag.replace(/^W\//, "").replace(/^"|"$/g, ""),
      publishedAt: new Date().toISOString(),
      publishedBy: "demo-user",
    };
    recordLocalFlowVersion({ version, definition: document.definition });
    globalThis.localStorage?.setItem(`${STORAGE_PREFIX}${document.flowId}`, JSON.stringify({
      ...document,
      publishedVersionId: version.id,
      publishedVersionNumber: version.number,
      draftMatchesPublished: true,
    } satisfies EditableFlow));
    return {
      version,
      etag: document.etag,
    };
  }
  requireUuid(document.flowId, "El flujo");

  const response = await apiFetch(`/v1/flows/${encodeURIComponent(document.flowId)}/publish`, {
    method: "POST",
    headers: {
      ...(csrfToken() ? { "X-CSRF-Token": csrfToken()! } : {}),
      "If-Match": document.etag,
    },
  });
  if (!response.ok) {
    if (response.status === 412) throw new Error("El borrador cambió en otra pestaña. Recárgalo antes de publicar.");
    if (response.status === 422) throw await httpError(response, "El flujo contiene errores que impiden publicarlo.");
    throw await httpError(response, "No se pudo publicar esta versión.");
  }
  const payload = await response.json() as PublishedVersion | { data: PublishedVersion };
  const version = "data" in payload ? payload.data : payload;
  if (!isUuid(version.id)) throw new Error("La API no devolvió un identificador válido para la versión publicada.");
  return {
    version,
    etag: response.headers.get("ETag") ?? document.etag,
  };
}

export async function createShareLink(document: EditableFlow, runs: RunSummary[]): Promise<CreatedShare> {
  const publishedVersionId = document.publishedVersionId
    ?? (document.status === "published" && isUuid(document.versionId) ? document.versionId : undefined);
  if (hasConfiguredApi) {
    requireUuid(document.flowId, "El flujo");
    if (!publishedVersionId || !isUuid(publishedVersionId) || !document.draftMatchesPublished) {
      throw new Error("Publica el borrador actual antes de crear un enlace.");
    }
    const runIds = runs
      .filter((run) => (
        (run.status === "completed" || run.status === "failed")
        && run.flowVersionId === publishedVersionId
        && isUuid(run.id)
      ))
      .slice(0, 20)
      .map((run) => run.id);
    const response = await apiFetch(`/v1/flows/${encodeURIComponent(document.flowId)}/share-links`, {
      method: "POST",
      headers: apiHeaders(),
      body: JSON.stringify({ versionId: publishedVersionId, runIds }),
    });
    if (response.ok) {
      const payload = await response.json() as { id: string; token: string; publicUrl?: string };
      return {
        id: payload.id,
        url: `${globalThis.location.origin}/compartir/${encodeURIComponent(payload.token)}`,
        source: "api",
      };
    }
    throw await httpError(response, "No se pudo crear el enlace público.");
  }
  return {
    id: "demo-share",
    url: `${globalThis.location?.origin ?? ""}/compartir/demo-pedidos`,
    source: "local",
  };
}

export async function revokeShareLink(shareId: string): Promise<void> {
  if (!hasConfiguredApi || shareId === "demo-share") return;
  requireUuid(shareId, "El enlace");
  const response = await apiFetch(`/v1/share-links/${encodeURIComponent(shareId)}`, {
    method: "DELETE",
    headers: apiHeaders(),
  });
  if (!response.ok) throw await httpError(response, "No se pudo revocar el enlace público.");
}

export function downloadFlow(flow: FlowDefinition) {
  const blob = new Blob([JSON.stringify(flow, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${flow.name.toLocaleLowerCase("es").replace(/[^a-z0-9]+/gi, "-") || "flujo"}.json`;
  anchor.click();
  URL.revokeObjectURL(url);
}


/**
 * Análisis estructural del motor en Go.
 *
 * Ciclos, camino crítico, cuellos de botella y nodos inalcanzables se calculan
 * ahí desde el principio —36 ms sobre 482 nodos, 367 ms sobre 4.782— y la web
 * los ignoraba, recalculando en el navegador un subconjunto más pobre. Esto solo
 * los trae; el cálculo local sigue sirviendo al modo demo.
 */
export interface FlowAnalysis {
  nodeCount: number;
  edgeCount: number;
  maxDepth: number;
  complexity: number;
  /** Cada ciclo es la lista de nodos que lo forman. */
  cycles: string[][];
  pathCount: number;
  pathsTruncated: boolean;
  unreachableNodeIds: string[];
  criticalPathNodeIds: string[];
}

export async function analyzeFlow(flowId: string): Promise<FlowAnalysis | undefined> {
  if (!hasConfiguredApi) return undefined;
  requireUuid(flowId, "El flujo");
  const response = await apiFetch(`/v1/flows/${encodeURIComponent(flowId)}/analyze`, {
    method: "POST",
    body: "{}",
  });
  if (!response.ok) throw await httpError(response, "No se pudo analizar el flujo.");

  const bruto = await response.json() as Record<string, unknown>;
  const lista = (valor: unknown): string[] => Array.isArray(valor) ? valor.filter((v): v is string => typeof v === "string") : [];
  const numero = (valor: unknown, porDefecto = 0): number => typeof valor === "number" && Number.isFinite(valor) ? valor : porDefecto;
  const caminos = (bruto.paths ?? {}) as { count?: number; truncated?: boolean };

  return {
    nodeCount: numero(bruto.nodeCount),
    edgeCount: numero(bruto.edgeCount),
    maxDepth: numero(bruto.maxDepth),
    complexity: numero(bruto.complexity ?? bruto.cyclomaticComplexity),
    cycles: Array.isArray(bruto.cycles) ? (bruto.cycles as unknown[]).map(lista) : [],
    pathCount: numero(caminos.count ?? bruto.pathCount),
    pathsTruncated: Boolean(caminos.truncated ?? bruto.pathsTruncated),
    unreachableNodeIds: lista(bruto.unreachableNodeIds),
    criticalPathNodeIds: lista(bruto.staticCriticalPath ?? bruto.criticalPathNodeIds),
  };
}
