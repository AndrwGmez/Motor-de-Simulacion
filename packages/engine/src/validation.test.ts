import { describe, expect, it } from "vitest";
import { DEMO_FLOW } from "@flowverse/core";
import { validateFlow } from "./validation";

describe("validateFlow", () => {
  it("acepta el flujo de pedidos como estructura ejecutable", () => {
    const issues = validateFlow(structuredClone(DEMO_FLOW));
    expect(issues.filter((issue) => issue.severity === "error")).toEqual([]);
  });

  it("bloquea decisiones sin camino predeterminado", () => {
    const flow = structuredClone(DEMO_FLOW);
    flow.edges = flow.edges.map((edge) =>
      edge.source === "payment-approved" ? { ...edge, isDefault: false } : edge,
    );
    expect(validateFlow(flow)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "decision.default_path", severity: "error", nodeId: "payment-approved" }),
      ]),
    );
  });

  it("detecta nodos inalcanzables y conexiones rotas", () => {
    const flow = structuredClone(DEMO_FLOW);
    flow.edges.push({
      id: "broken-edge",
      source: "missing",
      target: "completed",
      sourcePort: "output",
      targetPort: "input",
      label: "",
      priority: 1,
      isDefault: false,
    });
    flow.nodes.push({
      ...structuredClone(flow.nodes.find((node) => node.id === "prepare")!),
      id: "orphan",
      label: "Nodo huérfano",
    });
    const issues = validateFlow(flow);
    expect(issues.some((issue) => issue.code === "edge.missing_node" && issue.edgeId === "broken-edge")).toBe(true);
    expect(issues.some((issue) => issue.code === "node.unreachable" && issue.nodeId === "orphan")).toBe(true);
  });

  it("rechaza puertos declarados en nodos que no exponen puertos", () => {
    const flow = structuredClone(DEMO_FLOW);
    const end = flow.nodes.find((node) => node.type === "end")!;
    const trigger = flow.nodes.find((node) => node.type === "trigger")!;
    flow.edges.push({
      id: "invalid-contract-edge",
      source: end.id,
      target: trigger.id,
      sourcePort: "ghost-output",
      targetPort: "ghost-input",
      label: "Contrato imposible",
      priority: 0,
      isDefault: false,
    });

    const issues = validateFlow(flow);

    expect(issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "edge.missing_source_port", edgeId: "invalid-contract-edge" }),
      expect.objectContaining({ code: "edge.missing_target_port", edgeId: "invalid-contract-edge" }),
    ]));
  });
});
