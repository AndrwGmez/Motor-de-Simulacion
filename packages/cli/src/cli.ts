#!/usr/bin/env node
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import type { SimulationOverrides } from "@flowverse/core";
import {
  checkDefinition,
  diffDefinitions,
  invalidCheckReport,
  invalidSimulationReport,
  invalidValidationReport,
  simulateDefinition,
  validateDefinition,
} from "./commands";
import { formatReport } from "./format";
import { CLIInputError, FlowSchemaError, loadFlow, parseInputArgument, writeOutput } from "./io";
import type { CheckThreshold, CommandReport, OutputFormat } from "./types";

const VERSION = "0.1.0";

const HELP = `FlowVerse CLI ${VERSION}

Uso:
  flowverse validate <flow.json> [--format human|json|sarif] [--output archivo]
  flowverse diff <baseline.json> <candidate.json> [--format human|json|sarif]
  flowverse simulate <flow.json> [--input JSON|@archivo] [--trigger id]
                     [--fail-node id] [--force-edge node=edge]
                     [--max-steps n] [--max-visits-per-node n]
  flowverse check <flow.json> [--baseline flow.json]
                  [--fail-on none|behavioral|breaking] [--format human|json|sarif]

Opciones globales:
  -f, --format <formato>  human (por defecto), json o sarif
      --json              Atajo para --format json
      --sarif             Atajo para --format sarif
  -o, --output <archivo>  Escribe el resultado en un archivo
  -h, --help              Muestra esta ayuda
  -V, --version           Muestra la versión

Política de check:
  Los errores de validación siempre bloquean. Con --baseline, behavioral
  bloquea cambios behavioral y breaking; breaking bloquea sólo breaking.
`;

type Writer = { write(value: string): unknown };

export interface CLIEnvironment {
  cwd?: string;
  stdout?: Writer;
  stderr?: Writer;
}

interface ParsedArguments {
  positionals: string[];
  flags: Map<string, string[]>;
}

const aliases: Record<string, string> = {
  "-f": "format",
  "-o": "output",
  "-h": "help",
  "-V": "version",
};

const booleanFlags = new Set([
  "help",
  "version",
  "json",
  "sarif",
  "fail-on-behavioral",
  "fail-on-breaking",
]);

const valueFlags = new Set([
  "format",
  "output",
  "input",
  "trigger",
  "fail-node",
  "force-edge",
  "max-steps",
  "max-visits-per-node",
  "baseline",
  "fail-on",
]);

const commonFlags = new Set(["format", "output", "json", "sarif", "help", "version"]);

function addFlag(flags: Map<string, string[]>, name: string, value = "true"): void {
  flags.set(name, [...(flags.get(name) ?? []), value]);
}

function parseArguments(args: string[]): ParsedArguments {
  const result: ParsedArguments = { positionals: [], flags: new Map() };
  let positionalOnly = false;
  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index]!;
    if (argument === "--") {
      positionalOnly = true;
      continue;
    }
    if (positionalOnly || !argument.startsWith("-")) {
      result.positionals.push(argument);
      continue;
    }
    const [rawName, inlineValue] = argument.startsWith("--")
      ? argument.slice(2).split(/=(.*)/s, 2)
      : [aliases[argument] ?? "", undefined];
    const name = aliases[argument] ?? rawName;
    if (!name || (!booleanFlags.has(name) && !valueFlags.has(name))) {
      throw new CLIInputError(`Opción desconocida: ${argument}`);
    }
    if (booleanFlags.has(name)) {
      if (inlineValue !== undefined) throw new CLIInputError(`--${name} no acepta un valor.`);
      addFlag(result.flags, name);
      continue;
    }
    const value = inlineValue ?? args[index + 1];
    if (value === undefined || (inlineValue === undefined && value.startsWith("-"))) {
      throw new CLIInputError(`--${name} requiere un valor.`);
    }
    if (inlineValue === undefined) index += 1;
    addFlag(result.flags, name, value);
  }
  return result;
}

