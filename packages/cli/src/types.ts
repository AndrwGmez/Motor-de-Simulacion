import type {
  FlowDefinition,
  FlowSemanticDiff,
  SemanticChange,
  SemanticImpact,
  SimulationOverrides,
  SimulationPlan,
  ValidationIssue,
} from "@flowverse/core";
import type { SimulationLimits } from "@flowverse/engine";

export type OutputFormat = "human" | "json" | "sarif";
export type CheckThreshold = "none" | "behavioral" | "breaking";

export interface ValidationReport {
  command: "validate";
  file: string;
  valid: boolean;
  issues: ValidationIssue[];
  metrics: {
    nodeCount: number;
    edgeCount: number;
    reachableCount: number;
    coveragePercent: number;
    complexity: number;
    cycleCount: number;
    errors: number;
    warnings: number;
  };
}

export interface DiffReport {
  command: "diff";
  baseline: string;
  candidate: string;
  diff: FlowSemanticDiff;
}

export interface SimulationReport {
  command: "simulate";
  file: string;
  valid: boolean;
  issues: ValidationIssue[];
  input: unknown;
  overrides: Partial<SimulationOverrides>;
  triggerId?: string;
  plan?: SimulationPlan;
}

export interface CheckFailure {
  kind: "validation" | "semantic-change";
  message: string;
  issue?: ValidationIssue;
  change?: SemanticChange;
}

export interface CheckReport {
  command: "check";
  file: string;
  baseline?: string;
  valid: boolean;
  issues: ValidationIssue[];
  diff?: FlowSemanticDiff;
  policy: {
    failOn: CheckThreshold;
  };
  failures: CheckFailure[];
  passed: boolean;
}

export type CommandReport = ValidationReport | DiffReport | SimulationReport | CheckReport;

export interface LoadedFlow {
  path: string;
  definition: FlowDefinition;
}

export interface SimulationOptions {
  input?: unknown;
  triggerId?: string;
  overrides?: Partial<SimulationOverrides>;
  limits?: SimulationLimits;
}

export interface CheckOptions {
  baseline?: FlowDefinition;
  baselinePath?: string;
  failOn?: CheckThreshold;
}

export interface SarifResult {
  ruleId: string;
  level: "error" | "warning" | "note";
  message: { text: string };
  locations?: Array<{
    physicalLocation: {
      artifactLocation: { uri: string };
    };
  }>;
  properties?: Record<string, unknown>;
}

export interface SarifLog {
  $schema: string;
  version: "2.1.0";
  runs: Array<{
    tool: {
      driver: {
        name: string;
        version: string;
        informationUri: string;
        rules: Array<{
          id: string;
          shortDescription: { text: string };
          defaultConfiguration: { level: SarifResult["level"] };
        }>;
      };
    };
    results: SarifResult[];
    properties?: Record<string, unknown>;
  }>;
}

export const IMPACT_RANK: Record<SemanticImpact | "none", number> = {
  none: 0,
  visual: 1,
  behavioral: 2,
  breaking: 3,
};
