"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { FlowDefinition, FlowVersion, SimulationPlan } from "@flowverse/core";
import {
  compareFlowVariants,
  compareScenarios,
  type ScenarioCase,
  type ScenarioComparison,
  type ScenarioLabReport,
  type ScenarioOutcome,
  type ScenarioVerdict,
} from "@flowverse/engine";
import { getFlowVersion, listFlowVersions } from "@/lib/version-service";
import {
  MAX_SCENARIO_PRESETS,
  deleteScenarioPreset,
  listScenarioPresets,
  saveScenarioPreset,
  type ScenarioLabPreset,
} from "@/lib/scenario-presets";
import { useFlowStore } from "@/store/flow-store";
import { Modal } from "./EditorDialogs";

type LabMode = "scenarios" | "versions";

interface ScenarioForm {
  name: string;
  input: string;
  triggerId: string;
  failedNodeId: string;
  forcedNodeId: string;
  forcedEdgeId: string;
}

interface ScenarioPairResult {
  kind: "scenarios";
  baseline: ScenarioOutcome;
  candidate: ScenarioOutcome;
  comparison: ScenarioComparison;
  definition: FlowDefinition;
}

interface VersionResult {
  kind: "versions";
  report: ScenarioLabReport;
  baselineDefinition: FlowDefinition;
  candidateDefinition: FlowDefinition;
}

type LabResult = ScenarioPairResult | VersionResult;

const EMPTY_VERSIONS: FlowVersion[] = [];

const VERDICT_COPY: Record<ScenarioVerdict, { label: string; detail: string; icon: string }> = {
  unchanged: {
    label: "Sin cambios",
    detail: "Ambos recorridos conservan estado, ruta y coste lógico.",
    icon: "=",
  },
  changed: {
    label: "Comportamiento distinto",
    detail: "La comparación detectó una diferencia observable.",
    icon: "Δ",
  },
  regression: {
    label: "Regresión detectada",
    detail: "El candidato termina en un estado peor que la referencia.",
    icon: "↓",
  },
  improvement: {
    label: "Mejora detectada",
    detail: "El candidato termina en un estado mejor que la referencia.",
    icon: "↑",
  },
};

function defaultInput(flow: FlowDefinition): unknown {
  const result: Record<string, unknown> = {};
  for (const variable of flow.variables) {
    const parts = variable.path
      .split("/")
      .filter(Boolean)
      .map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"));
    if (parts.length === 0 || parts.some((part) => ["__proto__", "prototype", "constructor"].includes(part))) continue;
    let cursor = result;
    for (const part of parts.slice(0, -1)) {
      const existing = cursor[part];
      if (!existing || typeof existing !== "object" || Array.isArray(existing)) cursor[part] = {};
      cursor = cursor[part] as Record<string, unknown>;
    }
    cursor[parts.at(-1)!] = structuredClone(variable.default ?? null);
  }
  return result;
}

function initialForm(name: string, flow: FlowDefinition): ScenarioForm {
  return {
    name,
    input: JSON.stringify(defaultInput(flow), null, 2),
    triggerId: "",
    failedNodeId: "",
    forcedNodeId: "",
    forcedEdgeId: "",
  };
}

function formFromScenario(scenario: ScenarioCase): ScenarioForm {
  const failedNodeId = scenario.overrides?.failedNodeIds?.[0] ?? "";
  const forcedEntry = Object.entries(scenario.overrides?.forcedEdgeIds ?? {})[0];
  return {
    name: scenario.name,
    input: JSON.stringify(scenario.input, null, 2),
    triggerId: scenario.triggerId ?? "",
    failedNodeId,
    forcedNodeId: forcedEntry?.[0] ?? "",
    forcedEdgeId: forcedEntry?.[1] ?? "",
  };
}

