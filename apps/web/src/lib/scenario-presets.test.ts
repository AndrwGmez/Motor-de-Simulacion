import { afterEach, describe, expect, it } from "vitest";
import { MAX_SCENARIO_PRESETS, deleteScenarioPreset, listScenarioPresets, saveScenarioPreset } from "./scenario-presets";

const flowId = "flow-scenario-lab";

function input(name: string) {
  return {
    name,
    scenarioA: { id: "a", name: "Caso A", input: { approved: true }, overrides: {} },
    scenarioB: { id: "b", name: "Caso B", input: { approved: false }, overrides: { failedNodeIds: ["ship"] } },
  };
}

afterEach(() => globalThis.localStorage.clear());

describe("scenario presets", () => {
  it("guarda copias aisladas, lista y elimina por flujo", () => {
    const source = input("Comparación de pagos");
    const saved = saveScenarioPreset(flowId, source);
    source.scenarioA.input.approved = false;

    expect(listScenarioPresets(flowId)[0]).toMatchObject({
      id: saved.id,
      name: "Comparación de pagos",
      scenarioA: { input: { approved: true } },
    });
    expect(listScenarioPresets("other-flow")).toEqual([]);

    deleteScenarioPreset(flowId, saved.id);
    expect(listScenarioPresets(flowId)).toEqual([]);
  });

  it("limita la colección a los presets más recientes", () => {
    for (let index = 0; index < MAX_SCENARIO_PRESETS + 3; index += 1) {
      saveScenarioPreset(flowId, input(`Preset ${index}`));
    }
    expect(listScenarioPresets(flowId)).toHaveLength(MAX_SCENARIO_PRESETS);
    expect(listScenarioPresets(flowId).map((preset) => preset.name)).not.toContain("Preset 0");
  });

  it("rechaza nombres, datos y overrides fuera de límite", () => {
    expect(() => saveScenarioPreset(flowId, { ...input(""), name: "" })).toThrow(/nombre/i);
    expect(() => saveScenarioPreset(flowId, {
      ...input("Grande"),
      scenarioA: { id: "a", name: "Caso A", input: { payload: "x".repeat(33 * 1024) } },
    })).toThrow(/32 KB/i);
    expect(() => saveScenarioPreset(flowId, {
      ...input("Overrides"),
      scenarioB: { id: "b", name: "Caso B", input: {}, overrides: { failedNodeIds: Array(21).fill("node") } },
    })).toThrow(/overrides/i);
  });

  it("ignora almacenamiento corrupto sin romper el editor", () => {
    globalThis.localStorage.setItem(`flowverse:scenario-presets:${flowId}`, "{roto");
    expect(listScenarioPresets(flowId)).toEqual([]);
  });
});
