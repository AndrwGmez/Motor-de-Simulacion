"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { connectRunEvents, loadFlow, loadPublicShare } from "@/lib/flow-service";
import { type LayoutMode, type ValidationIssue } from "@/lib/flow-types";
import { getProject, type ProjectSummary } from "@/lib/workspace-service";
import { useAutosave } from "@/hooks/useAutosave";
import { useFlowStore } from "@/store/flow-store";
import { FlowScene } from "./FlowScene";
import { Inspector } from "./Inspector";
import { NodePalette } from "./NodePalette";
import { RunControls } from "./RunControls";
import {
  AccessibleGraphDialog,
  ExportButton,
  HistoryDialog,
  ImportDialog,
  PublishDialog,
  RunDialog,
  ShareDialog,
  TextToFlowDialog,
  ValidationDialog,
} from "./EditorDialogs";

const LAYOUTS: Array<{ id: LayoutMode; label: string; icon: string }> = [
  { id: "force", label: "Espacio libre", icon: "⠿" },
  { id: "directional", label: "Direccional", icon: "⇢" },
  { id: "layers", label: "Capas", icon: "≋" },
  { id: "timeline", label: "Cronología", icon: "⌁" },
  { id: "clusters", label: "Clústeres", icon: "⌘" },
  { id: "execution", label: "Ejecución", icon: "▶" },
];

interface EditorClientProps {
  flowId: string;
  projectId?: string;
  readOnly?: boolean;
  shareToken?: string;
}

