import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DEMO_DOCUMENT } from "@flowverse/core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { askEvidenceCopilot } from "@/lib/copilot-service";
import { listFlowVersions } from "@/lib/version-service";
import { useFlowStore } from "@/store/flow-store";
import { EvidenceCopilotDialog } from "./EvidenceCopilotDialog";

vi.mock("@/lib/copilot-service", () => ({
  askEvidenceCopilot: vi.fn(),
  isEvidenceCopilotAvailable: true,
}));

vi.mock("@/lib/version-service", () => ({
  listFlowVersions: vi.fn(),
}));

vi.mock("./IncidentTimeMachineDialog", () => ({
  IncidentTimeMachineDialog: ({ open, runId, onClose }: { open: boolean; runId: string; onClose: () => void }) => open ? (
    <section role="dialog" aria-label={`Incidente ${runId}`}>
      <button type="button" onClick={onClose}>Cerrar incidente</button>
    </section>
  ) : null,
}));

const FLOW_ID = "02fbd9ba-8dd8-4f61-93be-845f067370f9";
const VERSION_ID = "04fbd9ba-8dd8-4f61-93be-845f067370f9";
const RUN_ID = "03fbd9ba-8dd8-4f61-93be-845f067370f9";

const version = {
  id: VERSION_ID,
  flowId: FLOW_ID,
  number: 4,
  checksum: "checksum-v4",
  publishedAt: "2026-08-02T10:00:00.000Z",
  publishedBy: "05fbd9ba-8dd8-4f61-93be-845f067370f9",
};

function responseFixture() {
  return {
    schemaVersion: "1.0" as const,
    provider: "openai" as const,
    summary: "La validación de pagos concentra el riesgo principal.",
    suggestions: [
      {
        title: "Conserva la línea base",
        explanation: "El resto de la topología es estable.",
        severity: "info" as const,
        confidence: "low" as const,
        evidenceIds: ["flow:summary"],
        actions: [{ kind: "none" as const, targetId: null, label: "Continuar monitoreando" }],
      },
      {
        title: "Inspecciona el primer fallo",
        explanation: "La validación y el incidente convergen en el nodo de pago.",
        severity: "critical" as const,
        confidence: "high" as const,
        evidenceIds: ["validation:payment", "incident:root-cause"],
        actions: [
          { kind: "inspect_node" as const, targetId: "validate-payment", label: "Abrir nodo de pago" },
          { kind: "open_incident" as const, targetId: RUN_ID, label: "Abrir incidente" },
        ],
      },
      {
        title: "Revisa la entrada a pagos",
        explanation: "La conexión participa en la ruta observada.",
        severity: "warning" as const,
        confidence: "medium" as const,
        evidenceIds: ["edge:e-start-payment"],
        actions: [{ kind: "inspect_edge" as const, targetId: "e-start-payment", label: "Abrir conexión" }],
      },
    ],
    limitations: ["No se incluyeron inputs, outputs ni payloads."],
    evidence: {
      schemaVersion: "1.0" as const,
      flowId: FLOW_ID,
      truncated: true,
      items: [
        { id: "flow:summary", kind: "flow" as const, summary: "Resumen estructural", facts: { nodeCount: 13, valid: false } },
        { id: "node:validate-payment", kind: "node" as const, summary: "Nodo Validar pago", nodeId: "validate-payment", facts: { type: "integration" } },
        { id: "edge:e-start-payment", kind: "edge" as const, summary: "Entrada al proveedor", edgeId: "e-start-payment", facts: { source: "start", target: "validate-payment" } },
        { id: "validation:payment", kind: "validation" as const, summary: "Configuración inválida", nodeId: "validate-payment", facts: { code: "node.invalid", severity: "error" } },
        { id: "incident:summary", kind: "incident" as const, summary: "Run fallido", facts: { runId: RUN_ID, status: "failed" } },
        { id: "incident:root-cause", kind: "incident" as const, summary: "Primer fallo registrado", nodeId: "validate-payment", facts: { sequence: 8, code: "provider.timeout" } },
      ],
    },
  };
}

