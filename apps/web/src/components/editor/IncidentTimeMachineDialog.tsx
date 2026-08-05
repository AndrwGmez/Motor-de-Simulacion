"use client";

import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import {
  getRunIncident,
  reconstructIncidentState,
  type IncidentNodeState,
  type IncidentTimelineFrame,
} from "@/lib/incident-service";
import { Modal } from "./EditorDialogs";

const NODE_STATE_LABELS: Record<IncidentNodeState, string> = {
  queued: "En cola",
  running: "En curso",
  waiting: "En espera",
  completed: "Completado",
  failed: "Falló",
  skipped: "Omitido",
};

const EVENT_LABELS: Record<string, string> = {
  "run.started": "Ejecución iniciada",
  "run.paused": "Ejecución pausada",
  "run.resumed": "Ejecución reanudada",
  "run.completed": "Ejecución completada",
  "run.failed": "Ejecución fallida",
  "run.limit_exceeded": "Límite excedido",
  "run.interrupted": "Ejecución interrumpida",
  "run.cancelled": "Ejecución cancelada",
  "node.queued": "Nodo en cola",
  "node.started": "Nodo iniciado",
  "node.waiting": "Nodo en espera",
  "node.completed": "Nodo completado",
  "node.failed": "Nodo fallido",
  "node.skipped": "Nodo omitido",
  "edge.traversed": "Conexión recorrida",
};

const RUN_STATUS_LABELS: Record<string, string> = {
  created: "Creada",
  running: "En ejecución",
  paused: "Pausada",
  completed: "Completada",
  failed: "Fallida",
  cancelled: "Cancelada",
};
const EMPTY_TIMELINE: IncidentTimelineFrame[] = [];

interface IncidentTimeMachineDialogProps {
  open: boolean;
  onClose: () => void;
  runId: string;
  onSelectNode?: (nodeId: string) => void;
}

function initialFrameIndex(timeline: IncidentTimelineFrame[], rootSequence?: number): number {
  if (timeline.length === 0) return -1;
  if (rootSequence !== undefined) {
    const rootIndex = timeline.findIndex((frame) => frame.sequence === rootSequence);
    if (rootIndex >= 0) return rootIndex;
  }
  return timeline.length - 1;
}

function timelineWindow(total: number, selected: number): { start: number; end: number } {
  const visible = 9;
  const start = Math.max(0, Math.min(selected - 4, total - visible));
  return { start, end: Math.min(total, start + visible) };
}

function formatLogicalTime(milliseconds: number): string {
  if (milliseconds < 1_000) return `${milliseconds} ms`;
  return `${(milliseconds / 1_000).toFixed(milliseconds < 10_000 ? 2 : 1)} s`;
}

function formatEvent(frame: IncidentTimelineFrame): string {
  return EVENT_LABELS[frame.type] ?? frame.type;
}

