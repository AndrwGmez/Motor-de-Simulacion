import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProjectClient } from "./ProjectClient";

const { createFlowMock, getProjectMock, listFlowsMock, pushMock } = vi.hoisted(() => ({
  createFlowMock: vi.fn(),
  getProjectMock: vi.fn(),
  listFlowsMock: vi.fn(),
  pushMock: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: pushMock }),
}));

vi.mock("@/lib/workspace-service", () => ({
  createFlow: createFlowMock,
  getProject: getProjectMock,
  listFlows: listFlowsMock,
}));

const project = {
  id: "project-id",
  name: "Operaciones",
  description: "Proyecto de prueba",
  role: "owner" as const,
  createdAt: "2026-07-30T00:00:00Z",
  updatedAt: "2026-07-30T00:00:00Z",
};

describe("ProjectClient", () => {
  beforeEach(() => {
    pushMock.mockReset();
    createFlowMock.mockReset();
    getProjectMock.mockReset();
    listFlowsMock.mockReset();
    getProjectMock.mockResolvedValue(project);
    listFlowsMock.mockResolvedValue([]);
  });

  it("no ofrece crear flujos a un viewer", async () => {
    getProjectMock.mockResolvedValue({ ...project, role: "viewer" });
    render(<ProjectClient projectId="project-id" />);
    await screen.findByRole("heading", { name: "Operaciones" });
    expect(screen.queryByRole("button", { name: /Nuevo flujo/ })).not.toBeInTheDocument();
  });

  it("navega al borrador inmediatamente después de crearlo", async () => {
    createFlowMock.mockResolvedValue({
      id: "flow-id",
      projectId: "project-id",
      name: "Nuevo flujo",
      description: "",
      draftEtag: "\"one\"",
      publishedVersionCount: 0,
      createdAt: "2026-07-30T00:00:00Z",
      updatedAt: "2026-07-30T00:00:00Z",
    });
    const user = userEvent.setup();
    render(<ProjectClient projectId="project-id" />);
    await user.click(await screen.findByRole("button", { name: /Nuevo flujo/ }));
    await user.type(screen.getByLabelText("Nombre"), "Nuevo flujo");
    await user.click(screen.getByRole("button", { name: "Crear y abrir borrador" }));
    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/proyectos/project-id/flujos/flow-id"));
  });
});
