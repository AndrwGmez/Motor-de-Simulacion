"use client";

import { useEffect } from "react";
import { saveFlow } from "@/lib/flow-service";
import { useFlowStore } from "@/store/flow-store";

export function useAutosave(enabled = true) {
  const document = useFlowStore((state) => state.document);
  const saveStatus = useFlowStore((state) => state.saveStatus);
  const markSaving = useFlowStore((state) => state.markSaving);
  const markSaved = useFlowStore((state) => state.markSaved);
  const markSaveError = useFlowStore((state) => state.markSaveError);

  useEffect(() => {
    if (!enabled || saveStatus !== "dirty") return;
    const timer = window.setTimeout(async () => {
      markSaving();
      try {
        const result = await saveFlow(document);
        markSaved(result.revision, result.etag, result.source);
      } catch (error) {
        markSaveError(error instanceof Error && error.message === "conflict");
      }
    }, 900);
    return () => window.clearTimeout(timer);
  }, [document, enabled, markSaveError, markSaved, markSaving, saveStatus]);
}
