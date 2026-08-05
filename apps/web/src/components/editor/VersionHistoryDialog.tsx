"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  diffFlowDefinitions,
  type FlowVersion,
  type SemanticChange,
  type SemanticImpact,
} from "@flowverse/core";
import {
  getFlowVersion,
  listFlowVersions,
  restoreFlowVersion,
} from "@/lib/version-service";
import { useFlowStore } from "@/store/flow-store";
import { Modal } from "./EditorDialogs";

const EMPTY_VERSIONS: FlowVersion[] = [];

const IMPACT_LABEL: Record<SemanticImpact, string> = {
  visual: "Visual",
  behavioral: "Comportamiento",
  breaking: "Ruptura",
};

const ENTITY_LABEL: Record<SemanticChange["entity"], string> = {
  flow: "Flujo",
  layout: "Distribución",
  variable: "Variable",
  node: "Nodo",
  edge: "Conexión",
};

const OPERATION_LABEL: Record<SemanticChange["operation"], string> = {
  added: "Añadido",
  removed: "Eliminado",
  modified: "Modificado",
};

const FIELD_LABEL: Record<string, string> = {
  name: "Nombre",
  description: "Descripción",
  layout: "Distribución",
  metadata: "Metadatos",
  type: "Tipo",
  inputs: "Entradas",
  outputs: "Salidas",
  activationMode: "Activación",
  durationMs: "Duración",
  configuration: "Configuración",
  position: "Posición",
  locked: "Bloqueo",
  source: "Origen",
  target: "Destino",
  sourcePort: "Puerto de origen",
  targetPort: "Puerto de destino",
  condition: "Condición",
  isDefault: "Ruta predeterminada",
  priority: "Prioridad",
  required: "Obligatoria",
  default: "Valor inicial",
};

function formatValue(value: unknown): string {
  if (value === undefined) return "—";
  if (value === null) return "null";
  if (typeof value === "string") return value || "∅";
  const serialized = JSON.stringify(value);
  if (!serialized) return String(value);
  return serialized.length > 96 ? `${serialized.slice(0, 93)}…` : serialized;
}

function versionDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Fecha desconocida";
  return new Intl.DateTimeFormat("es-CO", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function VersionTimeline({
  versions,
  selectedId,
  currentVersionId,
  onSelect,
}: {
  versions: FlowVersion[];
  selectedId?: string;
  currentVersionId?: string;
  onSelect: (versionId: string) => void;
}) {
  return (
    <div className="version-timeline" aria-label="Versiones publicadas">
      {versions.map((version, index) => (
        <button
          type="button"
          key={version.id}
          className={selectedId === version.id ? "selected" : ""}
          onClick={() => onSelect(version.id)}
          aria-pressed={selectedId === version.id}
        >
          <span className="version-orbit"><i /></span>
          <span className="version-copy">
            <strong>Versión {version.number}</strong>
            <small>{versionDate(version.publishedAt)}</small>
            <code>{version.publishedBy === "demo-user" ? "Modo local" : version.publishedBy.slice(0, 8)}</code>
          </span>
          <span className="version-badges">
            {index === 0 && <b>ÚLTIMA</b>}
            {version.id === currentVersionId && <b className="matches">ACTUAL</b>}
          </span>
        </button>
      ))}
    </div>
  );
}

function ChangeRow({ change }: { change: SemanticChange }) {
  return (
    <article className={`semantic-change impact-${change.impact}`}>
      <div className="change-heading">
        <span>{ENTITY_LABEL[change.entity]}</span>
        <strong>{change.label || change.id}</strong>
        <i>{OPERATION_LABEL[change.operation]}</i>
        <b>{IMPACT_LABEL[change.impact]}</b>
      </div>
      {change.fields.length > 0 && (
        <div className="change-fields">
          {change.fields.map((field) => (
            <div key={field.path}>
              <span>{FIELD_LABEL[field.path.replace(/^\//, "")] ?? field.path}</span>
              <code title={formatValue(field.before)}>{formatValue(field.before)}</code>
              <i aria-hidden="true">→</i>
              <code title={formatValue(field.after)}>{formatValue(field.after)}</code>
            </div>
          ))}
        </div>
      )}
    </article>
  );
}

export function VersionHistoryDialog({
  open,
  onClose,
  canRestore,
}: {
  open: boolean;
  onClose: () => void;
  canRestore: boolean;
}) {
  const document = useFlowStore((state) => state.document);
  const saveStatus = useFlowStore((state) => state.saveStatus);
  const applyRestoredVersion = useFlowStore((state) => state.applyRestoredVersion);
  const markSaveError = useFlowStore((state) => state.markSaveError);
  const [baseVersionId, setBaseVersionId] = useState("");
  const [target, setTarget] = useState("draft");
  const [confirming, setConfirming] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  const versionsQuery = useQuery({
    queryKey: ["flow-versions", document.flowId],
    queryFn: () => listFlowVersions(document.flowId),
    enabled: open,
    staleTime: 0,
  });
  const versions = versionsQuery.data ?? EMPTY_VERSIONS;

  useEffect(() => {
    if (!open) return;
    setConfirming(false);
    setError("");
    setSuccess("");
  }, [open]);

  useEffect(() => {
    if (!versions.length) {
      setBaseVersionId("");
      return;
    }
    if (!versions.some((version) => version.id === baseVersionId)) {
      setBaseVersionId(versions[0].id);
    }
  }, [baseVersionId, versions]);

  const baseQuery = useQuery({
    queryKey: ["flow-version", document.flowId, baseVersionId],
    queryFn: () => getFlowVersion(baseVersionId, document.flowId),
    enabled: open && Boolean(baseVersionId),
    staleTime: Infinity,
  });
  const targetVersionId = target === "draft" ? "" : target;
  const targetQuery = useQuery({
    queryKey: ["flow-version", document.flowId, targetVersionId],
    queryFn: () => getFlowVersion(targetVersionId, document.flowId),
    enabled: open && Boolean(targetVersionId),
    staleTime: Infinity,
  });

  const targetDefinition = target === "draft" ? document.definition : targetQuery.data?.definition;
  const diff = useMemo(
    () => baseQuery.data && targetDefinition
      ? diffFlowDefinitions(baseQuery.data.definition, targetDefinition)
      : undefined,
    [baseQuery.data, targetDefinition],
  );
  const selectedVersion = versions.find((version) => version.id === baseVersionId);
  const loadingComparison = baseQuery.isLoading || (target !== "draft" && targetQuery.isLoading);
  const comparisonError = baseQuery.error ?? targetQuery.error;
  const canConfirmRestore = canRestore && saveStatus === "saved" && Boolean(baseQuery.data) && !restoring;

  async function restoreSelectedVersion() {
    const snapshot = baseQuery.data;
    if (!snapshot || !canConfirmRestore) return;
    setRestoring(true);
    setError("");
    setSuccess("");
    try {
      const currentDocument = useFlowStore.getState().document;
      const restored = await restoreFlowVersion(currentDocument, snapshot);
      applyRestoredVersion(
        { version: restored.version, definition: restored.definition },
        restored.revision,
        restored.etag,
        restored.source,
      );
      setConfirming(false);
      setTarget("draft");
      setSuccess(`Versión ${restored.version.number} aplicada al borrador. Puedes deshacerla desde el editor.`);
    } catch (cause) {
      const conflict = cause instanceof Error && cause.message === "conflict";
      if (conflict) markSaveError(true);
      setError(conflict
        ? "El borrador cambió en otra pestaña. Recárgalo antes de restaurar."
        : cause instanceof Error ? cause.message : "No se pudo restaurar la versión.");
    } finally {
      setRestoring(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} eyebrow="MÁQUINA DEL TIEMPO" title="Versiones y cambios" wide>
      {versionsQuery.isLoading ? (
        <div className="version-loading" role="status"><span className="scene-loader" /><strong>Reconstruyendo la línea temporal…</strong></div>
      ) : versionsQuery.error ? (
        <div className="version-empty error" role="alert">
          <span>!</span><strong>No pudimos cargar las versiones</strong>
          <p>{versionsQuery.error instanceof Error ? versionsQuery.error.message : "Inténtalo de nuevo."}</p>
          <button type="button" className="secondary-button" onClick={() => void versionsQuery.refetch()}>Reintentar</button>
        </div>
      ) : versions.length === 0 ? (
        <div className="version-empty">
          <span>◇</span><strong>Aún no existe una versión publicada</strong>
          <p>Publica el borrador para crear el primer punto inmutable de esta línea temporal.</p>
        </div>
      ) : (
        <div className="version-explorer">
          <aside>
            <div className="version-aside-heading">
              <span>{versions.length}</span>
              <div><strong>Línea temporal</strong><small>Publicaciones inmutables</small></div>
            </div>
            <VersionTimeline
              versions={versions}
              selectedId={baseVersionId}
              currentVersionId={document.draftMatchesPublished ? document.publishedVersionId : undefined}
              onSelect={(versionId) => {
                setBaseVersionId(versionId);
                setConfirming(false);
                setSuccess("");
              }}
            />
          </aside>

          <section className="version-comparison">
            <div className="compare-selectors">
              <label>
                <span>BASE</span>
                <select value={baseVersionId} onChange={(event) => setBaseVersionId(event.target.value)}>
                  {versions.map((version) => <option value={version.id} key={version.id}>Versión {version.number}</option>)}
                </select>
              </label>
              <i aria-hidden="true">→</i>
              <label>
                <span>OBJETIVO</span>
                <select value={target} onChange={(event) => setTarget(event.target.value)}>
                  <option value="draft">Borrador actual</option>
                  {versions.map((version) => <option value={version.id} key={version.id}>Versión {version.number}</option>)}
                </select>
              </label>
            </div>

            {loadingComparison ? (
              <div className="comparison-loading" role="status"><span className="button-spinner" /> Calculando diferencias semánticas…</div>
            ) : comparisonError ? (
              <p className="dialog-error" role="alert">⚠ {comparisonError instanceof Error ? comparisonError.message : "No se pudo comparar."}</p>
            ) : diff ? (
              <>
                <div className="diff-summary" aria-label="Resumen de cambios">
                  <article className="breaking"><span>Rupturas</span><strong>{diff.summary.breaking}</strong><small>contrato o conectividad</small></article>
                  <article className="behavioral"><span>Comportamiento</span><strong>{diff.summary.behavioral}</strong><small>ejecución o rutas</small></article>
                  <article className="visual"><span>Visuales</span><strong>{diff.summary.visual}</strong><small>canvas y contenido</small></article>
                  <article><span>Entidades</span><strong>{diff.changes.length}</strong><small>{diff.summary.added} + · {diff.summary.removed} − · {diff.summary.modified} ~</small></article>
                </div>

                {diff.hasChanges ? (
                  <div className="semantic-change-list">
                    {diff.changes.map((change) => <ChangeRow change={change} key={`${change.entity}:${change.id}`} />)}
                  </div>
                ) : (
                  <div className="versions-match">
                    <span>✓</span>
                    <div><strong>Son semánticamente idénticos</strong><p>No hay cambios visuales, de comportamiento ni de contrato.</p></div>
                  </div>
                )}
              </>
            ) : null}

            {success && <p className="restore-success" role="status">✓ {success}</p>}
            {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
            {canRestore && selectedVersion && (
              <div className="restore-zone">
                {confirming ? (
                  <div className="restore-confirmation">
                    <div>
                      <strong>¿Aplicar la versión {selectedVersion.number} al borrador?</strong>
                      <p>La publicación seguirá inmutable. El borrador actual se reemplazará y podrás deshacer la operación.</p>
                    </div>
                    <button type="button" className="secondary-button" onClick={() => setConfirming(false)} disabled={restoring}>Cancelar</button>
                    <button type="button" className="primary-button" onClick={() => void restoreSelectedVersion()} disabled={!canConfirmRestore}>
                      {restoring ? "Restaurando…" : `Restaurar V${selectedVersion.number}`}
                    </button>
                  </div>
                ) : (
                  <>
                    <div>
                      <strong>Convertir V{selectedVersion.number} en el nuevo borrador</strong>
                      <small>{saveStatus === "saved" ? "Operación segura y reversible" : "Espera a que terminen de guardarse los cambios"}</small>
                    </div>
                    <button type="button" className="primary-button" onClick={() => setConfirming(true)} disabled={saveStatus !== "saved" || !baseQuery.data}>
                      ↺ Aplicar al borrador
                    </button>
                  </>
                )}
              </div>
            )}
          </section>
        </div>
      )}
    </Modal>
  );
}
