import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DEMO_DOCUMENT, type FlowVersionSnapshot } from "@flowverse/core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { getFlowVersion, listFlowVersions } from "@/lib/version-service";
import { useFlowStore } from "@/store/flow-store";
import { ScenarioLabDialog } from "./ScenarioLabDialog";

vi.mock("@/lib/version-service", () => ({
  listFlowVersions: vi.fn(),
  getFlowVersion: vi.fn(),
}));

const version = {
  id: "04fbd9ba-8dd8-4f61-93be-845f067370f9",
  flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
  number: 3,
  checksum: "checksum-v3",
  publishedAt: "2026-07-21T10:00:00.000Z",
  publishedBy: "05fbd9ba-8dd8-4f61-93be-845f067370f9",
};

function snapshot(): FlowVersionSnapshot {
  const definition = structuredClone(DEMO_DOCUMENT.definition);
  const payment = definition.nodes.find((node) => node.id === "validate-payment");
  if (payment) payment.durationMs += 600;
  return { version, definition };
}

function renderDialog(onClose = vi.fn()) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return {
    onClose,
    ...render(
      <QueryClientProvider client={client}>
        <ScenarioLabDialog open onClose={onClose} />
      </QueryClientProvider>,
    ),
  };
}

describe("ScenarioLabDialog", () => {
  beforeEach(() => {
    globalThis.localStorage.clear();
    vi.mocked(listFlowVersions).mockReset().mockResolvedValue([version]);
    vi.mocked(getFlowVersion).mockReset().mockResolvedValue(snapshot());
    useFlowStore.getState().loadDocument({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: version.flowId,
      definition: structuredClone(DEMO_DOCUMENT.definition),
    });
  });

  it("compara dos entradas, explica la divergencia y reproduce el plan elegido", async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    const inputs = screen.getAllByLabelText(/^Entrada JSON/);
    fireEvent.change(inputs[1], {
      target: {
        value: JSON.stringify({
          payment: { status: "rejected" },
          inventory: { available: true, checked: false },
        }, null, 2),
      },
    });

    await user.click(screen.getByRole("button", { name: "Comparar escenarios" }));

    expect(screen.getByRole("region", { name: "Resultado de la comparación" })).toHaveTextContent("Regresión detectada");
    expect(screen.getByText("Primera divergencia")).toBeVisible();
    expect(screen.getAllByText("¿Hay inventario?").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Devolver dinero").length).toBeGreaterThan(0);
    expect(screen.getByLabelText("Deltas del candidato")).toHaveTextContent("Δ EVENTOS");

    const replayButtons = screen.getAllByRole("button", { name: /Reproducir plan/ });
    await user.click(replayButtons[1]);

    expect(onClose).toHaveBeenCalledOnce();
    expect(useFlowStore.getState().runStatus).toBe("running");
    expect(useFlowStore.getState().activePlan?.summary.visitedNodeIds).toContain("refund");
    expect(useFlowStore.getState().activePlan?.events).toEqual(useFlowStore.getState().plannedEvents);
  });

  it("ejecuta ambos casos sobre una versión inmutable y el borrador", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("tab", { name: /Versión vs borrador/ }));
    expect(await screen.findByRole("option", { name: /Versión 3/ })).toBeInTheDocument();
    expect(getFlowVersion).toHaveBeenCalledWith(version.id, version.flowId);

    const compare = screen.getByRole("button", { name: "Comparar versión y borrador" });
    await waitFor(() => expect(compare).toBeEnabled());
    await user.click(compare);

    const summary = screen.getByLabelText("Resumen del experimento");
    expect(summary).toHaveTextContent("2casos");
    expect(screen.getByRole("tab", { name: /Caso base/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("region", { name: "Resultado de la comparación" })).toHaveTextContent("Versión 3");
    expect(screen.getByLabelText("Deltas del candidato")).toHaveTextContent("-600 ms");

    await user.click(screen.getByRole("tab", { name: /Caso alternativo/ }));
    expect(screen.getByRole("tab", { name: /Caso alternativo/ })).toHaveAttribute("aria-selected", "true");
  });

  it("guarda, recupera y elimina presets limitados al flujo", async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.clear(screen.getByLabelText("Nombre del preset"));
    await user.type(screen.getByLabelText("Nombre del preset"), "Pago rechazado");
    await user.click(screen.getByRole("button", { name: "Guardar" }));

    const presets = screen.getByLabelText("Preset guardado");
    expect(within(presets).getByRole("option", { name: "Pago rechazado" })).toBeInTheDocument();
    expect((presets as HTMLSelectElement).value).not.toBe("");

    await user.click(screen.getByRole("button", { name: "Eliminar preset" }));
    expect(within(presets).queryByRole("option", { name: "Pago rechazado" })).not.toBeInTheDocument();
    expect(globalThis.localStorage.getItem(`flowverse:scenario-presets:${version.flowId}`)).toBe("[]");
  });
});
