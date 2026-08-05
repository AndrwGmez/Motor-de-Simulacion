import { afterEach, describe, expect, it, vi } from "vitest";

const RUN_ID = "03fbd9ba-8dd8-4f61-93be-845f067370f9";
const FLOW_ID = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
const VERSION_ID = "04fbd9ba-8dd8-4f61-93be-845f067370f9";

function incidentFixture() {
  const frames = [
    { sequence: 5, occurredAt: "2026-08-04T14:00:01.000Z", logicalTimeMs: 120, type: "run.failed", category: "run", message: "provider timeout", payload: { error: "provider timeout" } },
    { sequence: 1, occurredAt: "2026-08-04T14:00:00.000Z", logicalTimeMs: 0, type: "run.started", category: "run", payload: {} },
    { sequence: 4, occurredAt: "2026-08-04T14:00:00.900Z", logicalTimeMs: 120, type: "node.failed", category: "node", nodeId: "payment", message: "Provider timed out", payload: { nodeId: "payment", code: "provider.timeout" } },
    { sequence: 3, occurredAt: "2026-08-04T14:00:00.500Z", logicalTimeMs: 60, type: "edge.traversed", category: "edge", edgeId: "checkout-payment", payload: { edgeId: "checkout-payment" } },
    { sequence: 2, occurredAt: "2026-08-04T14:00:00.200Z", logicalTimeMs: 10, type: "node.started", category: "node", nodeId: "payment", payload: { nodeId: "payment" } },
  ];
  return {
    schemaVersion: "1.0",
    runId: RUN_ID,
    traceId: "0123456789abcdef0123456789abcdef",
    flowId: FLOW_ID,
    flowVersionId: VERSION_ID,
    definitionEtag: "sha256:incident",
    status: "failed",
    createdAt: "2026-08-04T14:00:00.000Z",
    startedAt: "2026-08-04T14:00:00.000Z",
    completedAt: "2026-08-04T14:00:01.000Z",
    error: "provider timeout",
    summary: {
      eventCount: 5,
      logicalDurationMs: 120,
      visitedNodeIds: ["payment"],
      traversedEdgeIds: ["checkout-payment"],
      failedNodeIds: ["payment"],
    },
    integrity: {
      complete: true,
      firstSequence: 1,
      lastSequence: 5,
      missingSequences: [],
      duplicateSequences: [],
    },
    rootCause: {
      sequence: 4,
      type: "node.failed",
      nodeId: "payment",
      code: "provider.timeout",
      message: "Provider timed out",
    },
    timeline: frames,
  };
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("incident service", () => {
  it("carga el informe autenticado y normaliza la timeline por secuencia", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(incidentFixture()), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const { getRunIncident } = await import("./incident-service");

    const report = await getRunIncident(RUN_ID);

    expect(report.timeline.map((frame) => frame.sequence)).toEqual([1, 2, 3, 4, 5]);
    expect(report.rootCause).toEqual(expect.objectContaining({ code: "provider.timeout", sequence: 4 }));
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/runs/${RUN_ID}/incident`,
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("rechaza informes que no respetan el contrato antes de mostrarlos", async () => {
    const fixture = incidentFixture();
    fixture.traceId = "trace-no-valido";
    const { parseIncidentReport } = await import("./incident-service");

    expect(() => parseIncidentReport(fixture)).toThrow("informe de incidente inválido");
  });

  it("reconstruye únicamente los deltas hasta la secuencia seleccionada", async () => {
    const { parseIncidentReport, reconstructIncidentState } = await import("./incident-service");
    const report = parseIncidentReport(incidentFixture());

    const beforeFailure = reconstructIncidentState(report.timeline, 3);
    expect(beforeFailure).toMatchObject({
      sequence: 3,
      logicalTimeMs: 60,
      appliedEvents: 3,
      runStatus: "running",
      nodeStates: { payment: "running" },
      nodeVisits: [expect.objectContaining({ nodeId: "payment", visit: 1, state: "running" })],
      traversedEdgeIds: ["checkout-payment"],
    });

    const atFailure = reconstructIncidentState(report.timeline, 4);
    expect(atFailure.nodeStates.payment).toBe("failed");
    expect(atFailure.appliedEvents).toBe(4);
  });

  it("preserva visitas, recorridos repetidos y el índice exacto ante secuencias duplicadas", async () => {
    const { reconstructIncidentState } = await import("./incident-service");
    const atFirstDuplicate = reconstructIncidentState([
      { sequence: 1, occurredAt: "2026-08-04T14:00:00Z", logicalTimeMs: 0, type: "run.started", category: "run", payload: {} },
      { sequence: 2, occurredAt: "2026-08-04T14:00:01Z", logicalTimeMs: 1, type: "node.queued", category: "node", nodeId: "work", payload: { tokenId: "token-1" } },
      { sequence: 2, occurredAt: "2026-08-04T14:00:02Z", logicalTimeMs: 2, type: "node.queued", category: "node", nodeId: "work", payload: { tokenId: "token-2" } },
    ], 2, 1);

    expect(atFirstDuplicate.appliedEvents).toBe(2);
    expect(atFirstDuplicate.nodeVisits).toHaveLength(1);
    expect(atFirstDuplicate.nodeVisits[0]).toMatchObject({ tokenId: "token-1", nodeId: "work" });

    const replay = reconstructIncidentState([
      { sequence: 1, occurredAt: "2026-08-04T14:00:00Z", logicalTimeMs: 0, type: "node.queued", category: "node", nodeId: "work", payload: { tokenId: "token-1" } },
      { sequence: 2, occurredAt: "2026-08-04T14:00:01Z", logicalTimeMs: 1, type: "node.completed", category: "node", nodeId: "work", payload: { tokenId: "token-1" } },
      { sequence: 3, occurredAt: "2026-08-04T14:00:02Z", logicalTimeMs: 2, type: "edge.traversed", category: "edge", edgeId: "loop", payload: { edgeId: "loop", tokenId: "token-1" } },
      { sequence: 4, occurredAt: "2026-08-04T14:00:03Z", logicalTimeMs: 3, type: "edge.traversed", category: "edge", edgeId: "loop", payload: { edgeId: "loop", tokenId: "token-2" } },
      { sequence: 5, occurredAt: "2026-08-04T14:00:04Z", logicalTimeMs: 4, type: "node.queued", category: "node", nodeId: "work", payload: { tokenId: "token-2" } },
    ], 5);

    expect(replay.nodeVisits).toHaveLength(2);
    expect(replay.traversedEdgeIds).toEqual(["loop", "loop"]);
  });

  it("propaga el mensaje de error seguro de la API", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      message: "No tienes acceso a esta ejecución.",
    }), { status: 403, headers: { "Content-Type": "application/json" } })));
    const { getRunIncident } = await import("./incident-service");

    await expect(getRunIncident(RUN_ID)).rejects.toThrow("No tienes acceso a esta ejecución.");
  });
});
