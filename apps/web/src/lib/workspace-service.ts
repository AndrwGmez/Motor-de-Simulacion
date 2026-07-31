import { DEMO_FLOW, DEMO_PROJECTS } from "@flowverse/core";
import type { EditableFlow, FlowDefinition } from "@flowverse/core";

export interface ProjectSummary {
  id: string;
  name: string;
  description: string;
  role: "owner" | "editor" | "viewer";
  createdAt: string;
  updatedAt: string;
  flowCount?: number;
}

export interface FlowSummary {
  id: string;
  projectId: string;
  name: string;
  description: string;
  draftEtag: string;
  publishedVersionCount: number;
  createdAt: string;
  updatedAt: string;
}

const API_URL = process.env.NEXT_PUBLIC_API_URL?.replace(/\/$/, "");
const FLOW_STORAGE_PREFIX = "flowverse:flow:";
const DEMO_PROJECTS_STORAGE = "flowverse:demo-projects";
const DEMO_FLOWS_STORAGE_PREFIX = "flowverse:demo-flows:";

export class WorkspaceApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "WorkspaceApiError";
  }
}

function csrfToken() {
  return globalThis.document?.cookie
    .split("; ")
    .find((entry) => entry.startsWith("flowverse_csrf="))
    ?.split("=")
    .slice(1)
    .join("=");
}

function isUuid(value: string) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
}

async function apiError(response: Response, fallback: string) {
  const payload = await response.json().catch(() => ({})) as { message?: string; error?: { message?: string } };
  return new WorkspaceApiError(response.status, payload.message ?? payload.error?.message ?? fallback);
}

function notFound(resource: string): Error {
  return new WorkspaceApiError(404, `${resource} no existe o su identificador no es válido.`);
}

function starterFlow(name: string): FlowDefinition {
  return {
    schemaVersion: "1.0",
    name,
    description: "",
    layout: { mode: "directional" },
    variables: [],
    nodes: [
      {
        id: "start",
        type: "trigger",
        label: "Inicio",
        description: "Punto de entrada del flujo",
        inputs: [],
        outputs: [{ id: "output", label: "Salida" }],
        activationMode: "each",
        durationMs: 0,
        configuration: { eventName: "manual" },
        position: { x: -120, y: 0, z: 0 },
        locked: false,
        metadata: { category: "inicio", color: "#7f8cff" },
      },
      {
        id: "end",
        type: "end",
        label: "Fin",
        description: "Finaliza una ruta",
        inputs: [{ id: "input", label: "Entrada" }],
        outputs: [],
        activationMode: "each",
        durationMs: 0,
        configuration: { result: "success" },
        position: { x: 120, y: 0, z: 0 },
        locked: false,
        metadata: { category: "resultado", color: "#35d39d" },
      },
    ],
    edges: [
      {
        id: "start_end",
        source: "start",
        target: "end",
        sourcePort: "output",
        targetPort: "input",
        label: "",
        isDefault: false,
        priority: 0,
      },
    ],
  };
}

function storedDemoProjects(): ProjectSummary[] {
  try {
    const value = JSON.parse(globalThis.localStorage?.getItem(DEMO_PROJECTS_STORAGE) ?? "[]") as unknown;
    return Array.isArray(value) ? value as ProjectSummary[] : [];
  } catch {
    return [];
  }
}

function demoProjects(): ProjectSummary[] {
  return [
    ...DEMO_PROJECTS.map((project) => ({
      ...project,
      role: project.role as ProjectSummary["role"],
      createdAt: project.updatedAt,
    })),
    ...storedDemoProjects(),
  ];
}

function storedDemoFlows(projectId: string): FlowSummary[] {
  try {
    const value = JSON.parse(globalThis.localStorage?.getItem(`${DEMO_FLOWS_STORAGE_PREFIX}${projectId}`) ?? "[]") as unknown;
    return Array.isArray(value) ? value as FlowSummary[] : [];
  } catch {
    return [];
  }
}

function headers(): Record<string, string> {
  const csrf = csrfToken();
  return {
    "Content-Type": "application/json",
    ...(csrf ? { "X-CSRF-Token": decodeURIComponent(csrf) } : {}),
  };
}

