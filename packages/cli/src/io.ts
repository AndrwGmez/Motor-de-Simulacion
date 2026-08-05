import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import type { ValidationIssue } from "@flowverse/core";
import { isFlowDefinition, validateFlowSchema } from "./schema";
import type { LoadedFlow } from "./types";

export class CLIInputError extends Error {
  override readonly name: string = "CLIInputError";
}

export class FlowSchemaError extends CLIInputError {
  override readonly name: string = "FlowSchemaError";

  constructor(readonly file: string, readonly issues: ValidationIssue[]) {
    super(`${file} no cumple el contrato FlowDefinition (${issues.length} hallazgo(s)).`);
  }
}

async function parseJSONFile(path: string): Promise<unknown> {
  let source: string;
  try {
    source = await readFile(path, "utf8");
  } catch (error) {
    throw new CLIInputError(`No se pudo leer ${path}: ${error instanceof Error ? error.message : String(error)}`);
  }
  try {
    return JSON.parse(source) as unknown;
  } catch (error) {
    throw new CLIInputError(`JSON inválido en ${path}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

export async function loadFlow(file: string, cwd = process.cwd()): Promise<LoadedFlow> {
  const absolutePath = resolve(cwd, file);
  const value = await parseJSONFile(absolutePath);
  if (!isFlowDefinition(value)) {
    throw new FlowSchemaError(file, validateFlowSchema(value));
  }
  return { path: file, definition: value };
}

export async function parseInputArgument(value: string | undefined, cwd = process.cwd()): Promise<unknown> {
  if (value === undefined) return {};
  if (value.startsWith("@")) {
    const inputPath = value.slice(1);
    if (!inputPath) throw new CLIInputError("--input @archivo requiere una ruta.");
    return parseJSONFile(resolve(cwd, inputPath));
  }
  try {
    return JSON.parse(value) as unknown;
  } catch (error) {
    throw new CLIInputError(`--input debe ser JSON o @archivo: ${error instanceof Error ? error.message : String(error)}`);
  }
}

export async function writeOutput(file: string, content: string, cwd = process.cwd()): Promise<void> {
  const absolutePath = resolve(cwd, file);
  await mkdir(dirname(absolutePath), { recursive: true });
  await writeFile(absolutePath, content, "utf8");
}