function flag(parsed: ParsedArguments, name: string): string | undefined {
  return parsed.flags.get(name)?.at(-1);
}

function assertAllowedFlags(parsed: ParsedArguments, allowed: Set<string>): void {
  for (const name of parsed.flags.keys()) {
    if (!commonFlags.has(name) && !allowed.has(name)) {
      throw new CLIInputError(`--${name} no aplica a este comando.`);
    }
  }
}

function outputFormat(parsed: ParsedArguments): OutputFormat {
  const selections = [
    ...(flag(parsed, "format") ? [flag(parsed, "format")!] : []),
    ...(parsed.flags.has("json") ? ["json"] : []),
    ...(parsed.flags.has("sarif") ? ["sarif"] : []),
  ];
  if (selections.length > 1) throw new CLIInputError("Selecciona un solo formato de salida.");
  const format = selections[0] ?? "human";
  if (format !== "human" && format !== "json" && format !== "sarif") {
    throw new CLIInputError(`Formato desconocido: ${format}`);
  }
  return format;
}

function exactPositionals(parsed: ParsedArguments, count: number, usage: string): void {
  if (parsed.positionals.length !== count) throw new CLIInputError(`Uso: ${usage}`);
}

function simulationOverrides(parsed: ParsedArguments): Partial<SimulationOverrides> {
  const forcedEdgeIds: Record<string, string> = {};
  for (const assignment of parsed.flags.get("force-edge") ?? []) {
    const separator = assignment.indexOf("=");
    if (separator <= 0 || separator === assignment.length - 1) {
      throw new CLIInputError(`--force-edge requiere nodeId=edgeId; recibido: ${assignment}`);
    }
    forcedEdgeIds[assignment.slice(0, separator)] = assignment.slice(separator + 1);
  }
  return {
    failedNodeIds: parsed.flags.get("fail-node") ?? [],
    forcedEdgeIds,
  };
}

function positiveIntegerFlag(parsed: ParsedArguments, name: string): number | undefined {
  const raw = flag(parsed, name);
  if (raw === undefined) return undefined;
  if (!/^\d+$/.test(raw) || Number(raw) < 1 || !Number.isSafeInteger(Number(raw))) {
    throw new CLIInputError(`--${name} requiere un entero positivo.`);
  }
  return Number(raw);
}

function checkThreshold(parsed: ParsedArguments): CheckThreshold {
  const shortcuts: CheckThreshold[] = [
    ...(parsed.flags.has("fail-on-behavioral") ? ["behavioral" as const] : []),
    ...(parsed.flags.has("fail-on-breaking") ? ["breaking" as const] : []),
  ];
  const explicit = flag(parsed, "fail-on");
  if (explicit) shortcuts.push(explicit as CheckThreshold);
  if (shortcuts.length > 1) throw new CLIInputError("Selecciona una sola política --fail-on.");
  const threshold = shortcuts[0] ?? "none";
  if (threshold !== "none" && threshold !== "behavioral" && threshold !== "breaking") {
    throw new CLIInputError(`Política desconocida: ${threshold}`);
  }
  return threshold;
}

