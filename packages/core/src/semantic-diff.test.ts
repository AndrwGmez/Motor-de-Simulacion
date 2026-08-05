import { describe, expect, it } from "vitest";
import { DEMO_DOCUMENT } from "./demo-flow";
import { diffFlowDefinitions } from "./semantic-diff";

const clone = () => structuredClone(DEMO_DOCUMENT.definition);

describe("diffFlowDefinitions", () => {
  it("ignores collection order and detects no changes for equivalent definitions", () => {
    const before = clone();
    const after = clone();
    after.nodes.reverse();
    after.edges.reverse();

    expect(diffFlowDefinitions(before, after)).toMatchObject({
      hasChanges: false,
      behaviorChanged: false,
      highestImpact: "none",
    });
  });

  it("classifies canvas and copy changes as visual", () => {
    const before = clone();
    const after = clone();
    after.name = "Nuevo nombre";
    after.nodes[0].position.x += 80;
    after.nodes[0].label = "Nuevo inicio";

    const result = diffFlowDefinitions(before, after);

    expect(result.highestImpact).toBe("visual");
    expect(result.behaviorChanged).toBe(false);
    expect(result.summary.visual).toBe(2);
    expect(result.changes.find((change) => change.entity === "node")?.fields.map((field) => field.path))
      .toEqual(["/label", "/position"]);
  });

  it("classifies runtime configuration and routing as behavioral", () => {
    const before = clone();
    const after = clone();
    after.nodes[0].durationMs += 100;
    after.edges[0].priority += 1;

    const result = diffFlowDefinitions(before, after);

    expect(result.highestImpact).toBe("behavioral");
    expect(result.behaviorChanged).toBe(true);
    expect(result.summary.behavioral).toBe(2);
  });

  it("marks removals, ports and required inputs as breaking", () => {
    const before = clone();
    const after = clone();
    after.nodes = after.nodes.slice(1);
    after.edges[0].targetPort = "new-contract";
    after.variables.push({ path: "/tenantId", type: "string", required: true });

    const result = diffFlowDefinitions(before, after);

    expect(result.highestImpact).toBe("breaking");
    expect(result.summary.breaking).toBe(3);
    expect(result.changes).toEqual(expect.arrayContaining([
      expect.objectContaining({ entity: "node", operation: "removed", impact: "breaking" }),
      expect.objectContaining({ entity: "edge", operation: "modified", impact: "breaking" }),
      expect.objectContaining({ entity: "variable", operation: "added", impact: "breaking" }),
    ]));
  });

  it("treats a node type change as a breaking runtime contract change", () => {
    const before = clone();
    const after = clone();
    after.nodes[1].type = "delay";

    const result = diffFlowDefinitions(before, after);

    expect(result.highestImpact).toBe("breaking");
    expect(result.changes).toContainEqual(expect.objectContaining({
      entity: "node",
      id: after.nodes[1].id,
      operation: "modified",
      impact: "breaking",
    }));
  });

  it("matches the production diff rules for directional contract changes", () => {
    const before = clone();
    const after = clone();
    before.variables[0].required = true;
    after.variables[0].required = false;
    after.nodes[0].inputs.push({ id: "optional", label: "Optional" });

    const result = diffFlowDefinitions(before, after);

    expect(result.changes.find((change) => change.entity === "variable")).toMatchObject({
      impact: "behavioral",
      fields: [expect.objectContaining({ path: "/required", impact: "behavioral" })],
    });
    expect(result.changes.find((change) => change.entity === "node")).toMatchObject({
      impact: "behavioral",
      fields: [expect.objectContaining({ path: "/inputs", impact: "behavioral" })],
    });
  });

  it("treats groups as visual entities and reports layout separately", () => {
    const before = clone();
    const after = clone();
    after.nodes.push({
      id: "visual-group",
      type: "group",
      label: "Checkout",
      description: "Visual boundary",
      inputs: [],
      outputs: [],
      activationMode: "each",
      durationMs: 0,
      configuration: { collapsed: false },
      position: { x: 0, y: 0, z: 0 },
      locked: false,
      metadata: {},
    });
    after.layout.mode = "layers";

    const result = diffFlowDefinitions(before, after);

    expect(result.changes.find((change) => change.id === "visual-group")?.impact).toBe("visual");
    expect(result.changes.find((change) => change.entity === "layout")).toMatchObject({
      id: "layout",
      impact: "visual",
      fields: [expect.objectContaining({ path: "/mode" })],
    });
    expect(result.behaviorChanged).toBe(false);
  });
});
