"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { FlowVersion } from "@flowverse/core";
import {
  askEvidenceCopilot,
  isEvidenceCopilotAvailable,
  type CopilotAction,
  type CopilotConfidence,
  type CopilotEvidenceItem,
  type CopilotSeverity,
  type CopilotSuggestion,
  type EvidenceCopilotResponse,
} from "@/lib/copilot-service";
import { listFlowVersions } from "@/lib/version-service";
import { useFlowStore } from "@/store/flow-store";
import { IncidentTimeMachineDialog } from "./IncidentTimeMachineDialog";
import { Modal } from "./EditorDialogs";

const EMPTY_VERSIONS: FlowVersion[] = [];
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const QUICK_QUESTIONS = [
  "¿Qué debo corregir antes de publicar?",
  "¿Dónde está el mayor riesgo operativo?",
  "Explícame los cambios y el último fallo con evidencia.",
];

const SEVERITY_LABEL: Record<CopilotSeverity, string> = {
  info: "Información",
  warning: "Atención",
  critical: "Crítica",
};

const CONFIDENCE_LABEL: Record<CopilotConfidence, string> = {
  low: "Confianza baja",
  medium: "Confianza media",
  high: "Confianza alta",
};

const SEVERITY_RANK: Record<CopilotSeverity, number> = { critical: 3, warning: 2, info: 1 };

const EVIDENCE_KIND_LABEL: Record<CopilotEvidenceItem["kind"], string> = {
  flow: "Flujo",
  analysis: "Análisis",
  validation: "Validación",
  node: "Nodo",
  edge: "Conexión",
  diff: "Diff",
  incident: "Incidente",
  event: "Evento",
};

interface EvidenceCopilotDialogProps {
  open: boolean;
  onClose: () => void;
  onInspectNode?: (nodeId: string) => void;
  onInspectEdge?: (edgeId: string) => void;
}

