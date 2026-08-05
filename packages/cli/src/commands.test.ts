import { describe, expect, it } from "vitest";
import { DEMO_FLOW } from "@flowverse/core";
import { checkDefinition, diffDefinitions, simulateDefinition, validateDefinition } from "./commands";
import { toSarif } from "./sarif";

const flow = () => structuredClone(DEMO_FLOW);

describe("FlowVerse CLI commands", () => {
  it("delegates validation and metrics to the engine", () => {
    const valid = validateDefinition("flow.json", flow());
    expect(valid.valid).toBe(true);
    expect(valid.metrics.nodeCount).toBeGreaterThan(0);

    const invalid = flow();
    invalid.nodes = invalid.nodes.filter((node) => node.type !== "trigger");
    const report = validateDefinition("invalid.json", invalid);
    expect(report.valid).toBe(false);
    expect(report.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "flow.no_trigger", severity: "error" }),
    ]));
  });

  it("uses the core semantic diff without treating visual edits as behavioral", () => {
    const baseline = flow();
    const candidate = flow();
    candidate.nodes[0]!.position.x += 100;

    const report = diffDefinitions("base.json", baseline, "candidate.json", candidate);
    expect(report.diff.highestImpact).toBe("visual");
    expect(report.diff.behaviorChanged).toBe(false);
  });

  it("enforces behavioral and breaking thresholds independently", () => {
    const baseline = flow();
    const behavioral = flow();
    behavioral.nodes[0]!.durationMs += 50;

    expect(checkDefinition("candidate.json", behavioral, {
      baseline,
      baselinePath: "base.json",
      failOn: "behavioral",
    }).passed).toBe(false);
    expect(checkDefinition("candidate.json", behavioral, {
      baseline,
      baselinePath: "base.json",
      failOn: "breaking",
    }).passed).toBe(true);

    const breaking = flow();
    breaking.variables[2]!.required = true;
    const breakingReport = checkDefinition("candidate.json", breaking, {
      baseline,
      baselinePath: "base.json",
      failOn: "breaking",
    });
    expect(breakingReport.passed).toBe(false);
    expect(breakingReport.failures).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: "semantic-change" }),
    ]));
  });

  it("delegates deterministic simulation to the engine", () => {
    const report = simulateDefinition("flow.json", flow(), {
      input: { payment: { status: "approved" }, inventory: { available: true } },
      overrides: { failedNodeIds: ["ship"] },
    });
    expect(report.valid).toBe(true);
    expect(report.plan?.summary.status).toBe("failed");
    expect(report.plan?.events).toEqual(expect.arrayContaining([
      expect.objectContaining({ type: "node.failed", payload: expect.objectContaining({ nodeId: "ship" }) }),
    ]));
  });

  it("renders a SARIF 2.1.0 policy violation", () => {
    const baseline = flow();
    const candidate = flow();
    candidate.nodes[0]!.durationMs += 50;
    const report = checkDefinition("candidate.json", candidate, {
      baseline,
      baselinePath: "base.json",
      failOn: "behavioral",
    });

    const sarif = toSarif(report);
    expect(sarif.version).toBe("2.1.0");
    expect(sarif.runs[0]?.tool.driver.name).toBe("FlowVerse CLI");
    expect(sarif.runs[0]?.results).toEqual(expect.arrayContaining([
      expect.objectContaining({
        ruleId: "flowverse.semantic.behavioral",
        level: "error",
        properties: expect.objectContaining({ policyViolation: true }),
      }),
    ]));
  });
});
