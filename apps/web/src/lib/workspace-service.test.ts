import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
  localStorage.clear();
});

describe("workspace service", () => {
  it("rechaza proyectos no UUID en modo API sin consultar datos demo", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const { getProject, listFlows } = await import("./workspace-service");
    await expect(getProject("demo")).rejects.toThrow("no existe");
    await expect(listFlows("demo")).rejects.toThrow("no existe");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("deja que la API cree la definición inicial mínima", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const projectId = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: vi.fn().mockResolvedValue({
        id: "03fbd9ba-8dd8-4f61-93be-845f067370f9",
        projectId,
        name: "Flujo mínimo",
        description: "",
        draftEtag: "\"starter\"",
        publishedVersionCount: 0,
        createdAt: "2026-07-30T00:00:00Z",
        updatedAt: "2026-07-30T00:00:00Z",
      }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { createFlow } = await import("./workspace-service");
    await createFlow(projectId, "Flujo mínimo");
    const request = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(JSON.parse(String(request.body))).toEqual({ name: "Flujo mínimo", description: "" });
  });

  it("crea un borrador demo mínimo con identificador único", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "");
    const { createFlow } = await import("./workspace-service");
    const flow = await createFlow("demo", "Flujo mínimo");
    const saved = JSON.parse(localStorage.getItem(`flowverse:flow:${flow.id}`) ?? "{}") as {
      definition?: { nodes?: unknown[]; edges?: unknown[] };
    };
    expect(flow.id).toMatch(/^demo-flow-/);
    expect(saved.definition?.nodes).toHaveLength(2);
    expect(saved.definition?.edges).toHaveLength(1);
  });

  it("conserva proyectos demo al navegar a su ruta", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "");
    const { createProject, getProject } = await import("./workspace-service");
    const created = await createProject({ name: "Nuevo espacio", description: "Persistente" });
    await expect(getProject(created.id)).resolves.toEqual(created);
  });
});
