import { act, renderHook } from "@testing-library/react";
import { DEMO_DOCUMENT } from "@flowverse/core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { saveFlow } from "@/lib/flow-service";
import { useFlowStore } from "@/store/flow-store";
import { useAutosave } from "./useAutosave";

vi.mock("@/lib/flow-service", () => ({
  saveFlow: vi.fn(),
}));

type SaveResult = Awaited<ReturnType<typeof saveFlow>>;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

async function flushMicrotasks() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

describe("useAutosave", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(saveFlow).mockReset();
    useFlowStore.getState().loadDocument(structuredClone(DEMO_DOCUMENT));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("serializa cambios hechos durante un request y no los marca como guardados antes de tiempo", async () => {
    const first = deferred<SaveResult>();
    const second = deferred<SaveResult>();
    const initialRevision = useFlowStore.getState().document.revision;
    vi.mocked(saveFlow)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    renderHook(() => useAutosave());

    act(() => useFlowStore.getState().updateFlowMeta({ description: "primera versión" }));
    act(() => vi.advanceTimersByTime(900));
    expect(saveFlow).toHaveBeenCalledTimes(1);

    act(() => useFlowStore.getState().updateFlowMeta({ description: "cambio durante el request" }));
    await act(async () => {
      first.resolve({ revision: initialRevision + 1, etag: '"next-1"', source: "api" });
      await flushMicrotasks();
    });

    expect(useFlowStore.getState().saveStatus).toBe("dirty");
    act(() => vi.advanceTimersByTime(900));
    expect(saveFlow).toHaveBeenCalledTimes(2);
    expect(vi.mocked(saveFlow).mock.calls[1][0]).toMatchObject({
      revision: initialRevision + 1,
      etag: '"next-1"',
      definition: { description: "cambio durante el request" },
    });

    await act(async () => {
      second.resolve({ revision: initialRevision + 2, etag: '"next-2"', source: "api" });
      await flushMicrotasks();
    });
    expect(useFlowStore.getState().saveStatus).toBe("saved");
    expect(useFlowStore.getState().document.etag).toBe('"next-2"');
  });

  it("guarda inmediatamente el debounce pendiente al desmontar", () => {
    vi.mocked(saveFlow).mockResolvedValue({ revision: 2, etag: '"2"', source: "api" });
    const { unmount } = renderHook(() => useAutosave());
    act(() => useFlowStore.getState().updateFlowMeta({ description: "antes de salir" }));

    unmount();

    expect(saveFlow).toHaveBeenCalledTimes(1);
    expect(vi.mocked(saveFlow).mock.calls[0][0].definition.description).toBe("antes de salir");
  });

  it("encadena y guarda el último cambio al desmontar durante un request", async () => {
    const first = deferred<SaveResult>();
    vi.mocked(saveFlow)
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({ revision: 3, etag: '"3"', source: "api" });
    const { unmount } = renderHook(() => useAutosave());
    act(() => useFlowStore.getState().updateFlowMeta({ description: "primera versión" }));
    act(() => vi.advanceTimersByTime(900));
    act(() => useFlowStore.getState().updateFlowMeta({ description: "última versión" }));

    unmount();
    await act(async () => {
      first.resolve({ revision: 2, etag: '"2"', source: "api" });
      await flushMicrotasks();
    });

    expect(saveFlow).toHaveBeenCalledTimes(2);
    expect(vi.mocked(saveFlow).mock.calls[1][0]).toMatchObject({
      revision: 2,
      etag: '"2"',
      definition: { description: "última versión" },
    });
  });

  it("vacía el debounce del flujo anterior cuando cambia el documento", () => {
    vi.mocked(saveFlow).mockResolvedValue({ revision: 2, etag: '"2"', source: "api" });
    renderHook(() => useAutosave());
    act(() => useFlowStore.getState().updateFlowMeta({ description: "pendiente del flujo A" }));

    const next = structuredClone(DEMO_DOCUMENT);
    next.flowId = "otro-flujo";
    next.versionId = "otra-version";
    act(() => useFlowStore.getState().loadDocument(next));

    expect(saveFlow).toHaveBeenCalledTimes(1);
    expect(vi.mocked(saveFlow).mock.calls[0][0]).toMatchObject({
      flowId: DEMO_DOCUMENT.flowId,
      definition: { description: "pendiente del flujo A" },
    });
  });

  it("vacía el debounce cuando autosave se deshabilita durante una carga", () => {
    vi.mocked(saveFlow).mockResolvedValue({ revision: 2, etag: '"2"', source: "api" });
    const { rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useAutosave(enabled),
      { initialProps: { enabled: true } },
    );
    act(() => useFlowStore.getState().updateFlowMeta({ description: "antes de deshabilitar" }));

    rerender({ enabled: false });

    expect(saveFlow).toHaveBeenCalledTimes(1);
    expect(vi.mocked(saveFlow).mock.calls[0][0].definition.description).toBe("antes de deshabilitar");
  });

  it("solicita keepalive al salir de la página con cambios pendientes", () => {
    vi.mocked(saveFlow).mockResolvedValue({ revision: 2, etag: '"2"', source: "api" });
    renderHook(() => useAutosave());
    act(() => useFlowStore.getState().updateFlowMeta({ description: "antes de cerrar" }));

    act(() => window.dispatchEvent(new Event("pagehide")));

    expect(saveFlow).toHaveBeenCalledTimes(1);
    expect(vi.mocked(saveFlow).mock.calls[0][1]).toEqual({ keepalive: true });
  });
});