function shortId(value: string): string {
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-5)}` : value;
}

function versionDate(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "fecha desconocida";
  return new Intl.DateTimeFormat("es-CO", { dateStyle: "medium" }).format(date);
}

function factValue(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "string") return value || "∅";
  if (["number", "boolean"].includes(typeof value)) return String(value);
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return "Valor no serializable";
  }
}

function actionIcon(action: CopilotAction): string {
  if (action.kind === "inspect_node") return "◎";
  if (action.kind === "inspect_edge") return "⌘";
  if (action.kind === "open_incident") return "↶";
  return "◇";
}

function SuggestionCard({
  suggestion,
  index,
  evidence,
  selectedEvidenceId,
  onSelectEvidence,
  onAction,
}: {
  suggestion: CopilotSuggestion;
  index: number;
  evidence: Map<string, CopilotEvidenceItem>;
  selectedEvidenceId?: string;
  onSelectEvidence: (evidenceId: string) => void;
  onAction: (action: CopilotAction) => void;
}) {
  return (
    <article className={`copilot-suggestion severity-${suggestion.severity}`}>
      <header>
        <span aria-hidden="true">{String(index + 1).padStart(2, "0")}</span>
        <div>
          <div className="copilot-suggestion-badges">
            <b>{SEVERITY_LABEL[suggestion.severity]}</b>
            <i className={`confidence-${suggestion.confidence}`}>{CONFIDENCE_LABEL[suggestion.confidence]}</i>
          </div>
          <h3>{suggestion.title}</h3>
        </div>
      </header>
      <p>{suggestion.explanation}</p>

      <div className="copilot-citations" aria-label={`Evidencia de ${suggestion.title}`}>
        <span>CITAS VERIFICABLES</span>
        <div>
          {suggestion.evidenceIds.map((evidenceId) => {
            const item = evidence.get(evidenceId)!;
            return (
              <button
                type="button"
                key={evidenceId}
                className={selectedEvidenceId === evidenceId ? "selected" : ""}
                aria-pressed={selectedEvidenceId === evidenceId}
                onClick={() => onSelectEvidence(evidenceId)}
                title={item.summary}
              >
                <i aria-hidden="true" />
                {EVIDENCE_KIND_LABEL[item.kind]} <code>{shortId(evidenceId)}</code>
              </button>
            );
          })}
        </div>
      </div>

      {suggestion.actions.length > 0 && (
        <footer>
          {suggestion.actions.map((action, actionIndex) => action.kind === "none" ? (
            <span className="copilot-manual-action" key={`${action.label}-${actionIndex}`}>
              <i aria-hidden="true">{actionIcon(action)}</i>{action.label}
            </span>
          ) : (
            <button
              type="button"
              className={`copilot-action action-${action.kind}`}
              key={`${action.kind}-${action.targetId}-${actionIndex}`}
              onClick={() => onAction(action)}
            >
              <i aria-hidden="true">{actionIcon(action)}</i>{action.label}
            </button>
          ))}
        </footer>
      )}
    </article>
  );
}

function EvidenceInspector({
  item,
  onInspectNode,
  onInspectEdge,
}: {
  item: CopilotEvidenceItem;
  onInspectNode: (nodeId: string) => void;
  onInspectEdge: (edgeId: string) => void;
}) {
  return (
    <aside className="copilot-evidence-inspector" aria-live="polite" aria-label="Detalle de evidencia seleccionada">
      <header>
        <span className={`evidence-kind kind-${item.kind}`}>{EVIDENCE_KIND_LABEL[item.kind]}</span>
        <code title={item.id}>{item.id}</code>
        {(item.nodeId || item.edgeId) && (
          <button
            type="button"
            onClick={() => item.nodeId ? onInspectNode(item.nodeId) : onInspectEdge(item.edgeId!)}
          >
            {item.nodeId ? "Inspeccionar nodo" : "Inspeccionar conexión"} ↗
          </button>
        )}
      </header>
      <strong>{item.summary || "Evidencia estructural"}</strong>
      <dl>
        {Object.entries(item.facts).length > 0 ? Object.entries(item.facts).map(([key, value]) => (
          <div key={key}>
            <dt>{key}</dt>
            <dd><pre>{factValue(value)}</pre></dd>
          </div>
        )) : (
          <div><dt>facts</dt><dd><pre>{"{}"}</pre></dd></div>
        )}
      </dl>
    </aside>
  );
}

export function EvidenceCopilotDialog({
  open,
  onClose,
  onInspectNode,
  onInspectEdge,
}: EvidenceCopilotDialogProps) {
  const document = useFlowStore((state) => state.document);
  const flow = document.definition;
  const remoteRunId = useFlowStore((state) => state.remoteRunId);
  const runSource = useFlowStore((state) => state.runSource);
  const runStatus = useFlowStore((state) => state.runStatus);
  const selectNode = useFlowStore((state) => state.selectNode);
  const selectEdge = useFlowStore((state) => state.selectEdge);
  const [question, setQuestion] = useState(QUICK_QUESTIONS[0]);
  const [baseVersionId, setBaseVersionId] = useState("");
  const [selectedRunId, setSelectedRunId] = useState("");
  const [response, setResponse] = useState<EvidenceCopilotResponse>();
  const [answeredQuestion, setAnsweredQuestion] = useState("");
  const [selectedEvidenceId, setSelectedEvidenceId] = useState<string>();
  const [incidentRunId, setIncidentRunId] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const currentRunId = runSource === "api" && remoteRunId && UUID_PATTERN.test(remoteRunId)
    ? remoteRunId
    : "";

  const versionsQuery = useQuery({
    queryKey: ["flow-versions", document.flowId, "evidence-copilot"],
    queryFn: () => listFlowVersions(document.flowId),
    enabled: open && isEvidenceCopilotAvailable,
    staleTime: 10_000,
  });
  const versions = versionsQuery.data ?? EMPTY_VERSIONS;

  useEffect(() => {
    if (!open) return;
    setActionError("");
    setSelectedRunId(currentRunId);
  }, [currentRunId, open]);

  const questionLength = Array.from(question).length;
  const evidenceById = useMemo(
    () => new Map(response?.evidence.items.map((item) => [item.id, item]) ?? []),
    [response],
  );
  const orderedSuggestions = useMemo(() => {
    return (response?.suggestions ?? [])
      .map((suggestion, index) => ({ suggestion, index }))
      .sort((left, right) => SEVERITY_RANK[right.suggestion.severity] - SEVERITY_RANK[left.suggestion.severity] || left.index - right.index)
      .map(({ suggestion }) => suggestion);
  }, [response]);
  const selectedEvidence = selectedEvidenceId ? evidenceById.get(selectedEvidenceId) : undefined;

  function inspectNode(nodeId: string) {
    if (!flow.nodes.some((node) => node.id === nodeId)) {
      setActionError(`El nodo ${nodeId} ya no existe en el borrador actual.`);
      return;
    }
    (onInspectNode ?? selectNode)(nodeId);
    onClose();
  }

  function inspectEdge(edgeId: string) {
    if (!flow.edges.some((edge) => edge.id === edgeId)) {
      setActionError(`La conexión ${edgeId} ya no existe en el borrador actual.`);
      return;
    }
    (onInspectEdge ?? selectEdge)(edgeId);
    onClose();
  }

  function executeAction(action: CopilotAction) {
    setActionError("");
    if (!action.targetId) return;
    if (action.kind === "inspect_node") inspectNode(action.targetId);
    if (action.kind === "inspect_edge") inspectEdge(action.targetId);
    if (action.kind === "open_incident") {
      setIncidentRunId(action.targetId);
      onClose();
    }
  }

  async function ask() {
    setError("");
    setActionError("");
    if (questionLength < 3 || questionLength > 4_000) {
      setError("La pregunta debe contener entre 3 y 4.000 caracteres.");
      return;
    }
    setLoading(true);
    try {
      const result = await askEvidenceCopilot(document.flowId, {
        question,
        baseVersionId: baseVersionId || undefined,
        runId: selectedRunId || undefined,
      });
      setResponse(result);
      setAnsweredQuestion(question.trim());
      const firstSuggestion = result.suggestions
        .map((suggestion, index) => ({ suggestion, index }))
        .sort((left, right) => SEVERITY_RANK[right.suggestion.severity] - SEVERITY_RANK[left.suggestion.severity] || left.index - right.index)[0]
        ?.suggestion;
      setSelectedEvidenceId(firstSuggestion?.evidenceIds[0] ?? result.evidence.items[0]?.id);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "No se pudo consultar el Copiloto con Evidencia.");
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <Modal open={open} onClose={onClose} eyebrow="DECISIONES CON TRAZABILIDAD" title="Copiloto con Evidencia" wide>
        <div className="evidence-copilot">
          <section className="copilot-hero" aria-label="Presentación del Copiloto con Evidencia">
            <div className="copilot-orbit" aria-hidden="true"><i /><i /><span>E</span></div>
            <div>
              <span className="eyebrow">GROUNDING VERIFICABLE</span>
              <strong>Pregunta. Verifica. Actúa con evidencia.</strong>
              <p>Cada recomendación cita hechos construidos por FlowVerse; ninguna afirmación llega sola.</p>
            </div>
            <div className="copilot-trust-badges">
              <span><i /> DATOS MINIMIZADOS</span>
              {response && <b>{response.provider === "openai" ? "OPENAI" : "MOTOR LOCAL"}</b>}
            </div>
          </section>

          {!isEvidenceCopilotAvailable ? (
            <section className="copilot-unavailable" role="status">
              <div aria-hidden="true">✦</div>
              <strong>El Copiloto necesita la API de FlowVerse</strong>
              <p>Configura <code>NEXT_PUBLIC_API_URL</code> para construir evidencia segura y consultar el proveedor seleccionado por el servidor.</p>
            </section>
          ) : (
            <div className="copilot-workbench">
              <aside className="copilot-composer" aria-label="Configurar consulta">
                <div className="copilot-composer-heading">
                  <span aria-hidden="true">✦</span>
                  <div><strong>Tu pregunta</strong><small>Hasta 4.000 caracteres</small></div>
                </div>
                <label htmlFor="copilot-question">Pregunta para el Copiloto</label>
                <textarea
                  id="copilot-question"
                  value={question}
                  maxLength={4_000}
                  rows={7}
                  disabled={loading}
                  onChange={(event) => setQuestion(event.target.value)}
                />
                <div className={`copilot-character-count ${questionLength > 3_800 ? "near-limit" : ""}`}>
                  <span>{questionLength < 3 ? "Mínimo 3 caracteres" : "La pregunta se enviará sin espacios externos"}</span>
                  <b>{questionLength.toLocaleString("es-CO")} / 4.000</b>
                </div>

                <div className="copilot-quick-questions" aria-label="Preguntas sugeridas">
                  {QUICK_QUESTIONS.map((prompt, index) => (
                    <button type="button" key={prompt} onClick={() => setQuestion(prompt)} disabled={loading}>
                      <span>{index + 1}</span>{prompt}
                    </button>
                  ))}
                </div>

                <div className="copilot-context-heading">
                  <span>CONTEXTO OPCIONAL</span><i>Mejora la precisión</i>
                </div>
                <div className="field compact-field">
                  <label htmlFor="copilot-base-version">Versión base para diff</label>
                  <select
                    id="copilot-base-version"
                    value={baseVersionId}
                    disabled={loading || versionsQuery.isLoading || Boolean(versionsQuery.error)}
                    onChange={(event) => setBaseVersionId(event.target.value)}
                  >
                    <option value="">Solo borrador actual</option>
                    {versions.map((version) => (
                      <option value={version.id} key={version.id}>
                        Versión {version.number} · {versionDate(version.publishedAt)}
                      </option>
                    ))}
                  </select>
                  {versionsQuery.isLoading && <small role="status">Cargando versiones…</small>}
                  {versionsQuery.error && <small className="field-error">No se pudieron cargar las versiones.</small>}
                </div>
                <div className="field compact-field">
                  <label htmlFor="copilot-run">Run para evidencia de incidente</label>
                  <select
                    id="copilot-run"
                    value={selectedRunId}
                    disabled={loading || !currentRunId}
                    onChange={(event) => setSelectedRunId(event.target.value)}
                  >
                    <option value="">Sin contexto de ejecución</option>
                    {currentRunId && <option value={currentRunId}>Run actual · {runStatus} · {shortId(currentRunId)}</option>}
                  </select>
                  {!currentRunId && <small>No hay un run remoto actual.</small>}
                </div>

                <div className="copilot-privacy-note">
                  <span aria-hidden="true">◈</span>
                  <p><strong>Privacidad por diseño</strong>Inputs, outputs, payloads y valores de configuración no se envían al proveedor.</p>
                </div>
                {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
                <button
                  type="button"
                  className="primary-button copilot-ask-button"
                  disabled={loading || questionLength < 3 || questionLength > 4_000}
                  onClick={() => void ask()}
                >
                  {loading ? <><i className="button-spinner" /> Construyendo evidencia…</> : <><span aria-hidden="true">✦</span> Analizar con evidencia</>}
                </button>
              </aside>

              <section className="copilot-answer" aria-live="polite" aria-label="Respuesta del Copiloto">
                {loading ? (
                  <section className="copilot-thinking" role="status">
                    <div className="copilot-thinking-orbit" aria-hidden="true"><i /><i /><span /></div>
                    <strong>Construyendo una respuesta trazable…</strong>
                    <p>Validamos estructura, análisis, diff e incidente antes de aceptar una recomendación.</p>
                    <ol><li className="done">Minimizando datos</li><li className="active">Resolviendo evidencia</li><li>Verificando acciones</li></ol>
                  </section>
                ) : response ? (
                  <div className="copilot-result">
                    <header className="copilot-result-heading">
                      <div>
                        <span className="eyebrow">RESPUESTA FUNDAMENTADA</span>
                        <h3>{response.summary || "Revisión completada"}</h3>
                        <p title={answeredQuestion}>“{answeredQuestion}”</p>
                      </div>
                      <div className="copilot-package-meta">
                        <span>{response.evidence.items.length} hechos</span>
                        <b className={response.evidence.truncated ? "truncated" : "complete"}>
                          {response.evidence.truncated ? "! PAQUETE TRUNCADO" : "✓ PAQUETE COMPLETO"}
                        </b>
                      </div>
                    </header>

                    {orderedSuggestions.length > 0 ? (
                      <section className="copilot-suggestion-list" aria-label="Sugerencias del Copiloto">
                        {orderedSuggestions.map((suggestion, index) => (
                          <SuggestionCard
                            suggestion={suggestion}
                            index={index}
                            evidence={evidenceById}
                            selectedEvidenceId={selectedEvidenceId}
                            onSelectEvidence={setSelectedEvidenceId}
                            onAction={executeAction}
                            key={`${suggestion.title}-${index}`}
                          />
                        ))}
                      </section>
                    ) : (
                      <section className="copilot-no-suggestions"><span>✓</span><strong>No hay acciones sustentadas</strong><p>El servidor no encontró una recomendación verificable para esta pregunta.</p></section>
                    )}

                    {actionError && <p className="dialog-error" role="alert">⚠ {actionError}</p>}
                    {selectedEvidence && (
                      <EvidenceInspector item={selectedEvidence} onInspectNode={inspectNode} onInspectEdge={inspectEdge} />
                    )}

                    <section className="copilot-limitations" aria-label="Limitaciones de la respuesta">
                      <header><span aria-hidden="true">!</span><div><strong>Alcance y limitaciones</strong><small>Lo que esta respuesta no puede afirmar</small></div></header>
                      {response.limitations.length > 0 ? (
                        <ul>{response.limitations.map((limitation, index) => <li key={`${limitation}-${index}`}>{limitation}</li>)}</ul>
                      ) : <p>El servidor no reportó limitaciones adicionales para este paquete.</p>}
                    </section>
                  </div>
                ) : (
                  <section className="copilot-answer-empty">
                    <div className="copilot-empty-constellation" aria-hidden="true"><i /><i /><i /><span /></div>
                    <strong>Una respuesta que puedes auditar</strong>
                    <p>Haz una pregunta y verás cada recomendación conectada con los hechos exactos que la sustentan.</p>
                    <div><span>◎ Nodo</span><span>⌘ Conexión</span><span>Δ Diff</span><span>↶ Incidente</span></div>
                  </section>
                )}
              </section>
            </div>
          )}
        </div>
      </Modal>

      <IncidentTimeMachineDialog
        open={Boolean(incidentRunId)}
        onClose={() => setIncidentRunId("")}
        runId={incidentRunId}
        onSelectNode={(nodeId) => {
          if (flow.nodes.some((node) => node.id === nodeId)) (onInspectNode ?? selectNode)(nodeId);
        }}
      />
    </>
  );
}
