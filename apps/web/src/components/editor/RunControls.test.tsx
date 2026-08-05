import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DEMO_DOCUMENT } from "@flowverse/core";
import { createSimulationPlan } from "@flowverse/engine";
import { useFlowStore } from "@/store/flow-store";
import { RunControls } from "./RunControls";

const { controlRunMock, changeRunSpeedMock } = vi.hoisted(() => ({
  controlRunMock: vi.fn(),
  changeRunSpeedMock: vi.fn(),
}));

vi.mock("@/lib/flow-service", () => ({
  controlRun: controlRunMock,
  changeRunSpeed: changeRunSpeedMock,
}));

vi.mock("./IncidentTimeMachineDialog", () => ({
  IncidentTimeMachineDialog: ({ open }: { open: boolean }) => open
    ? <div role="dialog" aria-label="Incident Time Machine de prueba" />
    : null,
}));

describe("RunControls", () => {
  beforeEach(() => {
    controlRunMock.mockReset();
    controlRunMock.mockResolvedValue(undefined);
    changeRunSpeedMock.mockReset();
    changeRunSpeedMock.mockResolvedValue(undefined);
    useFlowStore.getState().loadDocument(structuredClone(DEMO_DOCUMENT));
  });

  it("abre la configuración desde el estado inicial", async () => {
    const user = userEvent.setup();
    const configure = vi.fn();
    render(<RunControls onConfigureRun={configure} onOpenHistory={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Ejecutar flujo" }));
    expect(configure).toHaveBeenCalledOnce();
  });

  it("abre Scenario Lab desde los controles del editor", async () => {
    const user = userEvent.setup();
    const openLab = vi.fn();
    render(<RunControls onConfigureRun={vi.fn()} onOpenHistory={vi.fn()} onOpenScenarioLab={openLab} />);
    await user.click(screen.getByRole("button", { name: "Scenario Lab" }));
    expect(openLab).toHaveBeenCalledOnce();
  });

  it("pausa y avanza una ejecución", async () => {
    const user = userEvent.setup();
    const plan = createSimulationPlan(
      DEMO_DOCUMENT.definition,
      { payment: { status: "approved" }, inventory: { available: true } },
    );
    useFlowStore.getState().startSimulation(plan);
    render(<RunControls onConfigureRun={vi.fn()} onOpenHistory={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Pausar" }));
    expect(useFlowStore.getState().runStatus).toBe("paused");
    await user.click(screen.getByRole("button", { name: "Avanzar un evento" }));
    expect(useFlowStore.getState().eventCursor).toBe(1);
  });

  it("cancela el run remoto antes de reiniciar la interfaz", async () => {
    const user = userEvent.setup();
    const runId = "03fbd9ba-8dd8-4f61-93be-845f067370f9";
    useFlowStore.getState().startRemoteSimulation(runId);
    render(<RunControls onConfigureRun={vi.fn()} onOpenHistory={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Reiniciar simulación" }));
    expect(controlRunMock).toHaveBeenCalledWith(runId, "cancel");
    expect(useFlowStore.getState().runStatus).toBe("idle");
  });

  it("abre Incident Time Machine desde una ejecución remota", async () => {
    const user = userEvent.setup();
    useFlowStore.getState().startRemoteSimulation("03fbd9ba-8dd8-4f61-93be-845f067370f9");
    render(<RunControls onConfigureRun={vi.fn()} onOpenHistory={vi.fn()} />);

    await user.click(screen.getByRole("button", { name: "Abrir Incident Time Machine" }));

    expect(screen.getByRole("dialog", { name: "Incident Time Machine de prueba" })).toBeInTheDocument();
  });

  it("conserva el estado remoto y muestra el error si pausar falla", async () => {
    const user = userEvent.setup();
    controlRunMock.mockRejectedValueOnce(new Error("La API rechazó la pausa."));
    useFlowStore.getState().startRemoteSimulation("03fbd9ba-8dd8-4f61-93be-845f067370f9");
    render(<RunControls onConfigureRun={vi.fn()} onOpenHistory={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Pausar" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("La API rechazó la pausa."));
    expect(useFlowStore.getState().runStatus).toBe("running");
  });
});
