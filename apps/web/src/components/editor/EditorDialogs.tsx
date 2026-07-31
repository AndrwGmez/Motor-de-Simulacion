"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";
import {
  createShareLink,
  downloadFlow,
  parseImportedFlow,
  parseTextToFlow,
  publishFlow,
  revokeShareLink,
  saveFlow,
  startRun,
} from "@/lib/flow-service";
import type { EditableFlow, FlowDefinition, RunSummary, ValidationIssue } from "@/lib/flow-types";
import { flowMetrics, validateFlow } from "@/lib/validation";
import { useFlowStore } from "@/store/flow-store";

export function Modal({
  open,
  onClose,
  title,
  eyebrow,
  children,
  wide,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  eyebrow: string;
  children: React.ReactNode;
  wide?: boolean;
}) {
  const dialogRef = useRef<HTMLElement>(null);
  const titleId = useId();
  useEffect(() => {
    if (!open) return;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
    const focusableSelector = "button:not(:disabled), a[href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex='-1'])";
    const focusTimer = window.setTimeout(() => {
      const firstFocusable = dialogRef.current?.querySelector<HTMLElement>(focusableSelector);
      (firstFocusable ?? dialogRef.current)?.focus();
    });
    const handleKeyboard = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const focusable = [...dialogRef.current.querySelectorAll<HTMLElement>(focusableSelector)];
      if (focusable.length === 0) {
        event.preventDefault();
        dialogRef.current.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable.at(-1)!;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyboard);
    return () => {
      window.clearTimeout(focusTimer);
      document.removeEventListener("keydown", handleKeyboard);
      previousFocus?.focus();
    };
  }, [onClose, open]);
  if (!open) return null;

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <section
        ref={dialogRef}
        className={`modal-card ${wide ? "wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <header>
          <div>
            <span className="eyebrow">{eyebrow}</span>
            <h2 id={titleId}>{title}</h2>
          </div>
          <button type="button" className="close-button" onClick={onClose} aria-label="Cerrar">×</button>
        </header>
        <div className="modal-body">{children}</div>
      </section>
    </div>
  );
}

async function persistAndPublish(document: EditableFlow) {
  const saved = await saveFlow(document);
  const persisted: EditableFlow = {
    ...document,
    revision: saved.revision,
    etag: saved.etag,
    updatedAt: new Date().toISOString(),
  };
  const publication = await publishFlow(persisted);
  return {
    document: {
      ...persisted,
      etag: publication.etag,
      publishedVersionId: publication.version.id,
      publishedVersionNumber: publication.version.number,
      draftMatchesPublished: true,
    } satisfies EditableFlow,
    source: saved.source,
    ...publication,
  };
}

function FlowPreview({ flow }: { flow: FlowDefinition }) {
  const issues = validateFlow(flow);
  return (
    <div className="flow-preview">
      <div className="preview-universe" aria-hidden="true">
        {flow.nodes.slice(0, 9).map((node, index) => (
          <i
            key={node.id}
            className={`preview-node preview-${node.type}`}
            style={{
              left: `${12 + ((index * 31) % 76)}%`,
              top: `${16 + ((index * 47) % 67)}%`,
              background: node.metadata.color,
            }}
          />
        ))}
      </div>
      <div>
        <span className="eyebrow">PREVISUALIZACIÓN</span>
        <h3>{flow.name}</h3>
        <p>{flow.description || "Sin descripción"}</p>
        <div className="preview-stats">
          <span><strong>{flow.nodes.length}</strong> nodos</span>
          <span><strong>{flow.edges.length}</strong> conexiones</span>
          <span className={issues.some((issue) => issue.severity === "error") ? "has-error" : ""}>
            <strong>{issues.length}</strong> avisos
          </span>
        </div>
      </div>
    </div>
  );
}

export function ImportDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const replaceDefinition = useFlowStore((state) => state.replaceDefinition);
  const [text, setText] = useState("");
  const [preview, setPreview] = useState<FlowDefinition>();
  const [error, setError] = useState("");

  const parse = () => {
    try {
      if (new Blob([text]).size > 1_000_000) throw new Error("El archivo supera el límite de 1 MB.");
      setPreview(parseImportedFlow(JSON.parse(text) as unknown));
      setError("");
    } catch (cause) {
      setPreview(undefined);
      setError(cause instanceof Error ? cause.message : "No se pudo interpretar el JSON.");
    }
  };

  return (
    <Modal open={open} onClose={onClose} eyebrow="ENTRADA ESTRUCTURADA" title="Importar un universo" wide>
      <div className="dialog-grid">
        <div>
          <div className="drop-zone">
            <input
              type="file"
              accept=".json,application/json"
              aria-label="Elegir archivo JSON"
              onChange={async (event) => {
                const file = event.target.files?.[0];
                if (!file) return;
                if (file.size > 1_000_000) {
                  setError("El archivo supera el límite de 1 MB.");
                  return;
                }
                const content = await file.text();
                setText(content);
                try {
                  setPreview(parseImportedFlow(JSON.parse(content) as unknown));
                  setError("");
                } catch (cause) {
                  setError(cause instanceof Error ? cause.message : "Archivo inválido.");
                }
              }}
            />
            <span aria-hidden="true">⇧</span>
            <strong>Arrastra o elige un JSON</strong>
            <small>Contrato FlowDefinition 1.0 · máximo 1 MB</small>
          </div>
          <div className="palette-divider"><span>O PEGA EL DOCUMENTO</span></div>
          <textarea
            className="code-input"
            value={text}
            onChange={(event) => setText(event.target.value)}
            rows={12}
            spellCheck={false}
            placeholder={'{\n  "schemaVersion": "1.0",\n  "name": "Mi flujo",\n  "variables": [],\n  "layout": { "mode": "directional" },\n  "nodes": [],\n  "edges": []\n}'}
          />
          {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
          <button type="button" className="secondary-button full" onClick={parse} disabled={!text.trim()}>Comprobar documento</button>
        </div>
        <div className="preview-pane">
          {preview ? (
            <>
              <FlowPreview flow={preview} />
              <p className="safe-note"><span>✓</span> Importar reemplazará el borrador actual, pero podrás deshacer la operación.</p>
              <button
                type="button"
                className="primary-button full"
                onClick={() => {
                  replaceDefinition(preview);
                  onClose();
                }}
              >Usar este flujo</button>
            </>
          ) : (
            <div className="empty-preview">
              <div className="empty-orbit"><i /><span /></div>
              <strong>Aún no hay previsualización</strong>
              <p>Revisaremos la estructura antes de modificar tu borrador.</p>
            </div>
          )}
        </div>
      </div>
    </Modal>
  );
}

export function TextToFlowDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const replaceDefinition = useFlowStore((state) => state.replaceDefinition);
  const [text, setText] = useState(
    "Cuando un cliente realiza un pedido, validar el pago. Si fue aprobado, revisar inventario. Si hay inventario, preparar y enviar el pedido. Finalmente, confirmar la entrega.",
  );
  const [preview, setPreview] = useState<FlowDefinition>();
  const [warnings, setWarnings] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [source, setSource] = useState<"api" | "local">("local");
  const [error, setError] = useState("");

  async function generate() {
    setLoading(true);
    setError("");
    try {
      const proposal = await parseTextToFlow(text);
      setPreview(proposal.flow);
      setWarnings(proposal.warnings);
      setSource(proposal.source);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "No pudimos generar una propuesta.");
    } finally {
      setLoading(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} eyebrow="TEXTO A GRAFO" title="Describe tu proceso" wide>
      <div className="dialog-grid">
        <div>
          <label className="large-input-label" htmlFor="process-description">
            <span>¿Qué debe ocurrir?</span>
            <small>Incluye decisiones, resultados y excepciones.</small>
          </label>
          <textarea
            id="process-description"
            className="prompt-input"
            value={text}
            onChange={(event) => setText(event.target.value)}
            maxLength={8_000}
            rows={14}
          />
          <div className="character-count">{text.length} / 8.000</div>
          {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
          <button
            type="button"
            className="primary-button full"
            onClick={generate}
            disabled={loading || text.trim().length < 10}
          >
            {loading ? <><span className="button-spinner" /> Interpretando proceso…</> : <>✦ Generar propuesta</>}
          </button>
          <p className="safe-note"><span>⌁</span> La propuesta nunca se ejecuta como código y siempre requiere confirmación.</p>
        </div>
        <div className="preview-pane">
          {preview ? (
            <>
              <div className="proposal-source"><i /> Proveedor: {source === "api" ? "API configurada" : "intérprete local"}</div>
              <FlowPreview flow={preview} />
              {warnings.map((warning) => <p className="proposal-warning" key={warning}>⚠ {warning}</p>)}
              <div className="dialog-actions">
                <button type="button" className="secondary-button" onClick={generate}>Regenerar</button>
                <button
                  type="button"
                  className="primary-button"
                  onClick={() => {
                    replaceDefinition(preview);
                    onClose();
                  }}
                >Confirmar y editar</button>
              </div>
            </>
          ) : (
            <div className="empty-preview">
              <span className="ai-spark">✦</span>
              <strong>Tu universo aparecerá aquí</strong>
              <p>Separaremos acciones, decisiones y resultados en nodos conectados.</p>
            </div>
          )}
        </div>
      </div>
    </Modal>
  );
}

export function ValidationDialog({
  open,
  onClose,
  onSelectIssue,
}: {
  open: boolean;
  onClose: () => void;
  onSelectIssue: (issue: ValidationIssue) => void;
}) {
  const flow = useFlowStore((state) => state.document.definition);
  const issues = useFlowStore((state) => state.validationIssues);
  const metrics = flowMetrics(flow, issues);
  return (
    <Modal open={open} onClose={onClose} eyebrow="VALIDACIÓN Y ANÁLISIS" title="Salud del flujo" wide>
      <div className="metrics-grid">
        <article><span>Nodos</span><strong>{metrics.nodeCount}</strong><small>{metrics.reachableCount} alcanzables</small></article>
        <article><span>Conexiones</span><strong>{metrics.edgeCount}</strong><small>complejidad {metrics.complexity}</small></article>
        <article><span>Cobertura</span><strong>{metrics.coveragePercent}%</strong><small>desde los inicios</small></article>
        <article className={metrics.errors ? "danger" : "healthy"}><span>Estado</span><strong>{metrics.errors ? `${metrics.errors} errores` : "Ejecutable"}</strong><small>{metrics.warnings} advertencias</small></article>
      </div>
      <div className="issue-list">
        {issues.length === 0 ? (
          <div className="validation-success">
            <span>✓</span>
            <div><strong>El flujo está listo para simular</strong><p>No encontramos problemas estructurales.</p></div>
          </div>
        ) : issues.map((item) => (
          <button
            type="button"
            className={`issue-row issue-${item.severity}`}
            key={item.id}
            onClick={() => {
              onSelectIssue(item);
              if (item.nodeId || item.edgeId) onClose();
            }}
          >
            <span>{item.severity === "error" ? "×" : item.severity === "warning" ? "!" : "i"}</span>
            <div><strong>{item.code}</strong><p>{item.message}</p></div>
            {(item.nodeId || item.edgeId) && <i>Enfocar →</i>}
          </button>
        ))}
      </div>
    </Modal>
  );
}

export function RunDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const document = useFlowStore((state) => state.document);
  const startSimulation = useFlowStore((state) => state.startSimulation);
  const startRemoteSimulation = useFlowStore((state) => state.startRemoteSimulation);
  const variables = document.definition.variables;
  const initialData = useMemo(
    () => variables.reduce<Record<string, unknown>>((result, variable) => {
      const parts = variable.path.split("/").filter(Boolean);
      if (parts.length === 1) result[parts[0]] = variable.default;
      if (parts.length === 2) {
        const parent = (result[parts[0]] as Record<string, unknown> | undefined) ?? {};
        parent[parts[1]] = variable.default;
        result[parts[0]] = parent;
      }
      return result;
    }, {}),
    [variables],
  );
  const [input, setInput] = useState(() => JSON.stringify(initialData, null, 2));
  const [failedNodeId, setFailedNodeId] = useState("");
  const [decisionId, setDecisionId] = useState("");
  const [forcedEdgeId, setForcedEdgeId] = useState("");
  const [error, setError] = useState("");
  const [starting, setStarting] = useState(false);
  const decisions = document.definition.nodes.filter((node) => node.type === "decision");
  const decisionEdges = document.definition.edges.filter((edge) => edge.source === decisionId);

  return (
    <Modal open={open} onClose={onClose} eyebrow="SIMULACIÓN DETERMINISTA" title="Configurar ejecución" wide>
      <div className="run-dialog-grid">
        <div>
          <label className="large-input-label" htmlFor="run-input">
            <span>Datos iniciales</span>
            <small>JSON disponible para evaluar las condiciones.</small>
          </label>
          <textarea
            id="run-input"
            className={`code-input ${error ? "invalid" : ""}`}
            value={input}
            rows={15}
            spellCheck={false}
            onChange={(event) => setInput(event.target.value)}
          />
          {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
        </div>
        <div>
          <span className="eyebrow">OVERRIDES DE PRUEBA</span>
          <div className="field">
            <label htmlFor="failed-node">Forzar error en nodo</label>
            <select id="failed-node" value={failedNodeId} onChange={(event) => setFailedNodeId(event.target.value)}>
              <option value="">Sin error forzado</option>
              {document.definition.nodes.filter((node) => !["trigger", "end", "group"].includes(node.type)).map((node) => (
                <option value={node.id} key={node.id}>{node.label}</option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="forced-decision">Forzar decisión</label>
            <select
              id="forced-decision"
              value={decisionId}
              onChange={(event) => {
                setDecisionId(event.target.value);
                setForcedEdgeId("");
              }}
            >
              <option value="">Evaluar normalmente</option>
              {decisions.map((node) => <option value={node.id} key={node.id}>{node.label}</option>)}
            </select>
          </div>
          {decisionId && (
            <div className="field">
              <label htmlFor="forced-edge">Camino elegido</label>
              <select id="forced-edge" value={forcedEdgeId} onChange={(event) => setForcedEdgeId(event.target.value)}>
                <option value="">Selecciona un camino</option>
                {decisionEdges.map((edge) => <option value={edge.id} key={edge.id}>{edge.label || edge.target}</option>)}
              </select>
            </div>
          )}
          <div className="simulation-note">
            <span>◷</span>
            <div><strong>Tiempo lógico, resultado reproducible</strong><p>La velocidad solo cambia la animación y nunca el orden de los eventos.</p></div>
          </div>
          <button
            type="button"
            className="primary-button full"
            disabled={starting}
            onClick={async () => {
              try {
                const parsed = JSON.parse(input) as Record<string, unknown>;
                setStarting(true);
                const result = await startRun(document, parsed, {
                  failedNodeIds: failedNodeId ? [failedNodeId] : [],
                  forcedEdgeIds: decisionId && forcedEdgeId ? { [decisionId]: forcedEdgeId } : {},
                });
                if (result.source === "api") startRemoteSimulation(result.runId);
                else startSimulation(result.plan);
                setError("");
                onClose();
              } catch (cause) {
                setError(cause instanceof SyntaxError
                  ? "Los datos iniciales deben contener JSON válido."
                  : cause instanceof Error
                    ? cause.message
                    : "No se pudo iniciar la simulación.");
              } finally {
                setStarting(false);
              }
            }}
          >{starting ? "Iniciando…" : "▶ Ejecutar flujo"}</button>
        </div>
      </div>
    </Modal>
  );
}

export function ShareDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const document = useFlowStore((state) => state.document);
  const flow = document.definition;
  const history = useFlowStore((state) => state.runHistory);
  const markSaving = useFlowStore((state) => state.markSaving);
  const markSaved = useFlowStore((state) => state.markSaved);
  const markSaveError = useFlowStore((state) => state.markSaveError);
  const markPublished = useFlowStore((state) => state.markPublished);
  const [copied, setCopied] = useState(false);
  const [share, setShare] = useState<{ id: string; url: string; source: "api" | "local" }>();
  const [busy, setBusy] = useState(false);
  const [stage, setStage] = useState("");
  const [error, setError] = useState("");
  const path = share?.url ?? "/compartir/demo-pedidos";
  const url = typeof window === "undefined" || /^https?:/.test(path) ? path : `${window.location.origin}${path}`;
  return (
    <Modal open={open} onClose={onClose} eyebrow="ENLACE PÚBLICO" title="Comparte esta versión">
      <div className="share-visual"><span>◎</span><i /><i /><i /></div>
      <h3 className="centered-title">{flow.name}</h3>
      <p className="centered-copy">El enlace abre una visualización de solo lectura. Los datos de entrada y salida permanecen privados.</p>
      {!share && (
        <button
          type="button"
          className="primary-button full"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            setError("");
            try {
              let current = document;
              if (!document.draftMatchesPublished || !document.publishedVersionId) {
                setStage("Guardando borrador…");
                markSaving();
                const publication = await persistAndPublish(document);
                current = publication.document;
                markSaved(current.revision, current.etag, publication.source);
                markPublished(publication.version.id, publication.version.number, publication.etag);
              }
              setStage("Creando enlace seguro…");
              setShare(await createShareLink(current, history));
              setStage("");
            } catch (cause) {
              markSaveError(cause instanceof Error && /otra pestaña/i.test(cause.message));
              setError(cause instanceof Error ? cause.message : "No se pudo crear el enlace público.");
            } finally {
              setBusy(false);
            }
          }}
        >{busy ? stage || "Preparando enlace…" : "Crear enlace público"}</button>
      )}
      {document.draftMatchesPublished && document.publishedVersionNumber && !share && (
        <p className="published-note">✓ Borrador sincronizado con la versión {document.publishedVersionNumber}.</p>
      )}
      {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
      <div className="copy-field">
        <input value={share ? url : "Crea el enlace para poder copiarlo"} readOnly aria-label="Enlace público" />
        <button
          type="button"
          disabled={!share || busy}
          onClick={async () => {
            await navigator.clipboard?.writeText(url);
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1_600);
          }}
        >{copied ? "Copiado ✓" : "Copiar"}</button>
      </div>
      <div className="share-options">
        <span><i className="green-dot" /> {share ? `Enlace activo · ${share.source}` : "Aún no publicado"}</span>
        <button
          type="button"
          disabled={!share || busy}
          onClick={async () => {
            if (!share) return;
            setBusy(true);
            setError("");
            try {
              await revokeShareLink(share.id);
              setShare(undefined);
              setCopied(false);
            } catch (cause) {
              setError(cause instanceof Error ? cause.message : "No se pudo revocar el enlace público.");
            } finally {
              setBusy(false);
            }
          }}
        >Revocar enlace</button>
      </div>
    </Modal>
  );
}

export function PublishDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const document = useFlowStore((state) => state.document);
  const issues = useFlowStore((state) => state.validationIssues);
  const markSaving = useFlowStore((state) => state.markSaving);
  const markSaved = useFlowStore((state) => state.markSaved);
  const markSaveError = useFlowStore((state) => state.markSaveError);
  const markPublished = useFlowStore((state) => state.markPublished);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [publishedNumber, setPublishedNumber] = useState<number>();
  const errors = issues.filter((issue) => issue.severity === "error");

  useEffect(() => {
    if (!open) return;
    setError("");
    setPublishedNumber(undefined);
  }, [open]);

  return (
    <Modal open={open} onClose={onClose} eyebrow="VERSIÓN INMUTABLE" title="Publicar borrador">
      <div className="publish-visual"><span>{publishedNumber ? "✓" : "◇"}</span></div>
      {publishedNumber ? (
        <div className="publish-success">
          <h3>Versión {publishedNumber} publicada</h3>
          <p>Esta copia ya puede compartirse y sus futuras ejecuciones conservarán exactamente la misma definición.</p>
          <button type="button" className="primary-button full" onClick={onClose}>Listo</button>
        </div>
      ) : (
        <>
          <p className="centered-copy">
            Guardaremos los cambios pendientes, validaremos el flujo y crearemos una copia que no podrá modificarse.
          </p>
          <div className="publish-summary">
            <span><strong>{document.definition.nodes.length}</strong> nodos</span>
            <span><strong>{document.definition.edges.length}</strong> conexiones</span>
            <span className={errors.length ? "has-error" : "is-valid"}>
              <strong>{errors.length}</strong> errores
            </span>
          </div>
          {document.draftMatchesPublished && document.publishedVersionNumber && (
            <p className="published-note">El borrador ya coincide con la versión {document.publishedVersionNumber}. Publicarlo creará una versión adicional.</p>
          )}
          {errors.length > 0 && (
            <p className="dialog-error" role="alert">⚠ Corrige los errores de validación antes de publicar.</p>
          )}
          {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
          <button
            type="button"
            className="primary-button full"
            disabled={busy || errors.length > 0}
            onClick={async () => {
              setBusy(true);
              setError("");
              markSaving();
              try {
                const publication = await persistAndPublish(document);
                markSaved(
                  publication.document.revision,
                  publication.document.etag,
                  publication.source,
                );
                markPublished(publication.version.id, publication.version.number, publication.etag);
                setPublishedNumber(publication.version.number);
              } catch (cause) {
                markSaveError(cause instanceof Error && /otra pestaña/i.test(cause.message));
                setError(cause instanceof Error ? cause.message : "No se pudo publicar el flujo.");
              } finally {
                setBusy(false);
              }
            }}
          >{busy ? "Guardando y publicando…" : "Publicar versión"}</button>
        </>
      )}
    </Modal>
  );
}

export function HistoryDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const history = useFlowStore((state) => state.runHistory);
  return (
    <Modal open={open} onClose={onClose} eyebrow="OBSERVABILIDAD" title="Historial de ejecuciones" wide>
      {history.length ? (
        <div className="history-table" role="table" aria-label="Historial">
          <div className="history-head" role="row">
            <span>Estado</span><span>Ejecución</span><span>Ruta</span><span>Tiempo lógico</span><span>Fecha</span>
          </div>
          {history.map((run) => <HistoryRow run={run} key={run.id} />)}
        </div>
      ) : (
        <div className="empty-preview">
          <div className="empty-orbit"><i /><span /></div>
          <strong>No hay ejecuciones disponibles</strong>
          <p>Las ejecuciones terminales aparecerán aquí cuando existan o hayan sido incluidas en el enlace.</p>
        </div>
      )}
    </Modal>
  );
}

function HistoryRow({ run }: { run: RunSummary }) {
  const statusLabel = run.status === "completed"
    ? "Completada"
    : run.status === "cancelled"
      ? "Cancelada"
      : "Fallida";
  return (
    <div className="history-row" role="row">
      <span className={`history-status history-${run.status}`}><i />{statusLabel}</span>
      <code>{run.id}</code>
      <span>{run.visitedNodeIds.length} nodos</span>
      <span>{(run.durationMs / 1_000).toFixed(2)} s</span>
      {run.createdAt
        ? <time dateTime={run.createdAt}>{new Intl.DateTimeFormat("es-CO", { dateStyle: "short", timeStyle: "short" }).format(new Date(run.createdAt))}</time>
        : <span>Fecha privada</span>}
    </div>
  );
}

export function AccessibleGraphDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const flow = useFlowStore((state) => state.document.definition);
  const selectNode = useFlowStore((state) => state.selectNode);
  const nodeStates = useFlowStore((state) => state.nodeStates);
  return (
    <Modal open={open} onClose={onClose} eyebrow="VISTA ACCESIBLE" title="Estructura del flujo" wide>
      <p className="dialog-intro">Alternativa textual al canvas 3D. Cada elemento muestra sus conexiones salientes y su estado de simulación.</p>
      <div className="graph-list">
        {flow.nodes.filter((node) => node.type !== "group").map((node) => {
          const outputs = flow.edges.filter((edge) => edge.source === node.id);
          return (
            <button
              type="button"
              key={node.id}
              onClick={() => {
                selectNode(node.id);
                onClose();
              }}
            >
              <span className={`list-node-type list-${node.type}`}>{node.type}</span>
              <div><strong>{node.label}</strong><small>{node.id}</small></div>
              <span className={`list-node-state state-${nodeStates[node.id] ?? "idle"}`}>{nodeStates[node.id] ?? "idle"}</span>
              <span>{outputs.length ? outputs.map((edge) => `${edge.label || "salida"} → ${edge.target}`).join(" · ") : "Sin salidas"}</span>
            </button>
          );
        })}
      </div>
    </Modal>
  );
}

export function ExportButton() {
  const flow = useFlowStore((state) => state.document.definition);
  return <button type="button" className="menu-row" onClick={() => downloadFlow(flow)}>⇩ Descargar JSON canónico</button>;
}
