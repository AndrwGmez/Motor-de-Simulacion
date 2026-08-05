import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DEMO_DOCUMENT, type FlowVersionSnapshot } from "@flowverse/core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  getFlowVersion,
  listFlowVersions,
  restoreFlowVersion,
} from "@/lib/version-service";
import { useFlowStore } from "@/store/flow-store";
import { VersionHistoryDialog } from "./VersionHistoryDialog";

vi.mock("@/lib/version-service", () => ({
  listFlowVersions: vi.fn(),
  getFlowVersion: vi.fn(),
  restoreFlowVersion: vi.fn(),
}));

const version = {
  id: "04fbd9ba-8dd8-4f61-93be-845f067370f9",
  flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
  number: 1,
  checksum: "checksum-v1",
  publishedAt: "2026-07-21T10:00:00.000Z",
  publishedBy: "05fbd9ba-8dd8-4f61-93be-845f067370f9",
};

function historicalSnapshot(): FlowVersionSnapshot {
  const definition = structuredClone(DEMO_DOCUMENT.definition);
  definition.name = "Flujo histórico V1";
  definition.nodes = definition.nodes.filter((node) => node.id !== "refund");
  definition.edges = definition.edges.filter((edge) => edge.source !== "refund" && edge.target !== "refund");
  return { version, definition };
}

function renderDialog(canRestore: boolean) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <VersionHistoryDialog open onClose={vi.fn()} canRestore={canRestore} />
    </QueryClientProvider>,
  );
}

describe("VersionHistoryDialog", () => {
  beforeEach(() => {
    vi.mocked(listFlowVersions).mockReset().mockResolvedValue([version]);
    vi.mocked(getFlowVersion).mockReset().mockResolvedValue(historicalSnapshot());
    vi.mocked(restoreFlowVersion).mockReset().mockImplementation(async (_document, snapshot) => ({
      ...structuredClone(snapshot),
      revision: 9,
      etag: '"restored-9"',
      source: "api",
    }));
    useFlowStore.getState().loadDocument({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: version.flowId,
      publishedVersionId: version.id,
      publishedVersionNumber: version.number,
      draftMatchesPublished: false,
      definition: {
        ...structuredClone(DEMO_DOCUMENT.definition),
        name: "Borrador actual",
      },
    });
  });

  it("carga la línea temporal y explica el diff versión a borrador", async () => {
    renderDialog(true);

    expect(await screen.findByText("Línea temporal")).toBeVisible();
    expect(screen.getByLabelText("Versiones publicadas")).toHaveTextContent("Versión 1");
    expect(await screen.findByText("Rupturas")).toBeVisible();
    const summary = screen.getByLabelText("Resumen de cambios");
    expect(within(summary).getByText("Comportamiento")).toBeVisible();
    expect(within(summary).getByText("Visuales")).toBeVisible();
    expect(screen.getByRole("option", { name: "Borrador actual" })).toBeInTheDocument();
    expect(getFlowVersion).toHaveBeenCalledWith(version.id, version.flowId);
  });

  it("confirma y aplica una restauración reversible para un editor", async () => {
    const user = userEvent.setup();
    renderDialog(true);
    const applyButton = await screen.findByRole("button", { name: /Aplicar al borrador/ });
    await user.click(applyButton);
    expect(screen.getByText("¿Aplicar la versión 1 al borrador?")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Restaurar V1" }));

    await waitFor(() => expect(restoreFlowVersion).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(/Versión 1 aplicada al borrador/)).toBeVisible();
    expect(useFlowStore.getState().document.definition.name).toBe("Flujo histórico V1");
    expect(useFlowStore.getState().document.status).toBe("draft");
    expect(useFlowStore.getState().document.etag).toBe('"restored-9"');

    useFlowStore.getState().undo();
    expect(useFlowStore.getState().document.definition.name).toBe("Borrador actual");
  });

  it("permite comparar a viewers sin exponer la acción de restaurar", async () => {
    renderDialog(false);

    expect(await screen.findByText("Línea temporal")).toBeVisible();
    expect(screen.queryByRole("button", { name: /Aplicar al borrador/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Restaurar V1/ })).not.toBeInTheDocument();
  });
});