export function IncidentTimeMachineDialog({
  open,
  onClose,
  runId,
  onSelectNode,
}: IncidentTimeMachineDialogProps) {
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const [playing, setPlaying] = useState(false);
  const incidentQuery = useQuery({
    queryKey: ["run-incident", runId],
    queryFn: () => getRunIncident(runId),
    enabled: open && Boolean(runId),
    staleTime: 5_000,
  });
  const report = incidentQuery.data;
  const timeline = report?.timeline ?? EMPTY_TIMELINE;

  useEffect(() => {
    if (!open || !report) return;
    setPlaying(false);
    setSelectedIndex(initialFrameIndex(report.timeline, report.rootCause?.sequence));
  }, [open, report]);

  useEffect(() => {
    if (!playing || selectedIndex < 0) return;
    if (selectedIndex >= timeline.length - 1) {
      setPlaying(false);
      return;
    }
    const timeout = window.setTimeout(() => setSelectedIndex((index) => Math.min(index + 1, timeline.length - 1)), 800);
    return () => window.clearTimeout(timeout);
  }, [playing, selectedIndex, timeline.length]);

  const selectedFrame = selectedIndex >= 0 ? timeline[selectedIndex] : undefined;
  const replay = useMemo(
    () => reconstructIncidentState(timeline, selectedFrame?.sequence ?? 0, selectedIndex),
    [selectedFrame?.sequence, selectedIndex, timeline],
  );
  const nodeVisits = replay.nodeVisits;
  const windowRange = timelineWindow(timeline.length, Math.max(0, selectedIndex));
  const visibleFrames = timeline.slice(windowRange.start, windowRange.end);

  useEffect(() => {
    if (open && selectedFrame?.nodeId) onSelectNode?.(selectedFrame.nodeId);
  }, [onSelectNode, open, selectedFrame?.nodeId, selectedFrame?.sequence]);

  function navigateTo(index: number) {
    setPlaying(false);
    setSelectedIndex(Math.max(0, Math.min(index, timeline.length - 1)));
  }

  function close() {
    setPlaying(false);
    onClose();
  }

  return (
    <Modal open={open} onClose={close} eyebrow="OBSERVABILIDAD FORENSE" title="Incident Time Machine" wide>
      {incidentQuery.isLoading ? (
        <div className="incident-query-state" role="status">
          <span className="scene-loader" />
          <strong>Reconstruyendo cada decisión…</strong>
          <small>Ordenamos la evidencia por secuencia y tiempo lógico.</small>
        </div>
      ) : incidentQuery.error ? (
        <div className="incident-query-state error" role="alert">
          <span aria-hidden="true">!</span>
          <strong>No pudimos abrir la máquina del tiempo</strong>
          <small>{incidentQuery.error instanceof Error ? incidentQuery.error.message : "Inténtalo nuevamente."}</small>
          <button type="button" className="secondary-button" onClick={() => void incidentQuery.refetch()}>Reintentar</button>
        </div>
      ) : report ? (
        <div className="incident-machine">
          <section className="incident-hero" aria-label="Resumen del incidente">
            <div className="incident-orbit" aria-hidden="true"><i /><i /><span>↶</span></div>
            <div>
              <span className="eyebrow">REPLAY DETERMINISTA</span>
              <strong>Vuelve al instante exacto en que cambió la ejecución</strong>
              <p>Inspecciona la evidencia, avanza evento por evento y reconstruye el estado sin alterar el run original.</p>
            </div>
            <div className="incident-identifiers">
              <span className={`incident-status status-${report.status}`}>{RUN_STATUS_LABELS[report.status] ?? report.status}</span>
              <small>TRACE ID</small>
              <code title={report.traceId ?? "Sin traceId"}>{report.traceId ?? "No disponible"}</code>
            </div>
          </section>

          <section className="incident-evidence-strip" aria-label="Integridad y causa raíz">
            <article className={report.integrity.complete ? "is-complete" : "is-incomplete"}>
              <span aria-hidden="true">{report.integrity.complete ? "✓" : "!"}</span>
              <div>
                <small>INTEGRIDAD</small>
                <strong>{report.integrity.complete ? "Evidencia completa" : "Evidencia incompleta"}</strong>
                <p>
                  {report.integrity.missingSequences.length} faltantes · {report.integrity.duplicateSequences.length} duplicadas
                </p>
              </div>
            </article>
            <article>
              <span aria-hidden="true">◎</span>
              <div>
                <small>VENTANA OBSERVADA</small>
                <strong>{report.summary.eventCount} eventos</strong>
                <p>#{report.integrity.firstSequence} → #{report.integrity.lastSequence} · {formatLogicalTime(report.summary.logicalDurationMs)}</p>
              </div>
            </article>
            <article className={report.rootCause ? "has-root-cause" : ""}>
              <span aria-hidden="true">{report.rootCause ? "⚡" : "◇"}</span>
              <div>
                <small>CAUSA RAÍZ</small>
                <strong>{report.rootCause?.code || (report.rootCause ? formatEventType(report.rootCause.type) : "Sin fallo detectado")}</strong>
                <p>{report.rootCause?.message ?? "La ejecución no contiene un evento de fallo."}</p>
              </div>
              {report.rootCause && timeline.length > 0 && (
                <button
                  type="button"
                  onClick={() => navigateTo(initialFrameIndex(timeline, report.rootCause?.sequence))}
                  aria-label={`Ir a la causa raíz, secuencia ${report.rootCause.sequence}`}
                >Ir a #{report.rootCause.sequence}</button>
              )}
            </article>
          </section>

          {timeline.length > 0 && selectedFrame ? (
            <>
              <section className="incident-transport" aria-label="Controles de reproducción">
                <div className="incident-playback-buttons">
                  <button type="button" onClick={() => navigateTo(selectedIndex - 1)} disabled={selectedIndex <= 0} aria-label="Evento anterior">‹</button>
                  <button
                    type="button"
                    className={playing ? "is-playing" : ""}
                    onClick={() => {
                      if (playing) {
                        setPlaying(false);
                      } else {
                        if (selectedIndex >= timeline.length - 1) setSelectedIndex(0);
                        setPlaying(true);
                      }
                    }}
                    aria-label={playing ? "Pausar reproducción" : "Reproducir línea temporal"}
                  >{playing ? "Ⅱ" : "▶"}</button>
                  <button type="button" onClick={() => navigateTo(selectedIndex + 1)} disabled={selectedIndex >= timeline.length - 1} aria-label="Evento siguiente">›</button>
                </div>
                <label className="incident-scrubber" htmlFor="incident-sequence-scrubber">
                  <span>Secuencia</span>
                  <input
                    id="incident-sequence-scrubber"
                    type="range"
                    min={0}
                    max={timeline.length - 1}
                    value={selectedIndex}
                    onChange={(event) => navigateTo(Number(event.target.value))}
                    aria-label="Secuencia del incidente"
                  />
                </label>
                <output htmlFor="incident-sequence-scrubber" aria-live="polite">
                  <strong>#{selectedFrame.sequence}</strong>
                  <small>{selectedIndex + 1} de {timeline.length}</small>
                </output>
              </section>

              <div className="incident-replay-grid">
                <aside aria-label="Línea temporal de eventos">
                  <header>
                    <div><span className="eyebrow">TIMELINE</span><strong>Evidencia ordenada</strong></div>
                    <small>{windowRange.start + 1}–{windowRange.end} de {timeline.length}</small>
                  </header>
                  <ol className="incident-timeline">
                    {visibleFrames.map((frame, offset) => {
                      const frameIndex = windowRange.start + offset;
                      return (
                        <li key={`${frame.sequence}-${frame.occurredAt}-${frameIndex}`}>
                          <button
                            type="button"
                            className={`${frame.category} ${frameIndex === selectedIndex ? "selected" : ""}`}
                            onClick={() => navigateTo(frameIndex)}
                            aria-current={frameIndex === selectedIndex ? "step" : undefined}
                          >
                            <i aria-hidden="true" />
                            <span><strong>{formatEvent(frame)}</strong><small>{frame.nodeId ?? frame.edgeId ?? frame.category}</small></span>
                            <time dateTime={frame.occurredAt}>#{frame.sequence}<small>{formatLogicalTime(frame.logicalTimeMs)}</small></time>
                          </button>
                        </li>
                      );
                    })}
                  </ol>
                </aside>

                <main>
                  <header className="incident-frame-heading">
                    <div>
                      <span className={`incident-category category-${selectedFrame.category}`}>{selectedFrame.category}</span>
                      <code>{selectedFrame.type}</code>
                      <h3>{formatEvent(selectedFrame)}</h3>
                      {selectedFrame.message && <p>{selectedFrame.message}</p>}
                    </div>
                    <div>
                      <span>TIEMPO LÓGICO</span>
                      <strong>{formatLogicalTime(selectedFrame.logicalTimeMs)}</strong>
                      <time dateTime={selectedFrame.occurredAt}>{new Intl.DateTimeFormat("es-CO", { dateStyle: "short", timeStyle: "medium" }).format(new Date(selectedFrame.occurredAt))}</time>
                    </div>
                  </header>

                  <section className="incident-reconstructed" aria-labelledby="reconstructed-state-title">
                    <header>
                      <div>
                        <span className="eyebrow">SNAPSHOT RECONSTRUIDO</span>
                        <h4 id="reconstructed-state-title">Estado reconstruido hasta #{selectedFrame.sequence}</h4>
                      </div>
                      <span className={`replay-run-status status-${replay.runStatus}`}>{RUN_STATUS_LABELS[replay.runStatus] ?? replay.runStatus}</span>
                    </header>
                    <dl className="incident-state-metrics">
                      <div><dt>Eventos aplicados</dt><dd>{replay.appliedEvents}</dd></div>
                      <div><dt>Visitas de nodo</dt><dd>{nodeVisits.length}</dd></div>
                      <div><dt>Conexiones recorridas</dt><dd>{replay.traversedEdgeIds.length}</dd></div>
                    </dl>

                    <div className="incident-node-state-list" aria-label="Estado de los nodos">
                      {nodeVisits.length > 0 ? nodeVisits.map((visit) => (
                        <button
                          type="button"
                          key={visit.id}
                          className={`node-state-${visit.state}`}
                          onClick={() => onSelectNode?.(visit.nodeId)}
                          disabled={!onSelectNode}
                          title={onSelectNode ? `Seleccionar ${visit.nodeId} en el lienzo` : undefined}
                        >
                          <i aria-hidden="true" />
                          <span>
                            <strong>{visit.nodeId}{visit.visit > 1 ? ` · visita ${visit.visit}` : ""}</strong>
                            <small>{NODE_STATE_LABELS[visit.state]}{visit.tokenId ? ` · ${visit.tokenId}` : ""}</small>
                          </span>
                        </button>
                      )) : <p>Aún no hay cambios de estado en nodos para este punto.</p>}
                    </div>

                    {replay.traversedEdgeIds.length > 0 && (
                      <div className="incident-edge-path">
                        <span>Recorrido reconstruido (con repeticiones)</span>
                        <div>{replay.traversedEdgeIds.map((edgeId, index) => <code key={`${edgeId}-${index}`}>{edgeId}</code>)}</div>
                      </div>
                    )}
                  </section>

                  <details className="incident-payload">
                    <summary>Payload del evento <span>{Object.keys(selectedFrame.payload).length} campos</span></summary>
                    <pre>{JSON.stringify(selectedFrame.payload, null, 2)}</pre>
                  </details>
                </main>
              </div>
            </>
          ) : (
            <div className="incident-query-state empty">
              <span aria-hidden="true">◇</span>
              <strong>El run todavía no tiene eventos</strong>
              <small>La integridad permanecerá incompleta hasta que se registre la primera secuencia.</small>
            </div>
          )}
        </div>
      ) : null}
    </Modal>
  );
}

function formatEventType(type: string): string {
  return EVENT_LABELS[type] ?? type;
}
