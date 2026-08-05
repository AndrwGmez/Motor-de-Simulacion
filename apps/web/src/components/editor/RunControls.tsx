"use client";

import { useCallback, useState } from "react";
import { useFlowStore } from "@/store/flow-store";
import { changeRunSpeed, controlRun } from "@/lib/flow-service";
import { IncidentTimeMachineDialog } from "./IncidentTimeMachineDialog";

interface RunControlsProps {
  readOnly?: boolean;
  publicView?: boolean;
  onConfigureRun: () => void;
  onOpenHistory: () => void;
  onOpenScenarioLab?: () => void;
}

export function RunControls({ readOnly, publicView, onConfigureRun, onOpenHistory, onOpenScenarioLab }: RunControlsProps) {
  const runStatus = useFlowStore((state) => state.runStatus);
  const runSource = useFlowStore((state) => state.runSource);
  const remoteRunId = useFlowStore((state) => state.remoteRunId);
  const streamStatus = useFlowStore((state) => state.streamStatus);
  const eventCursor = useFlowStore((state) => state.eventCursor);
  const eventCount = useFlowStore((state) => state.plannedEvents.length);
  const speed = useFlowStore((state) => state.speed);
  const visibleEvents = useFlowStore((state) => state.visibleEvents);
  const pause = useFlowStore((state) => state.pauseSimulation);
  const resume = useFlowStore((state) => state.resumeSimulation);
  const step = useFlowStore((state) => state.applyNextEvent);
  const reset = useFlowStore((state) => state.resetSimulation);
  const setSpeed = useFlowStore((state) => state.setSpeed);
  const flowNodes = useFlowStore((state) => state.document.definition.nodes);
  const selectNode = useFlowStore((state) => state.selectNode);
  const [pending, setPending] = useState(false);
  const [controlError, setControlError] = useState("");
  const [incidentOpen, setIncidentOpen] = useState(false);
  const progress = eventCount ? Math.min(100, (eventCursor / eventCount) * 100) : 0;
  const current = visibleEvents.at(-1);
  const selectIncidentNode = useCallback((nodeId: string) => {
    if (flowNodes.some((node) => node.id === nodeId)) selectNode(nodeId);
  }, [flowNodes, selectNode]);

  if (readOnly) {
    return (
      <section className="run-controls public-run-controls" aria-label="Información de visualización pública">
        <div className="public-mode-mark">◎</div>
        <div>
          <span className="eyebrow">{publicView ? "VISUALIZACIÓN PÚBLICA" : "MODO DE CONSULTA"}</span>
          <strong>Versión de solo lectura</strong>
          <small>{publicView
            ? "Puedes explorar nodos y consultar ejecuciones compartidas sin modificar el flujo."
            : "Tu rol permite explorar el flujo y consultar su historial sin modificarlo."}</small>
        </div>
        <button type="button" className="secondary-button" onClick={onOpenHistory}>Ver historial compartido</button>
      </section>
    );
  }

  return (
    <>
      <section className="run-controls" aria-label="Controles de simulación">
      <div className="run-primary-controls">
        <button
          type="button"
          className="run-main-button"
          disabled={pending}
          onClick={async () => {
            if (runStatus !== "running" && runStatus !== "paused") {
              onConfigureRun();
              return;
            }
            setPending(true);
            setControlError("");
            try {
              if (runStatus === "running") {
                if (runSource === "api" && remoteRunId) await controlRun(remoteRunId, "pause");
                pause();
              } else {
                if (runSource === "api" && remoteRunId) await controlRun(remoteRunId, "resume");
                resume();
              }
            } catch (cause) {
              setControlError(cause instanceof Error ? cause.message : "No se pudo controlar la ejecución.");
            } finally {
              setPending(false);
            }
          }}
          aria-label={runStatus === "running" ? "Pausar" : runStatus === "paused" ? "Reanudar" : "Ejecutar flujo"}
        >
          {runStatus === "running" ? "Ⅱ" : "▶"}
        </button>
        <button
          type="button"
          className="control-icon-button"
          onClick={async () => {
            setPending(true);
            setControlError("");
            try {
              if (runSource === "api" && remoteRunId) await controlRun(remoteRunId, "step");
              else step();
            } catch (cause) {
              setControlError(cause instanceof Error ? cause.message : "No se pudo avanzar la ejecución.");
            } finally {
              setPending(false);
            }
          }}
          disabled={runStatus !== "paused" || pending}
          title="Avanzar un evento"
          aria-label="Avanzar un evento"
        >▷|</button>
        <button
          type="button"
          className="control-icon-button"
          onClick={async () => {
            setPending(true);
            setControlError("");
            try {
              if (runSource === "api" && remoteRunId && (runStatus === "running" || runStatus === "paused")) {
                await controlRun(remoteRunId, "cancel");
              }
              reset();
            } catch (cause) {
              setControlError(cause instanceof Error ? cause.message : "No se pudo reiniciar la ejecución.");
            } finally {
              setPending(false);
            }
          }}
          disabled={runStatus === "idle" || pending}
          title="Reiniciar"
          aria-label="Reiniciar simulación"
        >↺</button>
        {remoteRunId && (
          <button
            type="button"
            className="incident-machine-trigger"
            onClick={() => setIncidentOpen(true)}
            aria-label="Abrir Incident Time Machine"
          >
            <span aria-hidden="true">↶</span> Time Machine
          </button>
        )}
      </div>

      <div className="run-progress">
        <div className="run-progress-meta">
          <span className={`run-state state-${runStatus}`}>
            <i />{runStatus === "idle" ? "Lista para simular" : runStatus}
            {runSource === "api" && <em> · {streamStatus}</em>}
          </span>
          <span>{eventCursor} / {eventCount || "—"} eventos</span>
        </div>
        <div className="progress-track"><i style={{ width: `${progress}%` }} /></div>
        {controlError
          ? <small className="run-control-error" role="alert">{controlError}</small>
          : <small>{current ? describeEvent(current.type, current.payload.nodeId) : "La ejecución utiliza tiempo lógico determinista."}</small>}
      </div>

      <div className="run-secondary-controls">
        <label className="speed-control">
          <span>Velocidad</span>
          <select
            value={speed}
            disabled={pending}
            onChange={async (event) => {
              const value = Number(event.target.value);
              setPending(true);
              setControlError("");
              try {
                if (runSource === "api" && remoteRunId) await changeRunSpeed(remoteRunId, value);
                setSpeed(value);
              } catch (cause) {
                setControlError(cause instanceof Error ? cause.message : "No se pudo cambiar la velocidad.");
              } finally {
                setPending(false);
              }
            }}
          >
            <option value={0.25}>0.25×</option>
            <option value={0.5}>0.5×</option>
            <option value={1}>1×</option>
            <option value={2}>2×</option>
            <option value={4}>4×</option>
          </select>
        </label>
        {!readOnly && (
          <button type="button" className="text-button" onClick={onConfigureRun}>Datos de prueba</button>
        )}
        {onOpenScenarioLab && (
          <button type="button" className="scenario-lab-trigger" onClick={onOpenScenarioLab}>
            <span aria-hidden="true">✦</span> Scenario Lab
          </button>
        )}
        <button type="button" className="text-button" onClick={onOpenHistory}>Historial</button>
      </div>
      </section>
      <IncidentTimeMachineDialog
        open={incidentOpen}
        onClose={() => setIncidentOpen(false)}
        runId={remoteRunId ?? ""}
        onSelectNode={selectIncidentNode}
      />
    </>
  );
}

function describeEvent(type: string, nodeId?: string) {
  const names: Record<string, string> = {
    "run.started": "Ejecución iniciada",
    "node.queued": "Nodo en cola",
    "node.started": "Procesando nodo",
    "edge.traversed": "Datos en tránsito",
    "node.completed": "Nodo completado",
    "node.failed": "Error en nodo",
    "run.completed": "Ejecución completada",
    "run.failed": "Ejecución fallida",
  };
  return `${names[type] ?? type}${nodeId ? ` · ${nodeId}` : ""}`;
}
