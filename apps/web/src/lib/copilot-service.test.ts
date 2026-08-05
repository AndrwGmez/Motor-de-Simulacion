import { afterEach, describe, expect, it, vi } from "vitest";

const FLOW_ID = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
const VERSION_ID = "04fbd9ba-8dd8-4f61-93be-845f067370f9";
const RUN_ID = "03fbd9ba-8dd8-4f61-93be-845f067370f9";

function responseFixture() {
  return {
    schemaVersion: "1.0",
    provider: "openai",
    summary: "La ruta de pagos concentra el principal riesgo.",
    suggestions: [
      {
        title: "Inspecciona el pago",
        explanation: "La validación y el incidente apuntan al mismo nodo.",
        severity: "critical",
        confidence: "high",
        evidenceIds: ["validation:payment", "incident:root-cause"],
        actions: [
          { kind: "inspect_node", targetId: "payment", label: "Abrir nodo" },
          { kind: "open_incident", targetId: RUN_ID, label: "Abrir incidente" },
        ],
      },
      {
        title: "Revisa la conexión",
        explanation: "La conexión forma parte de la ruta crítica.",
        severity: "warning",
        confidence: "medium",
        evidenceIds: ["edge:checkout-payment"],
        actions: [{ kind: "inspect_edge", targetId: "checkout-payment", label: "Abrir conexión" }],
      },
    ],
    limitations: ["No se incluyen valores de inputs ni outputs."],
    evidence: {
      schemaVersion: "1.0",
      flowId: FLOW_ID,
      truncated: true,
      items: [
        { id: "node:payment", kind: "node", summary: "Nodo de pago", nodeId: "payment", facts: { type: "integration", durationMs: 420 } },
        { id: "edge:checkout-payment", kind: "edge", summary: "Conexión hacia pago", edgeId: "checkout-payment", facts: { source: "checkout", target: "payment" } },
        { id: "validation:payment", kind: "validation", summary: "Error de validación", nodeId: "payment", facts: { code: "node.invalid" } },
        { id: "incident:summary", kind: "incident", summary: "Resumen del incidente", facts: { runId: RUN_ID, status: "failed" } },
        { id: "incident:root-cause", kind: "incident", summary: "Primer fallo registrado", nodeId: "payment", facts: { sequence: 4 } },
      ],
    },
  };
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
  document.cookie = "flowverse_csrf=; Max-Age=0; path=/";
});

describe("copilot service", () => {
  it("envía referencias opcionales y valida una respuesta totalmente resoluble", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    document.cookie = "flowverse_csrf=csrf-copilot; path=/";
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: responseFixture() }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const { askEvidenceCopilot } = await import("./copilot-service");

    const result = await askEvidenceCopilot(FLOW_ID, {
      question: "  ¿Qué debo corregir antes de publicar?  ",
      baseVersionId: VERSION_ID,
      runId: RUN_ID,
    });

    expect(result).toMatchObject({
      provider: "openai",
      evidence: { flowId: FLOW_ID, truncated: true },
      suggestions: expect.arrayContaining([
        expect.objectContaining({
          severity: "critical",
          actions: expect.arrayContaining([
            expect.objectContaining({ kind: "inspect_node" }),
            expect.objectContaining({ kind: "open_incident" }),
          ]),
        }),
      ]),
    });
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/flows/${FLOW_ID}/copilot`,
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-copilot" }),
        body: JSON.stringify({
          question: "¿Qué debo corregir antes de publicar?",
          baseVersionId: VERSION_ID,
          runId: RUN_ID,
        }),
      }),
    );
  });

  it("rechaza citas inventadas, IDs repetidos y acciones no respaldadas", async () => {
    const { parseEvidenceCopilotResponse } = await import("./copilot-service");
    const unknownCitation = responseFixture();
    unknownCitation.suggestions[0].evidenceIds = ["made:up"];
    expect(() => parseEvidenceCopilotResponse(unknownCitation)).toThrow("respuesta inválida");

    const duplicateEvidence = responseFixture();
    duplicateEvidence.evidence.items.push(structuredClone(duplicateEvidence.evidence.items[0]));
    expect(() => parseEvidenceCopilotResponse(duplicateEvidence)).toThrow("respuesta inválida");

    const ungroundedAction = responseFixture();
    ungroundedAction.suggestions[1].actions[0].targetId = "missing-edge";
    expect(() => parseEvidenceCopilotResponse(ungroundedAction)).toThrow("respuesta inválida");
  });

  it("valida pregunta y referencias antes de abrir una petición", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const { askEvidenceCopilot } = await import("./copilot-service");

    await expect(askEvidenceCopilot(FLOW_ID, { question: "x" })).rejects.toThrow("entre 3 y 4.000");
    await expect(askEvidenceCopilot(FLOW_ID, { question: "Revisar flujo", runId: "run-inválido" })).rejects.toThrow("ejecución");
    await expect(askEvidenceCopilot("flow-inválido", { question: "Revisar flujo" })).rejects.toThrow("flujo");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("propaga el error seguro de la API y explica el modo sin backend", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      message: "El proveedor está temporalmente ocupado.",
    }), { status: 503, headers: { "Content-Type": "application/json" } })));
    let service = await import("./copilot-service");
    await expect(service.askEvidenceCopilot(FLOW_ID, { question: "Revisar antes de publicar" }))
      .rejects.toThrow("El proveedor está temporalmente ocupado.");

    vi.resetModules();
    vi.stubEnv("NEXT_PUBLIC_API_URL", "");
    service = await import("./copilot-service");
    await expect(service.askEvidenceCopilot(FLOW_ID, { question: "Revisar antes de publicar" }))
      .rejects.toThrow("requiere una API configurada");
  });
});
