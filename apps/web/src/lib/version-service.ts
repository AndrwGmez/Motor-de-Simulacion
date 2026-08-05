import type {
  EditableFlow,
  FlowDefinition,
  FlowVersion,
  FlowVersionSnapshot,
} from "@flowverse/core";
import { apiFetch, apiHeaders, hasConfiguredApi, httpError } from "./api-client";
import { parseImportedFlow } from "./flow-contract";

const VERSION_STORAGE_PREFIX = "flowverse:flow-versions:";
const FLOW_STORAGE_PREFIX = "flowverse:flow:";

export interface RestoredFlowVersion extends FlowVersionSnapshot {
  revision: number;
  etag: string;
  source: "api" | "local";
}

function isUuid(value: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
}

function revisionFromEtag(etag: string | null | undefined, fallback: number): number {
  const parsed = Number(etag?.replaceAll('"', "").replace("W/", ""));
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" ? value as Record<string, unknown> : undefined;
}

function parseVersion(value: unknown): FlowVersion {
  const version = asRecord(value);
  if (
    !version
    || typeof version.id !== "string"
    || typeof version.flowId !== "string"
    || typeof version.number !== "number"
    || !Number.isInteger(version.number)
    || version.number < 1
    || typeof version.checksum !== "string"
    || typeof version.publishedAt !== "string"
    || typeof version.publishedBy !== "string"
  ) {
    throw new Error("La API devolvió metadatos inválidos para una versión.");
  }
  return version as unknown as FlowVersion;
}

function versionItems(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  const envelope = asRecord(value);
  if (!envelope) return [];
  if (Array.isArray(envelope.items)) return envelope.items;
  if (Array.isArray(envelope.data)) return envelope.data;
  return versionItems(envelope.data);
}

function storedVersions(flowId: string): FlowVersionSnapshot[] {
  try {
    const value = JSON.parse(globalThis.localStorage?.getItem(`${VERSION_STORAGE_PREFIX}${flowId}`) ?? "[]") as unknown;
    if (!Array.isArray(value)) return [];
    return value.flatMap((item) => {
      try {
        const envelope = asRecord(item);
        if (!envelope) return [];
        const version = parseVersion(envelope.version);
        if (version.flowId !== flowId) return [];
        return [{ version, definition: parseImportedFlow(envelope.definition) }];
      } catch {
        return [];
      }
    });
  } catch {
    return [];
  }
}

/** Guarda el snapshot local al mismo tiempo que se publica en modo demo. */
export function recordLocalFlowVersion(snapshot: FlowVersionSnapshot): void {
  const current = storedVersions(snapshot.version.flowId)
    .filter((candidate) => candidate.version.id !== snapshot.version.id);
  const versions = [structuredClone(snapshot), ...current]
    .sort((left, right) => right.version.number - left.version.number);
  globalThis.localStorage?.setItem(
    `${VERSION_STORAGE_PREFIX}${snapshot.version.flowId}`,
    JSON.stringify(versions),
  );
}

export async function listFlowVersions(flowId: string): Promise<FlowVersion[]> {
  if (!hasConfiguredApi) {
    return storedVersions(flowId)
      .map((snapshot) => snapshot.version)
      .sort((left, right) => right.number - left.number);
  }
  if (!isUuid(flowId)) throw new Error("El flujo no existe o su identificador no es válido.");
  const response = await apiFetch(`/v1/flows/${encodeURIComponent(flowId)}/versions`);
  if (!response.ok) throw await httpError(response, "No se pudo cargar el historial de versiones.");
  const versions = versionItems(await response.json()).map(parseVersion);
  return versions.sort((left, right) => right.number - left.number);
}

export async function getFlowVersion(versionId: string, flowId?: string): Promise<FlowVersionSnapshot> {
  if (!hasConfiguredApi) {
    const snapshot = flowId
      ? storedVersions(flowId).find((candidate) => candidate.version.id === versionId)
      : undefined;
    if (!snapshot) throw new Error("La versión local ya no está disponible.");
    return structuredClone(snapshot);
  }
  if (!isUuid(versionId)) throw new Error("La versión no existe o su identificador no es válido.");
  const response = await apiFetch(`/v1/flow-versions/${encodeURIComponent(versionId)}`);
  if (!response.ok) throw await httpError(response, "No se pudo cargar la versión.");
  const raw = await response.json() as unknown;
  const envelope = asRecord(raw);
  const payload = asRecord(envelope?.data) ?? envelope;
  if (!payload) throw new Error("La API devolvió una versión vacía.");
  return {
    version: parseVersion(payload.version),
    definition: parseImportedFlow(payload.definition),
  };
}

/**
 * Restaura una publicación como un borrador nuevo. Las versiones publicadas
 * nunca se modifican: la API hace una copia protegida por el ETag vigente.
 */
export async function restoreFlowVersion(
  document: EditableFlow,
  snapshot: FlowVersionSnapshot,
): Promise<RestoredFlowVersion> {
  if (!hasConfiguredApi) {
    const revision = document.revision + 1;
    const etag = `"demo-${revision}"`;
    const definition = structuredClone(snapshot.definition);
    const restored: EditableFlow = {
      ...document,
      revision,
      etag,
      draftMatchesPublished: snapshot.version.id === document.publishedVersionId,
      updatedAt: new Date().toISOString(),
      definition,
    };
    globalThis.localStorage?.setItem(`${FLOW_STORAGE_PREFIX}${document.flowId}`, JSON.stringify(restored));
    return { ...structuredClone(snapshot), definition, revision, etag, source: "local" };
  }

  if (!isUuid(document.flowId) || !isUuid(snapshot.version.id)) {
    throw new Error("El flujo o la versión no tienen un identificador válido.");
  }
  const response = await apiFetch(`/v1/flows/${encodeURIComponent(document.flowId)}/draft/restore`, {
    method: "POST",
    headers: apiHeaders({ "If-Match": document.etag }),
    body: JSON.stringify({ versionId: snapshot.version.id }),
  });
  if (response.status === 409 || response.status === 412) {
    throw new Error("conflict");
  }
  if (!response.ok) throw await httpError(response, "No se pudo restaurar la versión.");

  const raw = await response.json() as unknown;
  const envelope = asRecord(raw);
  const payload = asRecord(envelope?.data) ?? envelope;
  const draft = asRecord(payload?.draft);
  const definitionCandidate = payload?.definition ?? draft?.definition ?? payload?.draft ?? raw;
  const definition: FlowDefinition = parseImportedFlow(definitionCandidate);
  const etag = response.headers.get("ETag")
    ?? (typeof payload?.etag === "string" ? payload.etag : document.etag);
  const headerRevision = Number(response.headers.get("X-Draft-Revision"));
  const revision = typeof payload?.revision === "number"
    ? payload.revision
    : Number.isInteger(headerRevision) && headerRevision >= 0
      ? headerRevision
      : revisionFromEtag(etag, document.revision + 1);
  const versionCandidate = payload?.restoredFromVersion ?? payload?.version;
  const responseVersion = versionCandidate ? parseVersion(versionCandidate) : snapshot.version;
  return {
    version: responseVersion,
    definition,
    revision,
    etag,
    source: "api",
  };
}
