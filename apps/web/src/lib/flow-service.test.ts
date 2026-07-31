import { afterEach, describe, expect, it, vi } from "vitest";
import { DEMO_DOCUMENT, DEMO_FLOW } from "@flowverse/core";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("flow service API adapter", () => {
  it("carga el draft canónico y conserva el ETag opaco", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      headers: new Headers({ ETag: '"sha256-canonical-checksum"' }),
      json: vi.fn().mockResolvedValue({ data: { definition: DEMO_FLOW } }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { loadFlow } = await import("./flow-service");
    const id = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
    const document = await loadFlow(id);
    expect(document.definition).toEqual(DEMO_FLOW);
    expect(document.etag).toBe('"sha256-canonical-checksum"');
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/flows/${id}/draft`,
      { credentials: "include" },
    );
  });

  it("recupera la última publicación al recargar y evita duplicarla si coincide el checksum", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const flowId = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
    const versionId = "04fbd9ba-8dd8-4f61-93be-845f067370f9";
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        headers: new Headers({ ETag: '"same-checksum"' }),
        json: vi.fn().mockResolvedValue(DEMO_FLOW),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: vi.fn().mockResolvedValue([
          {
            id: versionId,
            flowId,
            number: 5,
            checksum: "same-checksum",
            publishedAt: "2026-07-30T21:00:00Z",
            publishedBy: "05fbd9ba-8dd8-4f61-93be-845f067370f9",
          },
        ]),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: vi.fn().mockResolvedValue({ items: [] }),
      });
    vi.stubGlobal("fetch", fetchMock);
    const { loadFlow } = await import("./flow-service");
    const document = await loadFlow(flowId);
    expect(document.publishedVersionId).toBe(versionId);
    expect(document.publishedVersionNumber).toBe(5);
    expect(document.draftMatchesPublished).toBe(true);
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      `http://api.flowverse.test/v1/flows/${flowId}/versions`,
      { credentials: "include" },
    );
  });

  it("hidrata runs terminales autenticados sin conservar input ni output", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const flowId = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
    const versionId = "04fbd9ba-8dd8-4f61-93be-845f067370f9";
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        headers: new Headers({ ETag: '"draft"' }),
        json: vi.fn().mockResolvedValue(DEMO_FLOW),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: vi.fn().mockResolvedValue([]),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: vi.fn().mockResolvedValue({
          items: [{
            id: "03fbd9ba-8dd8-4f61-93be-845f067370f9",
            versionId,
            status: "completed",
            createdAt: "2026-07-30T21:00:00Z",
            completedAt: "2026-07-30T21:00:01Z",
            input: { private: true },
            output: { private: true },
            events: [{ logicalTimeMs: 0 }, { logicalTimeMs: 125 }],
            nodeRuns: [
              { nodeId: "start", status: "success", startedMs: 0, completedMs: 0 },
              { nodeId: "completed", status: "success", startedMs: 100, completedMs: 125 },
            ],
          }, {
            id: "running-run",
            status: "running",
          }],
        }),
      });
    vi.stubGlobal("fetch", fetchMock);
    const { loadFlow } = await import("./flow-service");
    const document = await loadFlow(flowId);
    expect(document.runHistory).toEqual([
      expect.objectContaining({
        flowVersionId: versionId,
        status: "completed",
        durationMs: 125,
        visitedNodeIds: ["start", "completed"],
        eventCount: 2,
      }),
    ]);
    expect(document.runHistory?.[0]).not.toHaveProperty("input");
    expect(document.runHistory?.[0]).not.toHaveProperty("output");
  });

  it("envía If-Match sin intentar interpretar el checksum", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      headers: new Headers({ ETag: '"new-checksum"' }),
      json: vi.fn(),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { saveFlow } = await import("./flow-service");
    const document = {
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
      etag: '"old-checksum"',
    };
    const result = await saveFlow(document);
    expect(result.etag).toBe('"new-checksum"');
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/draft"),
      expect.objectContaining({
        method: "PUT",
        headers: expect.objectContaining({ "If-Match": '"old-checksum"' }),
      }),
    );
  });

  it("propaga errores HTTP y no guarda silenciosamente en local", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 422,
      json: vi.fn().mockResolvedValue({ message: "Documento inválido." }),
    }));
    const { saveFlow } = await import("./flow-service");
    await expect(saveFlow({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
    })).rejects.toThrow("Documento inválido.");
  });

  it("no sustituye por demo cuando falla la red en modo API", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("network down")));
    const { loadFlow } = await import("./flow-service");
    await expect(loadFlow("02fbd9ba-8dd8-4f61-93be-845f067370f9")).rejects.toThrow("network down");
  });

  it("rechaza identificadores demo cuando la API está configurada", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const { loadFlow } = await import("./flow-service");
    await expect(loadFlow("pedidos")).rejects.toMatchObject({ status: 404 });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("no convierte un fallo de red al guardar en persistencia local", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    const localWrite = vi.spyOn(Storage.prototype, "setItem");
    const { saveFlow } = await import("./flow-service");
    await expect(saveFlow({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
    })).rejects.toThrow("offline");
    expect(localWrite).not.toHaveBeenCalled();
  });

  it("crea runs sobre el snapshot del borrador cuando no hay versión publicada", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const runId = "03fbd9ba-8dd8-4f61-93be-845f067370f9";
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: vi.fn().mockResolvedValue({ id: runId }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { startRun } = await import("./flow-service");
    const flowId = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
    const result = await startRun(
      { ...structuredClone(DEMO_DOCUMENT), flowId, versionId: `${flowId}-draft` },
      { payment: { status: "approved" } },
      { failedNodeIds: [], forcedEdgeIds: {} },
    );
    expect(result).toEqual({ source: "api", runId });
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/flows/${flowId}/runs`,
      expect.objectContaining({ method: "POST", credentials: "include" }),
    );
  });

  it("publica usando el ETag opaco y conserva el UUID retornado", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const versionId = "04fbd9ba-8dd8-4f61-93be-845f067370f9";
    const flowId = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      headers: new Headers({ ETag: '"draft-after-publish"' }),
      json: vi.fn().mockResolvedValue({
        data: {
          id: versionId,
          flowId,
          number: 4,
          checksum: "sha256-version",
          publishedAt: "2026-07-30T21:00:00Z",
          publishedBy: "05fbd9ba-8dd8-4f61-93be-845f067370f9",
        },
      }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { publishFlow } = await import("./flow-service");
    const result = await publishFlow({
      ...structuredClone(DEMO_DOCUMENT),
      flowId,
      etag: '"draft-before-publish"',
    });
    expect(result.version.id).toBe(versionId);
    expect(result.etag).toBe('"draft-after-publish"');
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/flows/${flowId}/publish`,
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        headers: expect.objectContaining({ "If-Match": '"draft-before-publish"' }),
      }),
    );
  });

  it("bloquea shares de borradores que ya no coinciden con su publicación", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const { createShareLink } = await import("./flow-service");
    await expect(createShareLink({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
      publishedVersionId: "04fbd9ba-8dd8-4f61-93be-845f067370f9",
      draftMatchesPublished: false,
    }, [])).rejects.toThrow("Publica el borrador actual");
  });

  it("convierte el token público de API en una ruta web de solo lectura", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: vi.fn().mockResolvedValue({
        id: "06fbd9ba-8dd8-4f61-93be-845f067370f9",
        token: "public-token-123",
        publicUrl: "http://api.flowverse.test/public/v1/shares/public-token-123",
      }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { createShareLink } = await import("./flow-service");
    const result = await createShareLink({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
      publishedVersionId: "04fbd9ba-8dd8-4f61-93be-845f067370f9",
      draftMatchesPublished: true,
    }, []);
    expect(result.url).toBe("http://localhost:3000/compartir/public-token-123");
    expect(result.url).not.toContain("/public/v1/shares/");
  });

  it("hidrata únicamente el resumen sanitizado de runs públicos", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: vi.fn().mockResolvedValue({
        definition: DEMO_FLOW,
        runs: [{
          id: "03fbd9ba-8dd8-4f61-93be-845f067370f9",
          status: "completed",
          path: ["start", "completed"],
          timings: { start: 0, completed: 50 },
          input: { secret: true },
        }],
      }),
    }));
    const { loadPublicShare } = await import("./flow-service");
    const document = await loadPublicShare("public-token");
    expect(document.runHistory).toEqual([
      expect.objectContaining({
        id: "03fbd9ba-8dd8-4f61-93be-845f067370f9",
        status: "completed",
        visitedNodeIds: ["start", "completed"],
        durationMs: 50,
      }),
    ]);
    expect(document.runHistory?.[0]).not.toHaveProperty("input");
    expect(document.runHistory?.[0].createdAt).toBeUndefined();
  });

  it("adjunta solo runs terminales de la versión publicada", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: vi.fn().mockResolvedValue({
        id: "06fbd9ba-8dd8-4f61-93be-845f067370f9",
        token: "public-token",
      }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { createShareLink } = await import("./flow-service");
    const flowId = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
    const versionId = "04fbd9ba-8dd8-4f61-93be-845f067370f9";
    const eligibleId = "03fbd9ba-8dd8-4f61-93be-845f067370f9";
    await createShareLink({
      ...structuredClone(DEMO_DOCUMENT),
      flowId,
      publishedVersionId: versionId,
      draftMatchesPublished: true,
    }, [
      {
        id: eligibleId,
        flowVersionId: versionId,
        status: "completed",
        durationMs: 10,
        visitedNodeIds: [],
        eventCount: 1,
      },
      {
        id: "07fbd9ba-8dd8-4f61-93be-845f067370f9",
        flowVersionId: `${flowId}-draft`,
        status: "failed",
        durationMs: 10,
        visitedNodeIds: [],
        eventCount: 1,
      },
      {
        id: "08fbd9ba-8dd8-4f61-93be-845f067370f9",
        flowVersionId: versionId,
        status: "cancelled",
        durationMs: 10,
        visitedNodeIds: [],
        eventCount: 1,
      },
    ]);
    const request = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(JSON.parse(String(request.body))).toEqual({ versionId, runIds: [eligibleId] });
  });

  it("propaga el fallo al revocar un enlace", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 403,
      json: vi.fn().mockResolvedValue({ message: "Sin permiso." }),
    }));
    const { revokeShareLink } = await import("./flow-service");
    await expect(revokeShareLink("06fbd9ba-8dd8-4f61-93be-845f067370f9")).rejects.toThrow("Sin permiso.");
  });

  it("explica el conflicto de ETag al publicar", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 412,
      headers: new Headers(),
      json: vi.fn().mockResolvedValue({ message: "precondition failed" }),
    }));
    const { publishFlow } = await import("./flow-service");
    await expect(publishFlow({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
    })).rejects.toThrow("cambió en otra pestaña");
  });
});

