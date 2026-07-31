import { beforeEach, describe, expect, it } from "vitest";
import { DEMO_DOCUMENT } from "@/lib/demo-flow";
import { useFlowStore } from "./flow-store";

describe("flow store", () => {
  beforeEach(() => {
    useFlowStore.getState().loadDocument(structuredClone(DEMO_DOCUMENT));
  });

  it("registra cambios y permite deshacer/rehacer", () => {
    const initialCount = useFlowStore.getState().document.definition.nodes.length;
    useFlowStore.getState().addNode("process");
    expect(useFlowStore.getState().document.definition.nodes).toHaveLength(initialCount + 1);
    expect(useFlowStore.getState().saveStatus).toBe("dirty");
    useFlowStore.getState().undo();
    expect(useFlowStore.getState().document.definition.nodes).toHaveLength(initialCount);
    useFlowStore.getState().redo();
    expect(useFlowStore.getState().document.definition.nodes).toHaveLength(initialCount + 1);
  });

  it("crea una conexión seleccionable sin permitir grupos como extremos", () => {
    const state = useFlowStore.getState();
    const created = state.addEdge("confirm", "refund");
    expect(created).toMatch(/^edge-/);
    expect(useFlowStore.getState().document.definition.edges.some((edge) => edge.id === created)).toBe(true);
    expect(useFlowStore.getState().addEdge("logistics-group", "refund")).toBeUndefined();
  });

  it("invalida la publicación cuando cambia el borrador", () => {
    useFlowStore.getState().markPublished("04fbd9ba-8dd8-4f61-93be-845f067370f9", 4);
    expect(useFlowStore.getState().document.draftMatchesPublished).toBe(true);
    useFlowStore.getState().updateFlowMeta({ description: "Una modificación posterior" });
    expect(useFlowStore.getState().document.draftMatchesPublished).toBe(false);
  });

  it("no filtra ejecuciones demo a documentos públicos o reales", () => {
    useFlowStore.getState().loadDocument({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "public-demo-pedidos",
      status: "published",
    });
    expect(useFlowStore.getState().runHistory).toEqual([]);
  });

  it("hidrata el historial sanitizado incluido en el documento", () => {
    const runHistory = [{
      id: "run-public",
      flowVersionId: "public-version",
      status: "completed" as const,
      durationMs: 25,
      visitedNodeIds: ["start", "end"],
      eventCount: 0,
    }];
    useFlowStore.getState().loadDocument({
      ...structuredClone(DEMO_DOCUMENT),
      flowId: "public-token",
      status: "published",
      runHistory,
    });
    expect(useFlowStore.getState().runHistory).toEqual(runHistory);
  });
});