async function execute(command: string, parsed: ParsedArguments, cwd: string): Promise<{ report: CommandReport; exitCode: number }> {
  switch (command) {
    case "validate": {
      assertAllowedFlags(parsed, new Set());
      exactPositionals(parsed, 1, "flowverse validate <flow.json>");
      const file = parsed.positionals[0]!;
      let flow;
      try {
        flow = await loadFlow(file, cwd);
      } catch (error) {
        if (error instanceof FlowSchemaError) {
          return { report: invalidValidationReport(file, error.issues), exitCode: 1 };
        }
        throw error;
      }
      const report = validateDefinition(flow.path, flow.definition);
      return { report, exitCode: report.valid ? 0 : 1 };
    }
    case "diff": {
      assertAllowedFlags(parsed, new Set());
      exactPositionals(parsed, 2, "flowverse diff <baseline.json> <candidate.json>");
      const [baseline, candidate] = await Promise.all([
        loadFlow(parsed.positionals[0]!, cwd),
        loadFlow(parsed.positionals[1]!, cwd),
      ]);
      return {
        report: diffDefinitions(baseline.path, baseline.definition, candidate.path, candidate.definition),
        exitCode: 0,
      };
    }
    case "simulate": {
      assertAllowedFlags(parsed, new Set([
        "input",
        "trigger",
        "fail-node",
        "force-edge",
        "max-steps",
        "max-visits-per-node",
      ]));
      exactPositionals(parsed, 1, "flowverse simulate <flow.json>");
      const file = parsed.positionals[0]!;
      let flow;
      try {
        flow = await loadFlow(file, cwd);
      } catch (error) {
        if (error instanceof FlowSchemaError) {
          return { report: invalidSimulationReport(file, error.issues), exitCode: 1 };
        }
        throw error;
      }
      const input = await parseInputArgument(flag(parsed, "input"), cwd);
      const report = simulateDefinition(flow.path, flow.definition, {
        input,
        triggerId: flag(parsed, "trigger"),
        overrides: simulationOverrides(parsed),
        limits: {
          maxSteps: positiveIntegerFlag(parsed, "max-steps"),
          maxVisitsPerNode: positiveIntegerFlag(parsed, "max-visits-per-node"),
        },
      });
      return { report, exitCode: report.valid ? 0 : 1 };
    }
    case "check": {
      assertAllowedFlags(parsed, new Set(["baseline", "fail-on", "fail-on-behavioral", "fail-on-breaking"]));
      exactPositionals(parsed, 1, "flowverse check <flow.json> [--baseline flow.json]");
      const file = parsed.positionals[0]!;
      const baselinePath = flag(parsed, "baseline");
      const threshold = checkThreshold(parsed);
      if (threshold !== "none" && !baselinePath) {
        throw new CLIInputError("--fail-on requiere --baseline.");
      }
      let candidate;
      try {
        candidate = await loadFlow(file, cwd);
      } catch (error) {
        if (error instanceof FlowSchemaError) {
          return { report: invalidCheckReport(file, error.issues, threshold), exitCode: 1 };
        }
        throw error;
      }
      const baseline = baselinePath ? await loadFlow(baselinePath, cwd) : undefined;
      const report = checkDefinition(candidate.path, candidate.definition, {
        baseline: baseline?.definition,
        baselinePath: baseline?.path,
        failOn: threshold,
      });
      return { report, exitCode: report.passed ? 0 : 1 };
    }
    default:
      throw new CLIInputError(`Comando desconocido: ${command}`);
  }
}

export async function runCLI(args: string[], environment: CLIEnvironment = {}): Promise<number> {
  const cwd = environment.cwd ?? process.cwd();
  const stdout = environment.stdout ?? process.stdout;
  const stderr = environment.stderr ?? process.stderr;
  try {
    const command = args[0];
    const parsed = parseArguments(args.slice(1));
    if (!command || command === "help" || command === "--help" || command === "-h" || parsed.flags.has("help")) {
      stdout.write(HELP);
      return 0;
    }
    if (command === "--version" || command === "-V" || parsed.flags.has("version")) {
      stdout.write(`${VERSION}\n`);
      return 0;
    }
    const format = outputFormat(parsed);
    const { report, exitCode } = await execute(command, parsed, cwd);
    const rendered = formatReport(report, format);
    const output = flag(parsed, "output");
    if (output) {
      await writeOutput(output, rendered, cwd);
      stderr.write(`FlowVerse escribió ${output}\n`);
    } else {
      stdout.write(rendered);
    }
    return exitCode;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    stderr.write(`flowverse: ${message}\n`);
    return error instanceof CLIInputError ? 2 : 1;
  }
}

const invokedDirectly = process.argv[1]
  && fileURLToPath(import.meta.url) === resolve(process.argv[1]);

if (invokedDirectly) {
  void runCLI(process.argv.slice(2)).then((exitCode) => {
    process.exitCode = exitCode;
  });
}
