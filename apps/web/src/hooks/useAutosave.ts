"use client";

import { useEffect, useRef } from "react";
import type { EditableFlow } from "@flowverse/core";
import { saveFlow } from "@/lib/flow-service";
import { useFlowStore } from "@/store/flow-store";

const AUTOSAVE_DELAY_MS = 900;

export function useAutosave(enabled = true) {
  const document = useFlowStore((state) => state.document);
  const saveStatus = useFlowStore((state) => state.saveStatus);

  const enabledRef = useRef(enabled);
  const mountedRef = useRef(false);
  const timerRef = useRef<number | undefined>(undefined);
  const inFlightRef = useRef(false);
  const pendingDocumentsRef = useRef(new Map<string, EditableFlow>());
  const flushFlowIdsRef = useRef(new Set<string>());
  const keepaliveFlowIdsRef = useRef(new Set<string>());
  const saveNextRef = useRef<(preferredFlowId?: string) => void>(() => undefined);
  const scheduleRef = useRef<(next: EditableFlow, immediate?: boolean, keepalive?: boolean) => void>(() => undefined);

  enabledRef.current = enabled;

  const clearTimer = () => {
    if (timerRef.current === undefined) return;
    window.clearTimeout(timerRef.current);
    timerRef.current = undefined;
  };

  scheduleRef.current = (next, immediate = false, keepalive = false) => {
    pendingDocumentsRef.current.set(next.flowId, next);
    if (immediate) flushFlowIdsRef.current.add(next.flowId);
    if (keepalive) keepaliveFlowIdsRef.current.add(next.flowId);
    clearTimer();
    if (inFlightRef.current) return;
    if (immediate) {
      saveNextRef.current(next.flowId);
      return;
    }
    timerRef.current = window.setTimeout(() => {
      timerRef.current = undefined;
      saveNextRef.current();
    }, AUTOSAVE_DELAY_MS);
  };

  saveNextRef.current = (preferredFlowId) => {
    if (inFlightRef.current) return;
    const pending = pendingDocumentsRef.current;
    const snapshot = preferredFlowId
      ? pending.get(preferredFlowId)
      : pending.values().next().value as EditableFlow | undefined;
    if (!snapshot) return;

    pending.delete(snapshot.flowId);
    flushFlowIdsRef.current.delete(snapshot.flowId);
    const keepalive = keepaliveFlowIdsRef.current.delete(snapshot.flowId);
    inFlightRef.current = true;
    const stateAtStart = useFlowStore.getState();
    if (stateAtStart.document.flowId === snapshot.flowId) stateAtStart.markSaving();

    void saveFlow(snapshot, { keepalive })
      .then((result) => {
        useFlowStore.getState().completeAutosave(
          snapshot,
          result.revision,
          result.etag,
          result.source,
        );

        // Los cambios hechos durante el request conservan el ETag con el que
        // empezó. Se encadenan a la versión recién persistida para que el
        // siguiente PUT no produzca un falso conflicto.
        const queued = pendingDocumentsRef.current.get(snapshot.flowId);
        if (queued) {
          pendingDocumentsRef.current.set(snapshot.flowId, {
            ...queued,
            revision: result.revision,
            etag: result.etag,
          });
        }
      })
      .catch((error: unknown) => {
        const current = useFlowStore.getState();
        if (current.document.flowId === snapshot.flowId) {
          current.markSaveError(error instanceof Error && error.message === "conflict");
        }
        // No reintentamos a ciegas: una respuesta perdida puede significar
        // que el servidor sí guardó y repetirla con el ETag viejo generaría
        // un conflicto. Un flujo distinto que esté esperando sí puede seguir.
        pendingDocumentsRef.current.delete(snapshot.flowId);
        flushFlowIdsRef.current.delete(snapshot.flowId);
        keepaliveFlowIdsRef.current.delete(snapshot.flowId);
      })
      .finally(() => {
        inFlightRef.current = false;
        const queued = pendingDocumentsRef.current;
        if (queued.size === 0) return;
        const immediateFlowId = [...flushFlowIdsRef.current][0];
        const next = immediateFlowId
          ? queued.get(immediateFlowId)
          : queued.values().next().value as EditableFlow | undefined;
        if (!next) return;
        scheduleRef.current(next, Boolean(immediateFlowId) || !mountedRef.current, keepaliveFlowIdsRef.current.has(next.flowId));
      });
  };

  useEffect(() => {
    mountedRef.current = true;
    const flushBeforePageExit = () => {
      if (!enabledRef.current) return;
      const current = useFlowStore.getState();
      if (current.saveStatus === "dirty") {
        pendingDocumentsRef.current.set(current.document.flowId, current.document);
      }
      for (const flowId of pendingDocumentsRef.current.keys()) {
        flushFlowIdsRef.current.add(flowId);
        keepaliveFlowIdsRef.current.add(flowId);
      }
      clearTimer();
      saveNextRef.current(current.document.flowId);
    };
    window.addEventListener("pagehide", flushBeforePageExit);
    window.addEventListener("beforeunload", flushBeforePageExit);
    return () => {
      mountedRef.current = false;
      window.removeEventListener("pagehide", flushBeforePageExit);
      window.removeEventListener("beforeunload", flushBeforePageExit);
      clearTimer();
      if (!enabledRef.current) return;

      const current = useFlowStore.getState();
      if (current.saveStatus === "dirty") {
        // React no puede esperar este trabajo en el cleanup, pero iniciar el
        // request aquí evita perder el último debounce al navegar dentro de
        // la aplicación. Si ya hay uno en curso, queda serializado detrás.
        scheduleRef.current(current.document, true);
      }
      for (const flowId of pendingDocumentsRef.current.keys()) flushFlowIdsRef.current.add(flowId);
      saveNextRef.current();
    };
  }, []);

  useEffect(() => {
    const flowId = document.flowId;
    return () => {
      clearTimer();
      if (!enabled || !pendingDocumentsRef.current.has(flowId)) return;
      flushFlowIdsRef.current.add(flowId);
      saveNextRef.current(flowId);
    };
  }, [document.flowId, enabled]);

  useEffect(() => {
    if (!enabled || saveStatus !== "dirty") return;
    scheduleRef.current(document);
    return clearTimer;
  }, [document, enabled, saveStatus]);
}
