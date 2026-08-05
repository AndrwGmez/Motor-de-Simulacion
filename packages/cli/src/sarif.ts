import type { SemanticChange, ValidationIssue } from "@flowverse/core";
import type { CheckReport, CommandReport, SarifLog, SarifResult } from "./types";

const CLI_VERSION = "0.1.0";

function uri(file: string): string {
  return file.replaceAll("\\", "/");
}

function location(file: string): SarifResult["locations"] {
  return [{ physicalLocation: { artifactLocation: { uri: uri(file) } } }];
}

function issueLevel(issue: ValidationIssue): SarifResult["level"] {
  if (issue.severity === "error") return "error";
  if (issue.severity === "warning") return "warning";
  return "note";
}

function issueResult(issue: ValidationIssue, file: string): SarifResult {
  return {
    ruleId: issue.code,
    level: issueLevel(issue),
    message: { text: issue.message },
    locations: location(file),
    properties: {
      issueId: issue.id,
      severity: issue.severity,
      ...(issue.nodeId ? { nodeId: issue.nodeId } : {}),
      ...(issue.edgeId ? { edgeId: issue.edgeId } : {}),
    },
  };
}

function changeLevel(change: SemanticChange, policyViolation: boolean): SarifResult["level"] {
  if (policyViolation) return "error";
  if (change.impact === "breaking") return "warning";
  if (change.impact === "behavioral") return "warning";
  return "note";
}

function changeResult(
  change: SemanticChange,
  file: string,
  policyViolation = false,
): SarifResult {
  const fields = change.fields.map((field) => field.path);
  const detail = fields.length > 0 ? ` (${fields.join(", ")})` : "";
  return {
    ruleId: `flowverse.semantic.${change.impact}`,
    level: changeLevel(change, policyViolation),
    message: {
      text: `${change.operation} ${change.entity} ${change.label ?? change.id}${detail}`,
    },
    locations: location(file),
    properties: {
      impact: change.impact,
      operation: change.operation,
      entity: change.entity,
      entityId: change.id,
      fields,
      policyViolation,
    },
  };
}

function resultsFor(report: CommandReport): SarifResult[] {
  switch (report.command) {
    case "validate":
      return report.issues.map((issue) => issueResult(issue, report.file));
    case "diff":
      return report.diff.changes.map((change) => changeResult(change, report.candidate));
    case "simulate": {
      const results = report.issues.map((issue) => issueResult(issue, report.file));
      if (report.plan?.summary.status === "failed") {
        results.push({
          ruleId: "flowverse.simulation.failed",
          level: "warning",
          message: { text: "La simulación terminó con estado failed." },
          locations: location(report.file),
          properties: { runId: report.plan.runId },
        });
      }
      return results;
    }
    case "check": {
      const violatingChanges = new Set(report.failures.flatMap((failure) => failure.change ? [failure.change] : []));
      return [
        ...report.issues.map((issue) => issueResult(issue, report.file)),
        ...(report.diff?.changes.map((change) => changeResult(change, report.file, violatingChanges.has(change))) ?? []),
      ];
    }
  }
}

function reportProperties(report: CommandReport): Record<string, unknown> {
  switch (report.command) {
    case "validate":
      return { command: report.command, valid: report.valid, metrics: report.metrics };
    case "diff":
      return { command: report.command, baseline: report.baseline, candidate: report.candidate, summary: report.diff.summary };
    case "simulate":
      return { command: report.command, valid: report.valid, summary: report.plan?.summary };
    case "check":
      return {
        command: report.command,
        passed: report.passed,
        baseline: report.baseline,
        policy: report.policy,
        failureCount: report.failures.length,
        diffSummary: report.diff?.summary,
      };
  }
}

export function toSarif(report: CommandReport): SarifLog {
  const results = resultsFor(report);
  const levels = new Map<string, SarifResult["level"]>();
  for (const result of results) {
    const current = levels.get(result.ruleId);
    const rank = { note: 0, warning: 1, error: 2 };
    if (!current || rank[result.level] > rank[current]) levels.set(result.ruleId, result.level);
  }
  return {
    $schema: "https://json.schemastore.org/sarif-2.1.0.json",
    version: "2.1.0",
    runs: [{
      tool: {
        driver: {
          name: "FlowVerse CLI",
          version: CLI_VERSION,
          informationUri: "https://github.com/AndrwGmez/Motor-de-Simulaci-n",
          rules: [...levels.entries()].sort(([left], [right]) => left.localeCompare(right)).map(([id, level]) => ({
            id,
            shortDescription: { text: id },
            defaultConfiguration: { level },
          })),
        },
      },
      results,
      properties: reportProperties(report),
    }],
  };
}

export function isCheckReport(report: CommandReport): report is CheckReport {
  return report.command === "check";
}
