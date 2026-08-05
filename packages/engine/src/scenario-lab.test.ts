import { describe, expect, it } from "vitest";
import { DEMO_FLOW } from "@flowverse/core";
import {
  compareFlowVariants,
  compareScenarios,
  runScenario,
  type ScenarioCase,
} from "./scenario-lab";

const approved: ScenarioCase = {
  id: "approved",
  name: "Pago e inventario aprobados",
  input: { payment: { status: "approved" }, inventory: { available: true } },
};

describe("Scenario Lab", () => {
  it("captures an exact ordered path and controlled failures", () => {
    const outcome = runScenario(DEMO_FLOW, {
      ...approved,
      overrides: { failedNodeIds: ["ship"] },
    });

    expect(outcome.summary.status).toBe("failed");
    expect(outcome.path[0]).toBe("start");
    expect(outcome.failedNodeIds).toEqual(["ship"]);
  });

  it("compares two scenarios against the same definition", () => {
    const comparison = compareScenarios(DEMO_FLOW, approved, {
      id: "rejected",
      name: "Pago rechazado",
      input: { payment: { status: "rejected" }, inventory: { available: true } },
    });

    expect(comparison.comparison).toMatchObject({
      verdict: "regression",
      statusChanged: true,
      pathChanged: true,
    });
    expect(comparison.comparison.addedNodeIds).toEqual(expect.arrayContaining(["refund", "cancelled"]));
    expect(comparison.comparison.firstDivergence?.baselineNodeId).not.toBe(
      comparison.comparison.firstDivergence?.candidateNodeId,
    );
  });

  it("finds regressions across a baseline and candidate flow", () => {
    const candidate = structuredClone(DEMO_FLOW);
    const ship = candidate.nodes.find((node) => node.id === "ship")!;
    ship.type = "end";
    ship.configuration.result = "failure";

    const report = compareFlowVariants(DEMO_FLOW, candidate, [approved], {
      baselineLabel: "V1",
      candidateLabel: "Draft",
    });

    expect(report.summary).toMatchObject({ total: 1, regressions: 1 });
    expect(report.cases[0].comparison.verdict).toBe("regression");
    expect(report.baselineLabel).toBe("V1");
  });

  it("reports equivalent executions even when run metadata differs", () => {
    const report = compareFlowVariants(DEMO_FLOW, structuredClone(DEMO_FLOW), [approved]);

    expect(report.summary.unchanged).toBe(1);
    expect(report.cases[0].comparison).toMatchObject({
      verdict: "unchanged",
      durationDeltaMs: 0,
      eventCountDelta: 0,
      pathChanged: false,
    });
  });

  it("rejects ambiguous or unbounded suites", () => {
    expect(() => compareFlowVariants(DEMO_FLOW, DEMO_FLOW, [])).toThrow(/at least one/i);
    expect(() => compareFlowVariants(DEMO_FLOW, DEMO_FLOW, [approved, approved])).toThrow(/duplicate/i);
    expect(() => compareFlowVariants(
      DEMO_FLOW,
      DEMO_FLOW,
      Array.from({ length: 51 }, (_, index) => ({ ...approved, id: `case-${index}` })),
    )).toThrow(/at most 50/i);
  });
});