// La API omite `metadata` porque el contrato lo declara opcional, pero el
// editor 3D lee `node.metadata.color` en cada fotograma. La ingesta es el único
// punto por el que entran flujos (API, importación, texto y enlace público),
// así que es donde se rellena el valor.
describe("ingesta de flujos sin campos opcionales", () => {
  const apiShapedFlow = {
    schemaVersion: "1.0",
    name: "Pedidos",
    variables: [],
    layout: { mode: "directional" },
    nodes: [
      {
        id: "start",
        type: "trigger",
        label: "Inicio",
        inputs: [],
        outputs: [{ id: "output", label: "Salida" }],
        activationMode: "each",
        durationMs: 0,
        configuration: {},
        position: { x: -120, y: 0, z: 0 },
        locked: false,
      },
      {
        id: "end",
        type: "end",
        label: "Fin",
        inputs: [{ id: "input", label: "Entrada" }],
        outputs: [],
        activationMode: "each",
        durationMs: 0,
        configuration: { result: "success" },
        position: { x: 120, y: 0, z: 0 },
        locked: false,
      },
    ],
    edges: [{
      id: "start_end",
      source: "start",
      target: "end",
      sourcePort: "output",
      targetPort: "input",
      priority: 0,
      isDefault: false,
    }],
  };

  it("rellena metadata ausente para que el editor pueda leer color y categoría", async () => {
    const { parseImportedFlow } = await import("./flow-service");
    const definition = parseImportedFlow(structuredClone(apiShapedFlow));
    for (const node of definition.nodes) {
      expect(node.metadata).toBeTypeOf("object");
      expect(node.metadata).not.toBeNull();
    }
  });

  it("conserva la metadata que ya venía en el flujo", async () => {
    const { parseImportedFlow } = await import("./flow-service");
    const source = structuredClone(apiShapedFlow) as typeof apiShapedFlow & {
      nodes: (typeof apiShapedFlow.nodes[number] & { metadata?: Record<string, string> })[];
    };
    source.nodes[0].metadata = { color: "#ff0000", category: "pagos" };
    const definition = parseImportedFlow(source);
    expect(definition.nodes[0].metadata).toEqual({ color: "#ff0000", category: "pagos" });
  });
});
