import { diffFlowDefinitions, type FlowDefinition, type SemanticChange } from "@flowverse/core";
import { createSimulationPlan, flowMetrics, validateFlow } from "@flowverse/engine";
import type {
  CheckOptions,
  CheckReport,
  CheckThreshold,
  DiffReport,
  SimulationOptions,
  SimulationReport,
  ValidationReport,
} from "./types";
import { IMPACT_RANK } from "./types";

export function validateDefinition(file: string, definition: FlowDefinition): ValidationReport {
  const issues = validateFlow(definition);
  return {
    command: "validate",
    file,
    valid: !issues.some((issue) => issue.severity === "error"),
    issues,
    metrics: flowMetrics(definition, issues),
  };
}

export function invalidValidationReport(file: string, issues: ValidationReport["issues"]): ValidationReport {
  return {
    command: "validate",
    file,
    valid: false,
    issues,
    metrics: {
      nodeCount: 0,
      edgeCount: 0,
      reachableCount: 0,
      coveragePercent: 0,
      complexity: 0,
      cycleCount: 0,
      errors: issues.filter((issue) => issue.severity === "error").length,
      warnings: issues.filter((issue) => issue.severity === "warning").length,
    },
  };
}

export function diffDefinitions(
  baselinePath: string,
  baseline: FlowDefinition,
  candidatePath: string,
  candidate: FlowDefinition,
): DiffReport {
  return {
    command: "diff",
    baseline: baselinePath,
    candidate: candidatePath,
    diff: diffFlowDefinitions(baseline, candidate),
  };
}

export function simulateDefinition(
  file: string,
  definition: FlowDefinition,
  options: SimulationOptions = {},
): SimulationReport {
  const validation = validateDefinition(file, definition);
  const input = structuredClone(options.input ?? {});
  const overrides = structuredClone(options.overrides ?? {});
  return {
    command: "simulate",
    file,
    valid: validation.valid,
    issues: validation.issues,
    input,
    overrides,
    triggerId: options.triggerId,
    plan: validation.valid
      ? createSimulationPlan(
          definition,
          input,
          overrides,
          options.triggerId,
          "local-draft",
          { limits: options.limits },
        )
      : undefined,
  };
}

export function invalidSimulationReport(file: string, issues: SimulationReport["issues"]): SimulationReport {
  return {
    command: "simulate",
    file,
    valid: false,
    issues,
    input: {},
    overrides: {},
  };
}

function changeViolatesThreshold(change: SemanticChange, threshold: CheckThreshold): boolean {
  if (threshold === "none") return false;
  return IMPACT_RANK[change.impact] >= IMPACT_RANK[threshold];
}

export function checkDefinition(
  file: string,
  definition: FlowDefinition,
  options: CheckOptions = {},
): CheckReport {
  const validation = validateDefinition(file, definition);
  const threshold = options.failOn ?? "none";
  const diff = options.baseline
    ? diffFlowDefinitions(options.baseline, definition)
    : undefined;
  const failures: CheckReport["failures"] = validation.issues
    .filter((issue) => issue.severity === "error")
    .map((issue) => ({
      kind: "validation" as const,
      message: issue.message,
      issue,
    }));

  if (diff) {
    failures.push(...diff.changes
      .filter((change) => changeViolatesThreshold(change, threshold))
      .map((change) => ({
        kind: "semantic-change" as const,
        message: `${change.impact}: ${change.operation} ${change.entity} ${change.label ?? change.id}`,
        change,
      })));
  }

  return {
    command: "check",
    file,
    baseline: options.baselinePath,
    valid: validation.valid,
    issues: validation.issues,
    diff,
    policy: { failOn: threshold },
    failures,
    passed: failures.length === 0,
  };
}

export function invalidCheckReport(
  file: string,
  issues: CheckReport["issues"],
  threshold: CheckThreshold,
): CheckReport {
  return {
    command: "check",
    file,
    valid: false,
    issues,
    policy: { failOn: threshold },
    failures: issues.map((issue) => ({ kind: "validation", message: issue.message, issue })),
    passed: false,
  };
}
