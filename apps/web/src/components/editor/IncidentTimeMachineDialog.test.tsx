import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getRunIncident, type IncidentReport } from "@/lib/incident-service";
import { IncidentTimeMachineDialog } from "./IncidentTimeMachineDialog";

vi.mock("@/lib/incident-service", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/incident-service")>();
  return { ...actual, getRunIncident: vi.fn() };
});

const getRunIncidentMock = vi.mocked(getRunIncident);
const RUN_ID = "03fbd9ba-8dd8-4f61-93be-845f067370f9";

const REPORT: IncidentReport = {
  schemaVersion: "1.0",
  runId: RUN_ID,
  traceId: "0123456789abcdef0123456789abcdef",
  flowId: "02fbd9ba-8dd8-4f61-93be-845f067370f9",
  flowVersionId: "04fbd9ba-8dd8-4f61-93be-845f067370f9",
  status: "failed",
  createdAt: "2026-08-04T14:00:00.000Z",
  completedAt: "2026-08-04T14:00:01.000Z",
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
  timeline: [
    { sequence: 1, occurredAt: "2026-08-04T14:00:00.000Z", logicalTimeMs: 0, type: "run.started", category: "run", payload: {} },
    { sequence: 2, occurredAt: "2026-08-04T14:00:00.200Z", logicalTimeMs: 10, type: "node.started", category: "node", nodeId: "payment", payload: { nodeId: "payment" } },
    { sequence: 3, occurredAt: "2026-08-04T14:00:00.500Z", logicalTimeMs: 60, type: "edge.traversed", category: "edge", edgeId: "checkout-payment", payload: { edgeId: "checkout-payment" } },
    { sequence: 4, occurredAt: "2026-08-04T14:00:00.900Z", logicalTimeMs: 120, type: "node.failed", category: "node", nodeId: "payment", message: "Provider timed out", payload: { nodeId: "payment", code: "provider.timeout" } },
    { sequence: 5, occurredAt: "2026-08-04T14:00:01.000Z", logicalTimeMs: 120, type: "run.failed", category: "run", message: "provider timeout", payload: { error: "provider timeout" } },
  ],
};

function renderDialog(overrides: Partial<React.ComponentProps<typeof IncidentTimeMachineDialog>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const props = {
    open: true,
    runId: RUN_ID,
    onClose: vi.fn(),
    onSelectNode: vi.fn(),
    ...overrides,
  };
  render(
    <QueryClientProvider client={client}>
      <IncidentTimeMachineDialog {...props} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  getRunIncidentMock.mockReset();
  getRunIncidentMock.mockResolvedValue(structuredClone(REPORT));
});

afterEach(() => {
  vi.useRealTimers();
});

describe("IncidentTimeMachineDialog", () => {
  it("expone evidencia, integridad, trace y abre en la causa raíz", async () => {
    const onSelectNode = vi.fn();
    renderDialog({ onSelectNode });

    expect(await screen.findByRole("dialog", { name: "Incident Time Machine" })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Estado reconstruido hasta #4" })).toBeInTheDocument();
    expect(screen.getByText("provider.timeout")).toBeInTheDocument();
    expect(screen.getByText("Evidencia completa")).toBeInTheDocument();
    expect(screen.getByTitle("0123456789abcdef0123456789abcdef")).toBeInTheDocument();
    expect(screen.getByText("Falló")).toBeInTheDocument();
    expect(getRunIncidentMock).toHaveBeenCalledWith(RUN_ID);
    await waitFor(() => expect(onSelectNode).toHaveBeenCalledWith("payment"));
  });

  it("navega, reconstruye el estado y selecciona el nodo en el lienzo", async () => {
    const user = userEvent.setup();
    const onSelectNode = vi.fn();
    renderDialog({ onSelectNode });
    await screen.findByRole("heading", { name: "Estado reconstruido hasta #4" });
    onSelectNode.mockClear();

    await user.click(screen.getByRole("button", { name: /Nodo iniciado/ }));

    expect(screen.getByRole("heading", { name: "Estado reconstruido hasta #2" })).toBeInTheDocument();
    expect(screen.getByText("En curso")).toBeInTheDocument();
    expect(screen.getByText("2", { selector: ".incident-state-metrics dd" })).toBeInTheDocument();
    await waitFor(() => expect(onSelectNode).toHaveBeenCalledWith("payment"));

    fireEvent.change(screen.getByRole("slider", { name: "Secuencia del incidente" }), { target: { value: "0" } });
    expect(screen.getByRole("heading", { name: "Estado reconstruido hasta #1" })).toBeInTheDocument();
    expect(screen.getByText("1", { selector: ".incident-state-metrics dd" })).toBeInTheDocument();
  });

  it("reproduce la timeline y permite pausarla", async () => {
    renderDialog();
    await screen.findByRole("heading", { name: "Estado reconstruido hasta #4" });
    fireEvent.change(screen.getByRole("slider", { name: "Secuencia del incidente" }), { target: { value: "0" } });
    vi.useFakeTimers();

    fireEvent.click(screen.getByRole("button", { name: "Reproducir línea temporal" }));
    act(() => vi.advanceTimersByTime(800));

    expect(screen.getByRole("heading", { name: "Estado reconstruido hasta #2" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pausar reproducción" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Pausar reproducción" }));
    expect(screen.getByRole("button", { name: "Reproducir línea temporal" })).toBeInTheDocument();
  });

  it("ofrece recuperación cuando el endpoint falla", async () => {
    getRunIncidentMock.mockRejectedValueOnce(new Error("No tienes acceso a esta ejecución."));
    renderDialog();

    expect(await screen.findByRole("alert")).toHaveTextContent("No tienes acceso a esta ejecución.");
    expect(screen.getByRole("button", { name: "Reintentar" })).toBeInTheDocument();
  });
});
