import type { ScenarioCase } from "@flowverse/engine";

const STORAGE_PREFIX = "flowverse:scenario-presets:";
export const MAX_SCENARIO_PRESETS = 12;
const MAX_PRESET_NAME = 80;
const MAX_SCENARIO_BYTES = 32 * 1024;

export interface ScenarioLabPreset {
  id: string;
  name: string;
  updatedAt: string;
  scenarioA: ScenarioCase;
  scenarioB: ScenarioCase;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function serializedSize(value: unknown): number {
  try {
    return new Blob([JSON.stringify(value)]).size;
  } catch {
    throw new Error("El escenario contiene datos que no se pueden guardar.");
  }
}

function validStringMap(value: unknown): value is Record<string, string> {
  return isRecord(value)
    && Object.keys(value).length <= 20
    && Object.entries(value).every(([key, entry]) => key.length <= 160 && typeof entry === "string" && entry.length <= 160);
}

function normalizeScenario(value: unknown, fallbackId: string): ScenarioCase {
  if (!isRecord(value)) throw new Error("El preset contiene un escenario inválido.");
  const id = typeof value.id === "string" && value.id.trim() ? value.id.trim() : fallbackId;
  const name = typeof value.name === "string" ? value.name.trim() : "";
  if (!name || name.length > MAX_PRESET_NAME) {
    throw new Error(`Cada escenario necesita un nombre de hasta ${MAX_PRESET_NAME} caracteres.`);
  }
  if (serializedSize(value.input) > MAX_SCENARIO_BYTES) {
    throw new Error("Los datos de un escenario no pueden superar 32 KB.");
  }
  const triggerId = typeof value.triggerId === "string" && value.triggerId.trim()
    ? value.triggerId.trim()
    : undefined;
  if (triggerId && triggerId.length > 160) throw new Error("El identificador del inicio es demasiado largo.");

  const rawOverrides = isRecord(value.overrides) ? value.overrides : {};
  const failedNodeIds = rawOverrides.failedNodeIds === undefined
    ? []
    : Array.isArray(rawOverrides.failedNodeIds)
      && rawOverrides.failedNodeIds.length <= 20
      && rawOverrides.failedNodeIds.every((entry) => typeof entry === "string" && entry.length <= 160)
      ? rawOverrides.failedNodeIds
      : undefined;
  const forcedEdgeIds = rawOverrides.forcedEdgeIds === undefined
    ? {}
    : validStringMap(rawOverrides.forcedEdgeIds)
      ? rawOverrides.forcedEdgeIds
      : undefined;
  if (!failedNodeIds || !forcedEdgeIds) throw new Error("Los overrides del escenario no son válidos.");

  return {
    id: id.slice(0, 160),
    name,
    input: structuredClone(value.input),
    triggerId,
    overrides: {
      failedNodeIds: [...failedNodeIds],
      forcedEdgeIds: { ...forcedEdgeIds },
    },
  };
}

function normalizePreset(value: unknown): ScenarioLabPreset {
  if (!isRecord(value)) throw new Error("El preset no tiene una estructura válida.");
  const name = typeof value.name === "string" ? value.name.trim() : "";
  if (!name || name.length > MAX_PRESET_NAME) {
    throw new Error(`El preset necesita un nombre de hasta ${MAX_PRESET_NAME} caracteres.`);
  }
  const id = typeof value.id === "string" && value.id.trim() ? value.id.trim() : crypto.randomUUID();
  const updatedAt = typeof value.updatedAt === "string" && !Number.isNaN(new Date(value.updatedAt).getTime())
    ? value.updatedAt
    : new Date().toISOString();
  return {
    id: id.slice(0, 160),
    name,
    updatedAt,
    scenarioA: normalizeScenario(value.scenarioA, "scenario-a"),
    scenarioB: normalizeScenario(value.scenarioB, "scenario-b"),
  };
}

function storageKey(flowId: string): string {
  if (!flowId.trim() || flowId.length > 200) throw new Error("El flujo no tiene un identificador válido.");
  return `${STORAGE_PREFIX}${flowId}`;
}

export function listScenarioPresets(flowId: string): ScenarioLabPreset[] {
  try {
    const raw = JSON.parse(globalThis.localStorage?.getItem(storageKey(flowId)) ?? "[]") as unknown;
    if (!Array.isArray(raw)) return [];
    return raw.flatMap((value) => {
      try {
        return [normalizePreset(value)];
      } catch {
        return [];
      }
    }).sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)).slice(0, MAX_SCENARIO_PRESETS);
  } catch {
    return [];
  }
}

export function saveScenarioPreset(
  flowId: string,
  value: Omit<ScenarioLabPreset, "id" | "updatedAt"> & { id?: string },
): ScenarioLabPreset {
  const preset = normalizePreset({
    ...value,
    id: value.id ?? crypto.randomUUID(),
    updatedAt: new Date().toISOString(),
  });
  const current = listScenarioPresets(flowId).filter((candidate) => candidate.id !== preset.id);
  const next = [preset, ...current].slice(0, MAX_SCENARIO_PRESETS);
  globalThis.localStorage?.setItem(storageKey(flowId), JSON.stringify(next));
  return structuredClone(preset);
}

export function deleteScenarioPreset(flowId: string, presetId: string): void {
  const next = listScenarioPresets(flowId).filter((preset) => preset.id !== presetId);
  globalThis.localStorage?.setItem(storageKey(flowId), JSON.stringify(next));
}
