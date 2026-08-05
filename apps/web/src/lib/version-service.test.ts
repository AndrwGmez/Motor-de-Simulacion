import { afterEach, describe, expect, it, vi } from "vitest";
import { DEMO_DOCUMENT, DEMO_FLOW, type FlowVersionSnapshot } from "@flowverse/core";

const FLOW_ID = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
const VERSION_1 = "04fbd9ba-8dd8-4f61-93be-845f067370f9";
const VERSION_2 = "06fbd9ba-8dd8-4f61-93be-845f067370f9";

function version(id: string, number: number) {
  return {
    id,
    flowId: FLOW_ID,
    number,
    checksum: `checksum-${number}`,
    publishedAt: `2026-07-${20 + number}T10:00:00.000Z`,
    publishedBy: "05fbd9ba-8dd8-4f61-93be-845f067370f9",
  };
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
  globalThis.localStorage?.clear();
});

describe("version service", () => {
  it("normaliza la lista de la API y la ordena de reciente a antigua", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      items: [version(VERSION_1, 1), version(VERSION_2, 2)],
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const { listFlowVersions } = await import("./version-service");

    await expect(listFlowVersions(FLOW_ID)).resolves.toEqual([
      expect.objectContaining({ id: VERSION_2, number: 2 }),
      expect.objectContaining({ id: VERSION_1, number: 1 }),
    ]);
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/flows/${FLOW_ID}/versions`,
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("carga y valida el snapshot inmutable de una versión", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: version(VERSION_1, 1),
      definition: DEMO_FLOW,
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const { getFlowVersion } = await import("./version-service");

    const snapshot = await getFlowVersion(VERSION_1, FLOW_ID);
    expect(snapshot.version.number).toBe(1);
    expect(snapshot.definition).toEqual(DEMO_FLOW);
    expect(snapshot.definition).not.toBe(DEMO_FLOW);
  });

  it("restaura por el endpoint dedicado con If-Match y adopta revisión y ETag nuevos", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const restoredDefinition = structuredClone(DEMO_FLOW);
    restoredDefinition.name = "Restaurada desde V1";
    const metadata = version(VERSION_1, 1);
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      definition: restoredDefinition,
      restoredFromVersion: metadata,
    }), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        ETag: '"restored-checksum"',
        "X-Draft-Revision": "18",
      },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const { restoreFlowVersion } = await import("./version-service");
    const document = {
      ...structuredClone(DEMO_DOCUMENT),
      flowId: FLOW_ID,
      etag: '"draft-before"',
    };
    const snapshot: FlowVersionSnapshot = { version: metadata, definition: DEMO_FLOW };

    const result = await restoreFlowVersion(document, snapshot);

    expect(result).toMatchObject({
      revision: 18,
      etag: '"restored-checksum"',
      source: "api",
      version: { id: VERSION_1 },
      definition: { name: "Restaurada desde V1" },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/flows/${FLOW_ID}/draft/restore`,
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "If-Match": '"draft-before"' }),
        body: JSON.stringify({ versionId: VERSION_1 }),
      }),
    );
  });

  it("persiste publicaciones y restauraciones completas en modo local", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "");
    const { publishFlow } = await import("./flow-service");
    const { getFlowVersion, listFlowVersions, restoreFlowVersion } = await import("./version-service");
    const document = {
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "demo-local",
      versionId: "demo-local-draft",
      revision: 3,
      etag: '"demo-3"',
    };

    const publication = await publishFlow(document);
    const versions = await listFlowVersions(document.flowId);
    expect(versions).toEqual([expect.objectContaining({
      id: publication.version.id,
      number: 1,
    })]);
    const snapshot = await getFlowVersion(publication.version.id, document.flowId);
    expect(snapshot.definition).toEqual(document.definition);

    const changedDocument = structuredClone(document);
    changedDocument.definition.name = "Cambio posterior";
    changedDocument.publishedVersionId = publication.version.id;
    const restored = await restoreFlowVersion(changedDocument, snapshot);
    expect(restored.source).toBe("local");
    expect(restored.definition.name).toBe(document.definition.name);
    expect(JSON.parse(globalThis.localStorage.getItem("flowverse:flow:demo-local") ?? "{}").definition.name)
      .toBe(document.definition.name);
  });
});
