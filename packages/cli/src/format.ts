import type { SemanticChange, ValidationIssue } from "@flowverse/core";
import { toSarif } from "./sarif";
import type { CommandReport, DiffReport, OutputFormat } from "./types";

function issueLine(issue: ValidationIssue): string {
  const marker = issue.severity === "error" ? "ERROR" : issue.severity === "warning" ? "WARN" : "INFO";
  const target = issue.nodeId ? ` node=${issue.nodeId}` : issue.edgeId ? ` edge=${issue.edgeId}` : "";
  return `  [${marker}] ${issue.code}${target}: ${issue.message}`;
}

function changeLine(change: SemanticChange): string {
  const fields = change.fields.length ? ` [${change.fields.map((field) => field.path).join(", ")}]` : "";
  return `  [${change.impact.toUpperCase()}] ${change.operation} ${change.entity} ${change.label ?? change.id}${fields}`;
}

function diffLines(report: DiffReport): string[] {
  const { diff } = report;
  if (!diff.hasChanges) return [`Sin cambios semánticos entre ${report.baseline} y ${report.candidate}.`];
  return [
    `Diff ${report.baseline} → ${report.candidate}`,
    `Impacto máximo: ${diff.highestImpact} · +${diff.summary.added} -${diff.summary.removed} ~${diff.summary.modified}`,
    ...diff.changes.map(changeLine),
  ];
}

function human(report: CommandReport): string {
  switch (report.command) {
    case "validate":
      return [
        `${report.valid ? "OK" : "FAIL"} ${report.file}: ${report.metrics.errors} error(es), ${report.metrics.warnings} advertencia(s)`,
        `  ${report.metrics.nodeCount} nodos · ${report.metrics.edgeCount} conexiones · cobertura ${report.metrics.coveragePercent}%`,
        ...report.issues.map(issueLine),
      ].join("\n");
    case "diff":
      return diffLines(report).join("\n");
    case "simulate":
      if (!report.plan) {
        return [
          `FAIL ${report.file}: la definición no es simulable.`,
          ...report.issues.map(issueLine),
        ].join("\n");
      }
      return [
        `Simulación ${report.plan.summary.status}: ${report.file}`,
        `  run ${report.plan.runId} · ${report.plan.summary.durationMs} ms lógicos · ${report.plan.summary.eventCount} eventos`,
        `  ruta: ${report.plan.summary.visitedNodeIds.join(" → ") || "(vacía)"}`,
        ...report.issues.map(issueLine),
      ].join("\n");
    case "check": {
      const lines = [
        `${report.passed ? "PASS" : "FAIL"} PR Flight Check: ${report.file}`,
        `  validación: ${report.valid ? "correcta" : "con errores"}`,
      ];
      if (report.baseline && report.diff) {
        lines.push(`  baseline: ${report.baseline} · policy: ${report.policy.failOn} · impacto: ${report.diff.highestImpact}`);
      }
      lines.push(...report.issues.map(issueLine));
      if (report.diff) {
        lines.push(...report.diff.changes.map(changeLine));
      }
      if (!report.passed) lines.push(`  bloqueado por ${report.failures.length} hallazgo(s).`);
      return lines.join("\n");
    }
  }
}

export function formatReport(report: CommandReport, format: OutputFormat): string {
  if (format === "human") return `${human(report)}\n`;
  if (format === "sarif") return `${JSON.stringify(toSarif(report), null, 2)}\n`;
  return `${JSON.stringify(report, null, 2)}\n`;
}