function renderDialog(props: Partial<React.ComponentProps<typeof EvidenceCopilotDialog>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onClose = props.onClose ?? vi.fn();
  const onInspectNode = props.onInspectNode ?? vi.fn();
  const onInspectEdge = props.onInspectEdge ?? vi.fn();
  return {
    onClose,
    onInspectNode,
    onInspectEdge,
    ...render(
      <QueryClientProvider client={client}>
        <EvidenceCopilotDialog
          open
          onClose={onClose}
          onInspectNode={onInspectNode}
          onInspectEdge={onInspectEdge}
          {...props}
        />
      </QueryClientProvider>,
    ),
  };
}

describe("EvidenceCopilotDialog", () => {
  beforeEach(() => {
    vi.mocked(askEvidenceCopilot).mockReset().mockResolvedValue(responseFixture());
    vi.mocked(listFlowVersions).mockReset().mockResolvedValue([version]);
    useFlowStore.getState().loadDocument({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: FLOW_ID,
      definition: structuredClone(DEMO_DOCUMENT.definition),
    });
    useFlowStore.getState().startRemoteSimulation(RUN_ID);
  });

  it("envía versión y run opcionales y muestra una respuesta auditable", async () => {
    const user = userEvent.setup();
    renderDialog();
    const versionOption = await screen.findByRole("option", { name: /Versión 4/ });
    await user.selectOptions(screen.getByLabelText("Versión base para diff"), versionOption);
    expect(screen.getByLabelText("Run para evidencia de incidente")).toHaveValue(RUN_ID);

    await user.click(screen.getByRole("button", { name: "Analizar con evidencia" }));

    await waitFor(() => expect(askEvidenceCopilot).toHaveBeenCalledWith(FLOW_ID, {
      question: "¿Qué debo corregir antes de publicar?",
      baseVersionId: VERSION_ID,
      runId: RUN_ID,
    }));
    expect(await screen.findByText("! PAQUETE TRUNCADO")).toBeVisible();
    expect(screen.getByText("No se incluyeron inputs, outputs ni payloads.")).toBeVisible();
    const suggestions = screen.getByLabelText("Sugerencias del Copiloto");
    expect(within(suggestions).getAllByRole("heading")[0]).toHaveTextContent("Inspecciona el primer fallo");
    expect(within(suggestions).getByText("Confianza alta")).toBeVisible();

    await user.click(within(suggestions).getByRole("button", { name: /Validación/ }));
    const evidence = screen.getByLabelText("Detalle de evidencia seleccionada");
    expect(evidence).toHaveTextContent("Configuración inválida");
    expect(evidence).toHaveTextContent("node.invalid");
  });

  it("ejecuta acciones de nodo, conexión e incidente con las capacidades existentes", async () => {
    const user = userEvent.setup();
    const callbacks = renderDialog();
    await user.click(screen.getByRole("button", { name: "Analizar con evidencia" }));
    await screen.findByText("Inspecciona el primer fallo");

    await user.click(screen.getByRole("button", { name: "Abrir nodo de pago" }));
    expect(callbacks.onInspectNode).toHaveBeenCalledWith("validate-payment");

    await user.click(screen.getByRole("button", { name: "Abrir conexión" }));
    expect(callbacks.onInspectEdge).toHaveBeenCalledWith("e-start-payment");

    await user.click(screen.getByRole("button", { name: "Abrir incidente" }));
    expect(screen.getByRole("dialog", { name: `Incidente ${RUN_ID}` })).toBeVisible();
    expect(callbacks.onClose).toHaveBeenCalledTimes(3);
  });

  it("mantiene el compositor utilizable cuando el proveedor falla", async () => {
    const user = userEvent.setup();
    vi.mocked(askEvidenceCopilot).mockRejectedValueOnce(new Error("El proveedor está temporalmente ocupado."));
    renderDialog();

    await user.click(screen.getByRole("button", { name: "Analizar con evidencia" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("El proveedor está temporalmente ocupado.");
    expect(screen.getByLabelText("Pregunta para el Copiloto")).toBeEnabled();
    expect(screen.getByRole("button", { name: "Analizar con evidencia" })).toBeEnabled();
  });
});
