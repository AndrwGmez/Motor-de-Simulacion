import type {
  FlowDefinition,
  RunSummary,
  SimulationOverrides,
  SimulationPlan,
} from "@flowverse/core";
import { createSimulationPlan } from "./simulation";

export interface ScenarioCase {
  id: string;
  name: string;
  input: unknown;
  triggerId?: string;
  overrides?: Partial<SimulationOverrides>;
}

export interface ScenarioOutcome {
  scenarioId: string;
  scenarioName: string;
  summary: RunSummary;
  path: string[];
  failedNodeIds: string[];
  plan: SimulationPlan;
}

export type ScenarioVerdict = "unchanged" | "changed" | "regression" | "improvement";

export interface ScenarioComparison {
  verdict: ScenarioVerdict;
  statusChanged: boolean;
  pathChanged: boolean;
  durationDeltaMs: number;
  eventCountDelta: number;
  addedNodeIds: string[];
  removedNodeIds: string[];
  firstDivergence?: {
    index: number;
    baselineNodeId?: string;
    candidateNodeId?: string;
  };
}

export interface ScenarioExperimentCase {
  scenario: ScenarioCase;
  baseline: ScenarioOutcome;
  candidate: ScenarioOutcome;
  comparison: ScenarioComparison;
}

export interface ScenarioLabReport {
  baselineLabel: string;
  candidateLabel: string;
  cases: ScenarioExperimentCase[];
  summary: {
    total: number;
    unchanged: number;
    changed: number;
    regressions: number;
    improvements: number;
    averageDurationDeltaMs: number;
  };
}

function executionPath(plan: SimulationPlan): string[] {
  return plan.events.flatMap((event) => (
    event.type === "node.started" && event.payload.nodeId ? [event.payload.nodeId] : []
  ));
}

function failedNodes(plan: SimulationPlan): string[] {
  return [...new Set(plan.events.flatMap((event) => (
    event.type === "node.failed" && event.payload.nodeId ? [event.payload.nodeId] : []
  )))];
}

export function runScenario(
  flow: FlowDefinition,
  scenario: ScenarioCase,
  flowVersionId = "local-draft",
): ScenarioOutcome {
  const plan = createSimulationPlan(
    flow,
    structuredClone(scenario.input),
    structuredClone(scenario.overrides ?? {}),
    scenario.triggerId,
    flowVersionId,
  );
  return {
    scenarioId: scenario.id,
    scenarioName: scenario.name,
    summary: plan.summary,
    path: executionPath(plan),
    failedNodeIds: failedNodes(plan),
    plan,
  };
}

function firstDivergence(baseline: string[], candidate: string[]): ScenarioComparison["firstDivergence"] {
  const length = Math.max(baseline.length, candidate.length);
  for (let index = 0; index < length; index += 1) {
    if (baseline[index] !== candidate[index]) {
      return {
        index,
        baselineNodeId: baseline[index],
        candidateNodeId: candidate[index],
      };
    }
  }
  return undefined;
}

function outcomeRank(status: RunSummary["status"]): number {
  if (status === "completed") return 2;
  if (status === "cancelled") return 1;
  return 0;
}

export function compareScenarioOutcomes(
  baseline: ScenarioOutcome,
  candidate: ScenarioOutcome,
): ScenarioComparison {
  const baselineNodes = new Set(baseline.path);
  const candidateNodes = new Set(candidate.path);
  const addedNodeIds = [...candidateNodes].filter((id) => !baselineNodes.has(id)).sort();
  const removedNodeIds = [...baselineNodes].filter((id) => !candidateNodes.has(id)).sort();
  const divergence = firstDivergence(baseline.path, candidate.path);
  const statusChanged = baseline.summary.status !== candidate.summary.status;
  const durationDeltaMs = candidate.summary.durationMs - baseline.summary.durationMs;
  const eventCountDelta = candidate.summary.eventCount - baseline.summary.eventCount;
  const changed = statusChanged || Boolean(divergence) || durationDeltaMs !== 0 || eventCountDelta !== 0;
  const rankDelta = outcomeRank(candidate.summary.status) - outcomeRank(baseline.summary.status);
  const verdict: ScenarioVerdict = rankDelta < 0
    ? "regression"
    : rankDelta > 0
      ? "improvement"
      : changed ? "changed" : "unchanged";
  return {
    verdict,
    statusChanged,
    pathChanged: Boolean(divergence),
    durationDeltaMs,
    eventCountDelta,
    addedNodeIds,
    removedNodeIds,
    firstDivergence: divergence,
  };
}

export function compareFlowVariants(
  baseline: FlowDefinition,
  candidate: FlowDefinition,
  scenarios: ScenarioCase[],
  options: {
    baselineLabel?: string;
    candidateLabel?: string;
    baselineVersionId?: string;
    candidateVersionId?: string;
  } = {},
): ScenarioLabReport {
  if (scenarios.length === 0) throw new Error("Scenario Lab requires at least one scenario.");
  if (scenarios.length > 50) throw new Error("Scenario Lab accepts at most 50 scenarios per experiment.");
  const ids = new Set<string>();
  for (const scenario of scenarios) {
    if (!scenario.id.trim() || !scenario.name.trim()) throw new Error("Every scenario requires an id and name.");
    if (ids.has(scenario.id)) throw new Error(`Duplicate scenario id: ${scenario.id}`);
    ids.add(scenario.id);
  }
  const cases = scenarios.map((scenario) => {
    const baselineOutcome = runScenario(baseline, scenario, options.baselineVersionId ?? "baseline");
    const candidateOutcome = runScenario(candidate, scenario, options.candidateVersionId ?? "candidate");
    return {
      scenario: structuredClone(scenario),
      baseline: baselineOutcome,
      candidate: candidateOutcome,
      comparison: compareScenarioOutcomes(baselineOutcome, candidateOutcome),
    };
  });
  const durationTotal = cases.reduce((total, item) => total + item.comparison.durationDeltaMs, 0);
  return {
    baselineLabel: options.baselineLabel ?? "Base",
    candidateLabel: options.candidateLabel ?? "Candidato",
    cases,
    summary: {
      total: cases.length,
      unchanged: cases.filter((item) => item.comparison.verdict === "unchanged").length,
      changed: cases.filter((item) => item.comparison.verdict === "changed").length,
      regressions: cases.filter((item) => item.comparison.verdict === "regression").length,
      improvements: cases.filter((item) => item.comparison.verdict === "improvement").length,
      averageDurationDeltaMs: durationTotal / cases.length,
    },
  };
}

/** Compare two inputs or override sets against the same flow. */
export function compareScenarios(
  flow: FlowDefinition,
  baselineScenario: ScenarioCase,
  candidateScenario: ScenarioCase,
  flowVersionId = "local-draft",
): {
  baseline: ScenarioOutcome;
  candidate: ScenarioOutcome;
  comparison: ScenarioComparison;
} {
  const baseline = runScenario(flow, baselineScenario, flowVersionId);
  const candidate = runScenario(flow, candidateScenario, flowVersionId);
  return {
    baseline,
    candidate,
    comparison: compareScenarioOutcomes(baseline, candidate),
  };
}