function parseScenario(form: ScenarioForm, id: string, flow: FlowDefinition): ScenarioCase {
  const name = form.name.trim();
  if (!name) throw new Error(`El escenario ${id === "scenario-a" ? "A" : "B"} necesita un nombre.`);
  if (name.length > 80) throw new Error("Los nombres de escenario no pueden superar 80 caracteres.");
  if (new Blob([form.input]).size > 32 * 1024) throw new Error("Cada entrada JSON puede ocupar como máximo 32 KB.");

  let input: unknown;
  try {
    input = JSON.parse(form.input) as unknown;
  } catch {
    throw new Error(`La entrada de ${name} no contiene JSON válido.`);
  }
  if (!input || typeof input !== "object" || Array.isArray(input)) {
    throw new Error(`La entrada de ${name} debe ser un objeto JSON.`);
  }

  if (form.triggerId && !flow.nodes.some((node) => node.id === form.triggerId && node.type === "trigger")) {
    throw new Error(`El inicio elegido para ${name} ya no existe.`);
  }
  if (form.failedNodeId && !flow.nodes.some((node) => node.id === form.failedNodeId)) {
    throw new Error(`El nodo de fallo elegido para ${name} ya no existe.`);
  }
  const forcedEdge = flow.edges.find((edge) => edge.id === form.forcedEdgeId);
  if (form.forcedNodeId && (!forcedEdge || forcedEdge.source !== form.forcedNodeId)) {
    throw new Error(`La ruta forzada de ${name} ya no es válida.`);
  }

  return {
    id,
    name,
    input,
    triggerId: form.triggerId || undefined,
    overrides: {
      failedNodeIds: form.failedNodeId ? [form.failedNodeId] : [],
      forcedEdgeIds: form.forcedNodeId && form.forcedEdgeId
        ? { [form.forcedNodeId]: form.forcedEdgeId }
        : {},
    },
  };
}

function signed(value: number, suffix = ""): string {
  if (value === 0) return `0${suffix}`;
  return `${value > 0 ? "+" : ""}${value}${suffix}`;
}

function statusLabel(status: ScenarioOutcome["summary"]["status"]): string {
  if (status === "completed") return "Completada";
  if (status === "cancelled") return "Cancelada";
  return "Fallida";
}

function versionDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "fecha desconocida";
  return new Intl.DateTimeFormat("es-CO", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function nodeName(id: string | undefined, primary: FlowDefinition, secondary?: FlowDefinition): string {
  if (!id) return "Fin de la ruta";
  return primary.nodes.find((node) => node.id === id)?.label
    ?? secondary?.nodes.find((node) => node.id === id)?.label
    ?? id;
}

function ScenarioEditor({
  side,
  value,
  flow,
  onChange,
}: {
  side: "A" | "B";
  value: ScenarioForm;
  flow: FlowDefinition;
  onChange: (next: ScenarioForm) => void;
}) {
  const decisions = flow.nodes.filter((node) => node.type === "decision");
  const availableEdges = flow.edges.filter((edge) => edge.source === value.forcedNodeId);
  const failureNodes = flow.nodes.filter((node) => !["trigger", "end", "group"].includes(node.type));
  const triggers = flow.nodes.filter((node) => node.type === "trigger");
  const prefix = `scenario-${side.toLowerCase()}`;

  return (
    <article className={`scenario-card scenario-${side.toLowerCase()}`} aria-label={`Escenario ${side}`}>
      <header>
        <span>{side}</span>
        <div>
          <strong>{side === "A" ? "Referencia" : "Candidato"}</strong>
          <small>{side === "A" ? "La hipótesis base" : "La variante que quieres medir"}</small>
        </div>
      </header>

      <div className="field compact-field">
        <label htmlFor={`${prefix}-name`}>Nombre</label>
        <input
          id={`${prefix}-name`}
          value={value.name}
          maxLength={80}
          onChange={(event) => onChange({ ...value, name: event.target.value })}
        />
      </div>
      <label className="scenario-json-label" htmlFor={`${prefix}-input`}>
        <span>Entrada JSON</span>
        <small aria-hidden="true">{new Blob([value.input]).size} / 32.768 bytes</small>
      </label>
      <textarea
        id={`${prefix}-input`}
        className="code-input scenario-json"
        value={value.input}
        rows={8}
        spellCheck={false}
        onChange={(event) => onChange({ ...value, input: event.target.value })}
      />

      <details className="scenario-overrides">
        <summary>Overrides opcionales <span>{value.failedNodeId || value.forcedEdgeId ? "ACTIVOS" : "NINGUNO"}</span></summary>
        <div className="scenario-override-fields">
          {triggers.length > 1 && (
            <div className="field compact-field">
              <label htmlFor={`${prefix}-trigger`}>Punto de inicio</label>
              <select
                id={`${prefix}-trigger`}
                value={value.triggerId}
                onChange={(event) => onChange({ ...value, triggerId: event.target.value })}
              >
                <option value="">Inicio predeterminado</option>
                {triggers.map((node) => <option value={node.id} key={node.id}>{node.label}</option>)}
              </select>
            </div>
          )}
          <div className="field compact-field">
            <label htmlFor={`${prefix}-failure`}>Forzar fallo</label>
            <select
              id={`${prefix}-failure`}
              value={value.failedNodeId}
              onChange={(event) => onChange({ ...value, failedNodeId: event.target.value })}
            >
              <option value="">Sin fallo forzado</option>
              {failureNodes.map((node) => <option value={node.id} key={node.id}>{node.label}</option>)}
            </select>
          </div>
          <div className="field compact-field">
            <label htmlFor={`${prefix}-decision`}>Forzar decisión</label>
            <select
              id={`${prefix}-decision`}
              value={value.forcedNodeId}
              onChange={(event) => onChange({
                ...value,
                forcedNodeId: event.target.value,
                forcedEdgeId: "",
              })}
            >
              <option value="">Evaluar condiciones</option>
              {decisions.map((node) => <option value={node.id} key={node.id}>{node.label}</option>)}
            </select>
          </div>
          {value.forcedNodeId && (
            <div className="field compact-field">
              <label htmlFor={`${prefix}-route`}>Ruta elegida</label>
              <select
                id={`${prefix}-route`}
                value={value.forcedEdgeId}
                onChange={(event) => onChange({ ...value, forcedEdgeId: event.target.value })}
              >
                <option value="">Selecciona una ruta</option>
                {availableEdges.map((edge) => (
                  <option value={edge.id} key={edge.id}>
                    {edge.label || nodeName(edge.target, flow)} → {nodeName(edge.target, flow)}
                  </option>
                ))}
              </select>
            </div>
          )}
        </div>
      </details>
    </article>
  );
}

function OutcomeCard({
  label,
  outcome,
  tone,
  onReplay,
}: {
  label: string;
  outcome: ScenarioOutcome;
  tone: "baseline" | "candidate";
  onReplay: (plan: SimulationPlan, label: string) => void;
}) {
  return (
    <article className={`scenario-outcome ${tone}`}>
      <div>
        <span>{label}</span>
        <strong className={`outcome-${outcome.summary.status}`}>{statusLabel(outcome.summary.status)}</strong>
        <small>{outcome.scenarioName}</small>
      </div>
      <dl>
        <div><dt>Tiempo</dt><dd>{outcome.summary.durationMs} ms</dd></div>
        <div><dt>Eventos</dt><dd>{outcome.summary.eventCount}</dd></div>
        <div><dt>Ruta</dt><dd>{outcome.path.length} nodos</dd></div>
      </dl>
      <button
        type="button"
        className="secondary-button"
        aria-label={`Reproducir plan: ${label} — ${outcome.scenarioName}`}
        onClick={() => onReplay(outcome.plan, label)}
      >
        ▶ Reproducir este plan
      </button>
    </article>
  );
}

function ComparisonResult({
  baseline,
  candidate,
  comparison,
  baselineLabel,
  candidateLabel,
  baselineDefinition,
  candidateDefinition,
  onReplay,
}: {
  baseline: ScenarioOutcome;
  candidate: ScenarioOutcome;
  comparison: ScenarioComparison;
  baselineLabel: string;
  candidateLabel: string;
  baselineDefinition: FlowDefinition;
  candidateDefinition: FlowDefinition;
  onReplay: (plan: SimulationPlan, label: string) => void;
}) {
  const verdict = VERDICT_COPY[comparison.verdict];
  return (
    <section className="scenario-result" aria-live="polite" aria-label="Resultado de la comparación">
      <div className={`scenario-verdict verdict-${comparison.verdict}`}>
        <span aria-hidden="true">{verdict.icon}</span>
        <div><strong>{verdict.label}</strong><p>{verdict.detail}</p></div>
        <b>{comparison.verdict.toLocaleUpperCase()}</b>
      </div>

      <div className="scenario-outcomes">
        <OutcomeCard label={baselineLabel} outcome={baseline} tone="baseline" onReplay={onReplay} />
        <span className="scenario-versus" aria-hidden="true">VS</span>
        <OutcomeCard label={candidateLabel} outcome={candidate} tone="candidate" onReplay={onReplay} />
      </div>

      <div className="scenario-deltas" aria-label="Deltas del candidato">
        <article className={comparison.durationDeltaMs > 0 ? "negative" : comparison.durationDeltaMs < 0 ? "positive" : ""}>
          <span>Δ TIEMPO</span><strong>{signed(comparison.durationDeltaMs, " ms")}</strong><small>Candidato − referencia</small>
        </article>
        <article>
          <span>Δ EVENTOS</span><strong>{signed(comparison.eventCountDelta)}</strong><small>Eventos lógicos</small>
        </article>
        <article className={comparison.statusChanged ? "negative" : "positive"}>
          <span>ESTADO</span><strong>{comparison.statusChanged ? "Cambió" : "Estable"}</strong><small>{statusLabel(baseline.summary.status)} → {statusLabel(candidate.summary.status)}</small>
        </article>
      </div>

      <div className="scenario-divergence">
        <div className="divergence-heading">
          <span>{comparison.firstDivergence ? `#${comparison.firstDivergence.index + 1}` : "✓"}</span>
          <div>
            <strong>{comparison.firstDivergence ? "Primera divergencia" : "Ruta sin divergencias"}</strong>
            <small>{comparison.firstDivergence ? "Primer punto donde los recorridos dejan de coincidir" : "Los nodos visitados conservan el mismo orden"}</small>
          </div>
        </div>
        {comparison.firstDivergence && (
          <div className="divergence-path">
            <span><i>A</i>{nodeName(comparison.firstDivergence.baselineNodeId, baselineDefinition, candidateDefinition)}</span>
            <b>≠</b>
            <span><i>B</i>{nodeName(comparison.firstDivergence.candidateNodeId, candidateDefinition, baselineDefinition)}</span>
          </div>
        )}
      </div>

      <div className="scenario-route-diff">
        <article>
          <span className="route-added">+ Rutas añadidas</span>
          <div>{comparison.addedNodeIds.length
            ? comparison.addedNodeIds.map((id) => <b key={id}>{nodeName(id, candidateDefinition)}</b>)
            : <small>Ninguna</small>}</div>
        </article>
        <article>
          <span className="route-removed">− Rutas eliminadas</span>
          <div>{comparison.removedNodeIds.length
            ? comparison.removedNodeIds.map((id) => <b key={id}>{nodeName(id, baselineDefinition)}</b>)
            : <small>Ninguna</small>}</div>
        </article>
      </div>
    </section>
  );
}

export function ScenarioLabDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const document = useFlowStore((state) => state.document);
  const startSimulation = useFlowStore((state) => state.startSimulation);
  const [mode, setMode] = useState<LabMode>("scenarios");
  const [scenarioA, setScenarioA] = useState(() => initialForm("Caso base", document.definition));
  const [scenarioB, setScenarioB] = useState(() => initialForm("Caso alternativo", document.definition));
  const [selectedVersionId, setSelectedVersionId] = useState("");
  const [result, setResult] = useState<LabResult>();
  const [selectedCaseId, setSelectedCaseId] = useState("scenario-a");
  const [error, setError] = useState("");
  const [presetName, setPresetName] = useState("Comparación rápida");
  const [selectedPresetId, setSelectedPresetId] = useState("");
  const [presets, setPresets] = useState<ScenarioLabPreset[]>([]);

  const versionsQuery = useQuery({
    queryKey: ["flow-versions", document.flowId, "scenario-lab"],
    queryFn: () => listFlowVersions(document.flowId),
    enabled: open && mode === "versions",
    staleTime: 0,
  });
  const versions = versionsQuery.data ?? EMPTY_VERSIONS;
  const selectedVersion = versions.find((version) => version.id === selectedVersionId);
  const snapshotQuery = useQuery({
    queryKey: ["flow-version", document.flowId, selectedVersionId],
    queryFn: () => getFlowVersion(selectedVersionId, document.flowId),
    enabled: open && mode === "versions" && Boolean(selectedVersionId),
    staleTime: Infinity,
  });

  useEffect(() => {
    if (!open) return;
    setResult(undefined);
    setError("");
    setPresets(listScenarioPresets(document.flowId));
  }, [document.flowId, open]);

  useEffect(() => {
    if (mode !== "versions") return;
    if (!versions.some((version) => version.id === selectedVersionId)) {
      setSelectedVersionId(versions[0]?.id ?? "");
    }
  }, [mode, selectedVersionId, versions]);

  const activeVersionCase = result?.kind === "versions"
    ? result.report.cases.find((item) => item.scenario.id === selectedCaseId) ?? result.report.cases[0]
    : undefined;

  const modeHelp = useMemo(() => mode === "scenarios"
    ? "Aísla el efecto de entradas y overrides sobre el mismo borrador."
    : "Ejecuta cada escenario sobre una versión inmutable y sobre el borrador actual.", [mode]);

  function readScenarios(): [ScenarioCase, ScenarioCase] {
    return [
      parseScenario(scenarioA, "scenario-a", document.definition),
      parseScenario(scenarioB, "scenario-b", document.definition),
    ];
  }

  function executeComparison() {
    setError("");
    try {
      const [baselineScenario, candidateScenario] = readScenarios();
      if (mode === "scenarios") {
        const comparison = compareScenarios(
          document.definition,
          baselineScenario,
          candidateScenario,
          document.versionId,
        );
        setResult({ kind: "scenarios", ...comparison, definition: structuredClone(document.definition) });
        return;
      }
      const snapshot = snapshotQuery.data;
      if (!snapshot) throw new Error(snapshotQuery.isLoading
        ? "La versión todavía se está cargando."
        : "Selecciona una versión publicada disponible.");
      const report = compareFlowVariants(snapshot.definition, document.definition, [baselineScenario, candidateScenario], {
        baselineLabel: `Versión ${snapshot.version.number}`,
        candidateLabel: "Borrador actual",
        baselineVersionId: snapshot.version.id,
        candidateVersionId: document.versionId,
      });
      setSelectedCaseId(report.cases[0].scenario.id);
      setResult({
        kind: "versions",
        report,
        baselineDefinition: structuredClone(snapshot.definition),
        candidateDefinition: structuredClone(document.definition),
      });
    } catch (cause) {
      setResult(undefined);
      setError(cause instanceof Error ? cause.message : "No se pudo ejecutar la comparación.");
    }
  }

  function savePreset() {
    setError("");
    try {
      const [first, second] = readScenarios();
      const saved = saveScenarioPreset(document.flowId, {
        id: selectedPresetId || undefined,
        name: presetName,
        scenarioA: first,
        scenarioB: second,
      });
      setPresets(listScenarioPresets(document.flowId));
      setSelectedPresetId(saved.id);
      setPresetName(saved.name);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "No se pudo guardar el preset.");
    }
  }

  function loadPreset() {
    const preset = presets.find((candidate) => candidate.id === selectedPresetId);
    if (!preset) return;
    setScenarioA(formFromScenario(preset.scenarioA));
    setScenarioB(formFromScenario(preset.scenarioB));
    setPresetName(preset.name);
    setResult(undefined);
    setError("");
  }

  function removePreset() {
    if (!selectedPresetId) return;
    deleteScenarioPreset(document.flowId, selectedPresetId);
    setPresets(listScenarioPresets(document.flowId));
    setSelectedPresetId("");
  }

  function replay(plan: SimulationPlan) {
    startSimulation(structuredClone(plan));
    onClose();
  }

  return (
    <Modal open={open} onClose={onClose} eyebrow="EXPERIMENTACIÓN DETERMINISTA" title="Scenario Lab" wide>
      <div className="scenario-lab">
        <section className="scenario-lab-hero">
          <div className="scenario-lab-orbit" aria-hidden="true"><i>A</i><span /><i>B</i></div>
          <div>
            <strong>Prueba hipótesis antes de publicar</strong>
            <p>Compara rutas, resultados y tiempo lógico con evidencia reproducible sobre el mismo motor.</p>
          </div>
          <b>LOCAL · SIN EFECTOS REALES</b>
        </section>

        <div className="scenario-mode-tabs" role="tablist" aria-label="Tipo de experimento">
          <button
            type="button"
            role="tab"
            aria-selected={mode === "scenarios"}
            className={mode === "scenarios" ? "active" : ""}
            onClick={() => { setMode("scenarios"); setResult(undefined); setError(""); }}
          ><span>A/B</span><div><strong>Escenario vs escenario</strong><small>Mismo borrador</small></div></button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === "versions"}
            className={mode === "versions" ? "active" : ""}
            onClick={() => { setMode("versions"); setResult(undefined); setError(""); }}
          ><span>VΔ</span><div><strong>Versión vs borrador</strong><small>Mismas entradas</small></div></button>
        </div>
        <p className="scenario-mode-help">{modeHelp}</p>

        <section className="scenario-presets" aria-label="Presets de escenarios">
          <div>
            <span aria-hidden="true">◈</span>
            <label htmlFor="scenario-preset-name">Preset</label>
            <input
              id="scenario-preset-name"
              aria-label="Nombre del preset"
              value={presetName}
              maxLength={80}
              onChange={(event) => setPresetName(event.target.value)}
            />
          </div>
          <select
            aria-label="Preset guardado"
            value={selectedPresetId}
            onChange={(event) => setSelectedPresetId(event.target.value)}
          >
            <option value="">{presets.length ? "Elegir preset guardado" : "Sin presets guardados"}</option>
            {presets.map((preset) => <option value={preset.id} key={preset.id}>{preset.name}</option>)}
          </select>
          <button type="button" className="secondary-button" onClick={savePreset}>Guardar</button>
          <button type="button" className="text-button" disabled={!selectedPresetId} onClick={loadPreset}>Cargar</button>
          <button type="button" className="scenario-delete-preset" disabled={!selectedPresetId} onClick={removePreset} aria-label="Eliminar preset">×</button>
          <small>{presets.length}/{MAX_SCENARIO_PRESETS}</small>
        </section>

        {mode === "versions" && (
          <section className="scenario-version-selector" aria-label="Versión de referencia">
            <div><span>V</span><div><strong>Referencia inmutable</strong><small>El borrador nunca se modifica durante la prueba.</small></div></div>
            {versionsQuery.isLoading ? (
              <span className="scenario-query-state" role="status">Cargando versiones…</span>
            ) : versionsQuery.error ? (
              <button type="button" className="secondary-button" onClick={() => void versionsQuery.refetch()}>Reintentar</button>
            ) : versions.length ? (
              <select
                aria-label="Versión de referencia"
                value={selectedVersionId}
                onChange={(event) => { setSelectedVersionId(event.target.value); setResult(undefined); }}
              >
                {versions.map((version) => (
                  <option value={version.id} key={version.id}>Versión {version.number} · {versionDate(version.publishedAt)}</option>
                ))}
              </select>
            ) : (
              <span className="scenario-query-state">Publica una versión para habilitar esta comparación.</span>
            )}
            {selectedVersion && <b>V{selectedVersion.number}</b>}
          </section>
        )}

        <div className="scenario-editors">
          <ScenarioEditor side="A" value={scenarioA} flow={document.definition} onChange={(next) => { setScenarioA(next); setResult(undefined); }} />
          <span className="scenario-editor-arrow" aria-hidden="true">→</span>
          <ScenarioEditor side="B" value={scenarioB} flow={document.definition} onChange={(next) => { setScenarioB(next); setResult(undefined); }} />
        </div>

        {error && <p className="dialog-error scenario-error" role="alert">⚠ {error}</p>}
        <button
          type="button"
          className="primary-button scenario-compare-button"
          disabled={mode === "versions" && (!selectedVersionId || snapshotQuery.isLoading)}
          onClick={executeComparison}
        >
          <span aria-hidden="true">✦</span>
          {mode === "scenarios" ? "Comparar escenarios" : "Comparar versión y borrador"}
        </button>

        {result?.kind === "scenarios" && (
          <ComparisonResult
            baseline={result.baseline}
            candidate={result.candidate}
            comparison={result.comparison}
            baselineLabel="Escenario A"
            candidateLabel="Escenario B"
            baselineDefinition={result.definition}
            candidateDefinition={result.definition}
            onReplay={(plan) => replay(plan)}
          />
        )}

        {result?.kind === "versions" && activeVersionCase && (
          <>
            <section className="scenario-report-summary" aria-label="Resumen del experimento">
              <div><span>{result.report.summary.total}</span><small>casos</small></div>
              <div className="regression"><span>{result.report.summary.regressions}</span><small>regresiones</small></div>
              <div className="improvement"><span>{result.report.summary.improvements}</span><small>mejoras</small></div>
              <div><span>{signed(result.report.summary.averageDurationDeltaMs, " ms")}</span><small>Δ medio</small></div>
            </section>
            <div className="scenario-case-tabs" role="tablist" aria-label="Casos comparados">
              {result.report.cases.map((item, index) => (
                <button
                  type="button"
                  role="tab"
                  aria-selected={item.scenario.id === activeVersionCase.scenario.id}
                  className={item.scenario.id === activeVersionCase.scenario.id ? "active" : ""}
                  key={item.scenario.id}
                  onClick={() => setSelectedCaseId(item.scenario.id)}
                >
                  <span>{index === 0 ? "A" : "B"}</span>
                  <strong>{item.scenario.name}</strong>
                  <i className={`case-${item.comparison.verdict}`}>{VERDICT_COPY[item.comparison.verdict].label}</i>
                </button>
              ))}
            </div>
            <ComparisonResult
              baseline={activeVersionCase.baseline}
              candidate={activeVersionCase.candidate}
              comparison={activeVersionCase.comparison}
              baselineLabel={result.report.baselineLabel}
              candidateLabel={result.report.candidateLabel}
              baselineDefinition={result.baselineDefinition}
              candidateDefinition={result.candidateDefinition}
              onReplay={(plan) => replay(plan)}
            />
          </>
        )}
      </div>
    </Modal>
  );
}