export async function listProjects(): Promise<ProjectSummary[]> {
  if (!API_URL) {
    return demoProjects();
  }
  const response = await fetch(`${API_URL}/v1/projects`, { credentials: "include" });
  if (!response.ok) throw await apiError(response, "No se pudieron cargar los proyectos.");
  const payload = await response.json() as { items?: ProjectSummary[]; data?: { items?: ProjectSummary[] } } | ProjectSummary[];
  if (Array.isArray(payload)) return payload;
  return payload.items ?? payload.data?.items ?? [];
}

export async function createProject(input: { name: string; description: string }): Promise<ProjectSummary> {
  if (!API_URL) {
    const project: ProjectSummary = {
      id: `demo-${Date.now().toString(36)}`,
      ...input,
      role: "owner",
      flowCount: 0,
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };
    globalThis.localStorage?.setItem(
      DEMO_PROJECTS_STORAGE,
      JSON.stringify([project, ...storedDemoProjects()]),
    );
    return project;
  }
  const response = await fetch(`${API_URL}/v1/projects`, {
    method: "POST",
    credentials: "include",
    headers: headers(),
    body: JSON.stringify(input),
  });
  if (!response.ok) throw await apiError(response, "No se pudo crear el proyecto.");
  const payload = await response.json() as ProjectSummary | { data: ProjectSummary };
  return "data" in payload ? payload.data : payload;
}

export async function getProject(projectId: string): Promise<ProjectSummary> {
  if (!API_URL) {
    const demo = demoProjects().find((project) => project.id === projectId);
    if (!demo) throw notFound("El proyecto");
    return demo;
  }
  if (!isUuid(projectId)) throw notFound("El proyecto");
  const response = await fetch(`${API_URL}/v1/projects/${encodeURIComponent(projectId)}`, { credentials: "include" });
  if (!response.ok) throw await apiError(response, "No se pudo cargar el proyecto.");
  const payload = await response.json() as ProjectSummary | { data: ProjectSummary };
  return "data" in payload ? payload.data : payload;
}

export async function listFlows(projectId: string): Promise<FlowSummary[]> {
  if (!API_URL) {
    const initial = DEMO_PROJECTS.some((project) => project.id === projectId)
      ? [{
        id: "pedidos",
        projectId,
        name: DEMO_FLOW.name,
        description: DEMO_FLOW.description,
        draftEtag: '"demo-7"',
        publishedVersionCount: 3,
        createdAt: "2026-07-20T14:00:00.000Z",
        updatedAt: "2026-07-30T19:46:00.000Z",
      }]
      : [];
    return [...storedDemoFlows(projectId), ...initial];
  }
  if (!isUuid(projectId)) throw notFound("El proyecto");
  const response = await fetch(`${API_URL}/v1/projects/${encodeURIComponent(projectId)}/flows`, { credentials: "include" });
  if (!response.ok) throw await apiError(response, "No se pudieron cargar los flujos.");
  const payload = await response.json() as FlowSummary[] | { data: FlowSummary[] };
  return Array.isArray(payload) ? payload : payload.data;
}

export async function createFlow(projectId: string, name: string): Promise<FlowSummary> {
  if (!API_URL) {
    const id = `demo-flow-${Date.now().toString(36)}`;
    const now = new Date().toISOString();
    const definition = starterFlow(name);
    const document: EditableFlow = {
      flowId: id,
      versionId: `${id}-draft`,
      status: "draft",
      revision: 1,
      etag: '"demo-1"',
      draftMatchesPublished: false,
      updatedAt: now,
      definition,
    };
    globalThis.localStorage?.setItem(`${FLOW_STORAGE_PREFIX}${id}`, JSON.stringify(document));
    const summary: FlowSummary = {
      id,
      projectId,
      name,
      description: "",
      draftEtag: '"demo-1"',
      publishedVersionCount: 0,
      createdAt: now,
      updatedAt: now,
    };
    globalThis.localStorage?.setItem(
      `${DEMO_FLOWS_STORAGE_PREFIX}${projectId}`,
      JSON.stringify([summary, ...storedDemoFlows(projectId)]),
    );
    return summary;
  }
  if (!isUuid(projectId)) throw notFound("El proyecto");
  const response = await fetch(`${API_URL}/v1/projects/${encodeURIComponent(projectId)}/flows`, {
    method: "POST",
    credentials: "include",
    headers: headers(),
    body: JSON.stringify({ name, description: "" }),
  });
  if (!response.ok) throw await apiError(response, "No se pudo crear el flujo.");
  const payload = await response.json() as FlowSummary | { data: FlowSummary };
  return "data" in payload ? payload.data : payload;
}
