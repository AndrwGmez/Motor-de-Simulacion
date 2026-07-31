import { describe, expect, it } from "vitest";
import { DEMO_FLOW } from "./demo-flow";
import { createSimulationPlan, evaluateCondition } from "./simulation";

describe("evaluateCondition", () => {
  it("evalúa comparaciones y grupos sin ejecutar código", () => {
    const input = { payment: { status: "approved", attempts: 1 }, tags: ["vip", "new"] };
    expect(evaluateCondition({ field: "/payment/status", operator: "equals", value: "approved" }, input)).toBe(true);
    expect(evaluateCondition({ field: "/tags", operator: "contains", value: "vip" }, input)).toBe(true);
    expect(
      evaluateCondition(
        {
          operator: "and",
          conditions: [
            { field: "/payment/attempts", operator: "less_than", value: 3 },
            { field: "/payment/status", operator: "exists" },
          ],
        },
        input,
      ),
    ).toBe(true);
  });
});

describe("createSimulationPlan", () => {
  it("recorre la ruta aprobada de forma determinista", () => {
    const plan = createSimulationPlan(DEMO_FLOW, {
      payment: { status: "approved" },
      inventory: { available: true },
    });
    expect(plan.summary.status).toBe("completed");
    expect(plan.summary.visitedNodeIds).toEqual(
      expect.arrayContaining(["payment-approved", "inventory-available", "prepare", "ship", "completed"]),
    );
    expect(plan.summary.visitedNodeIds).not.toContain("refund");
    expect(plan.events.at(-1)?.type).toBe("run.completed");
    expect(plan.events.map((event) => event.sequence)).toEqual(
      Array.from({ length: plan.events.length }, (_, index) => index + 1),
    );
  });

  it("permite forzar un error controlado", () => {
    const plan = createSimulationPlan(
      DEMO_FLOW,
      { payment: { status: "approved" }, inventory: { available: true } },
      { failedNodeIds: ["ship"] },
    );
    expect(plan.summary.status).toBe("failed");
    expect(plan.events).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ type: "node.failed", payload: expect.objectContaining({ nodeId: "ship" }) }),
      ]),
    );
  });

  it("elige la devolución cuando una decisión no coincide", () => {
    const plan = createSimulationPlan(DEMO_FLOW, {
      payment: { status: "rejected" },
      inventory: { available: true },
    });
    expect(plan.summary.visitedNodeIds).toContain("refund");
    expect(plan.summary.visitedNodeIds).toContain("cancelled");
    expect(plan.summary.status).toBe("failed");
  });
});