export function EditorClient({ flowId, projectId, readOnly = false, shareToken }: EditorClientProps) {
  const document = useFlowStore((state) => state.document);
  const flow = document.definition;
  const loadDocument = useFlowStore((state) => state.loadDocument);
  const updateFlowMeta = useFlowStore((state) => state.updateFlowMeta);
  const selectedNodeId = useFlowStore((state) => state.selectedNodeId);
  const selectedEdgeId = useFlowStore((state) => state.selectedEdgeId);
  const nodeStates = useFlowStore((state) => state.nodeStates);
  const activeEdgeId = useFlowStore((state) => state.activeEdgeId);
  const validationIssues = useFlowStore((state) => state.validationIssues);
  const saveStatus = useFlowStore((state) => state.saveStatus);
  const saveSource = useFlowStore((state) => state.saveSource);
  const pastLength = useFlowStore((state) => state.past.length);
  const futureLength = useFlowStore((state) => state.future.length);
  const runStatus = useFlowStore((state) => state.runStatus);
  const runSource = useFlowStore((state) => state.runSource);
  const remoteRunId = useFlowStore((state) => state.remoteRunId);
  const eventCursor = useFlowStore((state) => state.eventCursor);
  const speed = useFlowStore((state) => state.speed);
  const addNode = useFlowStore((state) => state.addNode);
  const addEdge = useFlowStore((state) => state.addEdge);
  const selectNode = useFlowStore((state) => state.selectNode);
  const selectEdge = useFlowStore((state) => state.selectEdge);
  const clearSelection = useFlowStore((state) => state.clearSelection);
  const moveNode = useFlowStore((state) => state.moveNode);
  const duplicateSelected = useFlowStore((state) => state.duplicateSelected);
  const deleteSelected = useFlowStore((state) => state.deleteSelected);
  const undo = useFlowStore((state) => state.undo);
  const redo = useFlowStore((state) => state.redo);
  const changeLayout = useFlowStore((state) => state.changeLayout);
  const applyNextEvent = useFlowStore((state) => state.applyNextEvent);
  const ingestRemoteEvent = useFlowStore((state) => state.ingestRemoteEvent);
  const setStreamStatus = useFlowStore((state) => state.setStreamStatus);

  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [project, setProject] = useState<ProjectSummary>();
  const [connectMode, setConnectMode] = useState(false);
  const [connectionSourceId, setConnectionSourceId] = useState<string>();
  const [fitRequest, setFitRequest] = useState(0);
  const [importOpen, setImportOpen] = useState(false);
  const [textOpen, setTextOpen] = useState(false);
  const [validationOpen, setValidationOpen] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [publishOpen, setPublishOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [accessibleOpen, setAccessibleOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const errors = validationIssues.filter((issue) => issue.severity === "error").length;
  const warnings = validationIssues.filter((issue) => issue.severity === "warning").length;
  const role = project?.role;
  const effectiveReadOnly = readOnly || role === "viewer";
  const canEdit = !effectiveReadOnly && (role === "owner" || role === "editor");
  const canPublish = canEdit;
  const canShare = !readOnly && role === "owner";

  useAutosave(canEdit && !loading);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadError("");
    Promise.all([
      shareToken ? loadPublicShare(shareToken) : loadFlow(flowId),
      projectId && !shareToken ? getProject(projectId) : Promise.resolve(undefined),
    ])
      .then(([loaded, loadedProject]) => {
        if (!cancelled) {
          loadDocument(loaded);
          setProject(loadedProject);
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) setLoadError(error instanceof Error ? error.message : "No se pudo abrir el flujo.");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [flowId, loadDocument, projectId, shareToken]);

  useEffect(() => {
    if (runStatus !== "running" || runSource !== "local") return;
    const timeout = window.setTimeout(applyNextEvent, Math.max(70, 390 / speed));
    return () => window.clearTimeout(timeout);
  }, [applyNextEvent, eventCursor, runSource, runStatus, speed]);

  useEffect(() => {
    if (runSource !== "api" || !remoteRunId || !["running", "paused"].includes(runStatus)) return;
    return connectRunEvents(remoteRunId, 0, ingestRemoteEvent, setStreamStatus);
  }, [ingestRemoteEvent, remoteRunId, runSource, runStatus, setStreamStatus]);

  useEffect(() => {
    if (!canEdit) return;
    const shortcuts = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target?.matches("input, textarea, select, [contenteditable=true]")) return;
      const command = event.ctrlKey || event.metaKey;
      if (command && event.key.toLocaleLowerCase() === "z" && !event.shiftKey) {
        event.preventDefault();
        undo();
      }
      if ((command && event.key.toLocaleLowerCase() === "y") || (command && event.shiftKey && event.key.toLocaleLowerCase() === "z")) {
        event.preventDefault();
        redo();
      }
      if (command && event.key.toLocaleLowerCase() === "d") {
        event.preventDefault();
        duplicateSelected();
      }
      if (event.key === "Delete" || event.key === "Backspace") deleteSelected();
      if (event.key.toLocaleLowerCase() === "c") {
        setConnectMode((active) => !active);
        setConnectionSourceId(undefined);
      }
      if (event.key === "Escape") {
        setConnectMode(false);
        setConnectionSourceId(undefined);
        clearSelection();
      }
    };
    window.addEventListener("keydown", shortcuts);
    return () => window.removeEventListener("keydown", shortcuts);
  }, [canEdit, clearSelection, deleteSelected, duplicateSelected, redo, undo]);

  const saveText = useMemo(() => {
    if (effectiveReadOnly) return "Solo lectura";
    if (saveStatus === "saving") return "Guardando…";
    if (saveStatus === "dirty") return "Cambios pendientes";
    if (saveStatus === "conflict") return "Conflicto de versión";
    if (saveStatus === "error") return "Error al guardar";
    return `Guardado · ${saveSource === "api" ? "API" : "local"}`;
  }, [effectiveReadOnly, saveSource, saveStatus]);

  function handleNodeClick(nodeId: string) {
    if (!connectMode || !canEdit) {
      selectNode(nodeId);
      return;
    }
    if (!connectionSourceId) {
      setConnectionSourceId(nodeId);
      selectNode(nodeId);
      return;
    }
    if (connectionSourceId !== nodeId) addEdge(connectionSourceId, nodeId);
    setConnectionSourceId(undefined);
    setConnectMode(false);
  }

  function selectIssue(issue: ValidationIssue) {
    if (issue.nodeId) selectNode(issue.nodeId);
    else if (issue.edgeId) selectEdge(issue.edgeId);
    setFitRequest((value) => value + 1);
  }

  if (loading) {
    return (
      <main className="editor-loading">
        <span className="scene-loader" />
        <strong>Abriendo {effectiveReadOnly ? "visualización" : "borrador"}…</strong>
      </main>
    );
  }

  if (loadError) {
    return (
      <main className="editor-loading">
        <span className="load-error-symbol">!</span>
        <strong>No pudimos abrir este universo</strong>
        <p>{loadError}</p>
        <Link className="secondary-button" href="/">Volver a proyectos</Link>
      </main>
    );
  }

  return (
    <main className={`editor-page ${effectiveReadOnly ? "read-only" : ""}`}>
      <header className="editor-topbar">
        <Link className="editor-brand" href={projectId ? `/proyectos/${projectId}` : "/"} aria-label="Volver a proyectos">
          <span className="brand-mark">FV</span>
          <span aria-hidden="true" className="breadcrumb-arrow">/</span>
          <span className="project-name">{project?.name ?? "Visualización compartida"}</span>
        </Link>
        <div className="flow-name-area">
          {effectiveReadOnly ? (
            <strong>{flow.name}</strong>
          ) : (
            <input
              value={flow.name}
              maxLength={120}
              aria-label="Nombre del flujo"
              onChange={(event) => updateFlowMeta({ name: event.target.value })}
            />
          )}
          <span className={`save-indicator save-${saveStatus}`}><i />{saveText}</span>
        </div>
        <div className="editor-header-actions">
          {canEdit && (
            <>
              <div className="history-buttons">
                <button type="button" onClick={undo} disabled={!pastLength} title="Deshacer">↶</button>
                <button type="button" onClick={redo} disabled={!futureLength} title="Rehacer">↷</button>
              </div>
              <button type="button" className="header-button" onClick={() => setValidationOpen(true)}>
                <span className={errors ? "validation-dot error" : warnings ? "validation-dot warning" : "validation-dot valid"} />
                Validar {errors + warnings > 0 && <b>{errors + warnings}</b>}
              </button>
              {canPublish && (
                <button type="button" className="header-button publish-button" onClick={() => setPublishOpen(true)}>
                  ◇ Publicar
                  {document.draftMatchesPublished && document.publishedVersionNumber && <b>V{document.publishedVersionNumber}</b>}
                </button>
              )}
              {canShare && <button type="button" className="header-button" onClick={() => setShareOpen(true)}>↗ Compartir</button>}
              <button type="button" className="primary-button header-run" onClick={() => setRunOpen(true)}>
                ▶ {runStatus === "idle" ? "Run Flow" : "Nueva ejecución"}
              </button>
            </>
          )}
          <div className="header-menu">
            <button type="button" onClick={() => setMenuOpen((open) => !open)} aria-expanded={menuOpen} aria-label="Más opciones">•••</button>
            {menuOpen && (
              <div className="header-menu-popover">
                <ExportButton />
                <button type="button" className="menu-row" onClick={() => setAccessibleOpen(true)}>☷ Vista de lista accesible</button>
                <button type="button" className="menu-row" onClick={() => setHistoryOpen(true)}>◷ Historial de ejecuciones</button>
              </div>
            )}
          </div>
        </div>
      </header>

      <div className="editor-workspace">
        {canEdit && (
          <NodePalette
            disabled={document.status === "published"}
            onAdd={addNode}
            onOpenText={() => setTextOpen(true)}
            onOpenImport={() => setImportOpen(true)}
          />
        )}
        <section className="canvas-area">
          <div className="canvas-toolbar">
            <div className="tool-group">
              {canEdit && (
                <>
                  <button
                    type="button"
                    className={connectMode ? "active" : ""}
                    onClick={() => {
                      setConnectMode((active) => !active);
                      setConnectionSourceId(undefined);
                    }}
                    title="Crear conexión (C)"
                  >⌁ <span>Conectar</span></button>
                  <button type="button" onClick={duplicateSelected} disabled={!selectedNodeId} title="Duplicar selección">⧉</button>
                  <button type="button" onClick={deleteSelected} disabled={!selectedNodeId && !selectedEdgeId} title="Eliminar selección">⌫</button>
                </>
              )}
              <button type="button" onClick={() => setFitRequest((value) => value + 1)} title="Encuadrar todo">⌗ <span>Encuadrar</span></button>
            </div>
            <div className="layout-switcher" aria-label="Modo de distribución">
              {LAYOUTS.map((layout) => (
                <button
                  type="button"
                  key={layout.id}
                  className={flow.layout.mode === layout.id ? "active" : ""}
                  onClick={() => changeLayout(layout.id)}
                  title={layout.label}
                  disabled={effectiveReadOnly && layout.id === "force"}
                >
                  {layout.icon}<span>{layout.id === "directional" ? layout.label : ""}</span>
                </button>
              ))}
            </div>
          </div>

          {connectMode && (
            <div className="connection-banner" role="status">
              <span>⌁</span>
              {connectionSourceId
                ? <>Origen seleccionado: <strong>{flow.nodes.find((node) => node.id === connectionSourceId)?.label}</strong>. Elige el destino.</>
                : <>Selecciona el nodo de <strong>origen</strong>.</>}
              <button type="button" onClick={() => {
                setConnectMode(false);
                setConnectionSourceId(undefined);
              }}>Cancelar</button>
            </div>
          )}

          <FlowScene
            flow={flow}
            selectedNodeId={selectedNodeId}
            selectedEdgeId={selectedEdgeId}
            activeEdgeId={activeEdgeId}
            nodeStates={nodeStates}
            readOnly={effectiveReadOnly}
            connectionSourceId={connectionSourceId}
            fitRequest={fitRequest}
            onNodeClick={handleNodeClick}
            onEdgeClick={selectEdge}
            onNodeMove={moveNode}
            onBackgroundClick={clearSelection}
          />
          <div className="canvas-badges">
            <span>{flow.nodes.filter((node) => node.type !== "group").length} nodos</span>
            <span>{flow.edges.length} conexiones</span>
            <span className={errors ? "badge-error" : "badge-good"}>{errors ? `${errors} errores` : "✓ estructura válida"}</span>
          </div>
        </section>
        <Inspector readOnly={effectiveReadOnly} />
      </div>

      <RunControls
        readOnly={!canEdit}
        publicView={readOnly}
        onConfigureRun={() => setRunOpen(true)}
        onOpenHistory={() => setHistoryOpen(true)}
      />

      <ImportDialog open={importOpen} onClose={() => setImportOpen(false)} />
      <TextToFlowDialog open={textOpen} onClose={() => setTextOpen(false)} />
      <ValidationDialog open={validationOpen} onClose={() => setValidationOpen(false)} onSelectIssue={selectIssue} />
      <RunDialog open={runOpen} onClose={() => setRunOpen(false)} />
      <PublishDialog open={publishOpen} onClose={() => setPublishOpen(false)} />
      <ShareDialog open={shareOpen} onClose={() => setShareOpen(false)} />
      <HistoryDialog open={historyOpen} onClose={() => setHistoryOpen(false)} />
      <AccessibleGraphDialog open={accessibleOpen} onClose={() => setAccessibleOpen(false)} />
    </main>
  );
}
