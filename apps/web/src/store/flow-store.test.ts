import { beforeEach, describe, expect, it } from "vitest";
import { DEMO_DOCUMENT } from "@flowverse/core";
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

  it("aplica una versión como borrador guardado y permite deshacer la restauración", () => {
    const store = useFlowStore.getState();
    store.markPublished("version-1", 1);
    store.selectNode("start");
    const before = structuredClone(useFlowStore.getState().document.definition);
    const restored = structuredClone(before);
    restored.name = "Versión histórica";
    restored.nodes = restored.nodes.filter((node) => node.id !== "refund");

    store.applyRestoredVersion({
      version: {
        id: "version-1",
        flowId: store.document.flowId,
        number: 1,
        checksum: "checksum-1",
        publishedAt: "2026-07-20T10:00:00.000Z",
        publishedBy: "user-1",
      },
      definition: restored,
    }, 8, '"restored-8"', "api");

    const applied = useFlowStore.getState();
    expect(applied.document.status).toBe("draft");
    expect(applied.document.versionId).toBe(DEMO_DOCUMENT.versionId);
    expect(applied.document.definition.name).toBe("Versión histórica");
    expect(applied.document.draftMatchesPublished).toBe(true);
    expect(applied.document.etag).toBe('"restored-8"');
    expect(applied.saveStatus).toBe("saved");
    expect(applied.selectedNodeId).toBeUndefined();

    applied.undo();
    expect(useFlowStore.getState().document.definition).toEqual(before);
    expect(useFlowStore.getState().saveStatus).toBe("dirty");
  });
});

// Selección múltiple: la especificación la pide en §8.1 y hoy solo se puede
// tener un nodo seleccionado, lo que impide mover grupos o borrar de una vez.
describe("selección múltiple", () => {
  it("añade nodos a la selección sin perder los anteriores", () => {
    const store = useFlowStore.getState();
    store.selectNode("start");
    store.toggleNodeSelection("end");
    expect(useFlowStore.getState().selectedNodeIds).toEqual(["start", "end"]);
  });

  it("quita un nodo ya seleccionado al volver a marcarlo", () => {
    const store = useFlowStore.getState();
    store.selectNode("start");
    store.toggleNodeSelection("end");
    store.toggleNodeSelection("start");
    expect(useFlowStore.getState().selectedNodeIds).toEqual(["end"]);
  });

  it("seleccionar sin modificador reemplaza la selección entera", () => {
    const store = useFlowStore.getState();
    store.selectNode("start");
    store.toggleNodeSelection("end");
    store.selectNode("start");
    expect(useFlowStore.getState().selectedNodeIds).toEqual(["start"]);
  });

  it("mantiene selectedNodeId apuntando al último para el inspector", () => {
    const store = useFlowStore.getState();
    store.selectNode("start");
    store.toggleNodeSelection("end");
    expect(useFlowStore.getState().selectedNodeId).toBe("end");
  });

  it("limpiar la selección la vacía por completo", () => {
    const store = useFlowStore.getState();
    store.selectNode("start");
    store.toggleNodeSelection("end");
    store.clearSelection();
    expect(useFlowStore.getState().selectedNodeIds).toEqual([]);
    expect(useFlowStore.getState().selectedNodeId).toBeUndefined();
  });

  it("seleccionar una conexión descarta la multiselección de nodos", () => {
    const store = useFlowStore.getState();
    const nodesBefore = store.document.definition.nodes.length;
    const edge = store.document.definition.edges[0];
    store.selectNode("start");
    store.toggleNodeSelection("end");

    store.selectEdge(edge.id);
    expect(useFlowStore.getState().selectedNodeIds).toEqual([]);
    useFlowStore.getState().deleteSelected();

    expect(useFlowStore.getState().document.definition.nodes).toHaveLength(nodesBefore);
    expect(useFlowStore.getState().document.definition.edges).not.toContainEqual(edge);
  });

  it("borra todos los nodos seleccionados de una vez", () => {
    const store = useFlowStore.getState();
    const nodos = useFlowStore.getState().document.definition.nodes;
    const antes = nodos.length;
    // Se usan identificadores reales del documento, no inventados: si no
    // existen, el borrado no elimina nada y la prueba mentiría.
    const [uno, dos] = nodos.filter((n) => n.type !== "group").slice(0, 2);
    store.selectNode(uno.id);
    store.toggleNodeSelection(dos.id);
    store.deleteSelected();
    expect(useFlowStore.getState().document.definition.nodes.length).toBe(antes - 2);
  });
});
