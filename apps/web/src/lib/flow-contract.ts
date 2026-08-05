import Ajv2020 from "ajv/dist/2020";
import addFormats from "ajv-formats";
import type { FlowDefinition } from "@flowverse/core";
import flowDefinitionSchema from "../../../../packages/contracts/schemas/flow-definition.schema.json";

const flowSchemaValidator = new Ajv2020({ allErrors: true, strict: false });
addFormats(flowSchemaValidator);
const validateFlowDefinitionSchema = flowSchemaValidator.compile(flowDefinitionSchema);

export function isFlowDefinition(value: unknown): value is FlowDefinition {
  return validateFlowDefinitionSchema(value);
}

/**
 * Extrae y valida una definición canónica desde los envelopes que usan la API,
 * los imports y las versiones publicadas.
 */
export function parseImportedFlow(value: unknown): FlowDefinition {
  let candidate = value;
  for (let depth = 0; depth < 3 && candidate && typeof candidate === "object" && !isFlowDefinition(candidate); depth += 1) {
    const envelope = candidate as Record<string, unknown>;
    candidate = envelope.definition ?? envelope.proposal ?? envelope.flow ?? envelope.data ?? candidate;
  }
  if (!isFlowDefinition(candidate)) {
    const details = (validateFlowDefinitionSchema.errors ?? [])
      .slice(0, 4)
      .map((item) => `${item.instancePath || "/"} ${item.message ?? "es inválido"}`)
      .join("; ");
    throw new Error(`El archivo no cumple el contrato FlowDefinition 1.0${details ? `: ${details}` : "."}`);
  }
  const definition = structuredClone(candidate);
  // `metadata` es opcional en el contrato, pero el editor lo consulta en los
  // nodos para decidir color, categoría y agrupación.
  for (const node of definition.nodes) {
    if (!node.metadata) node.metadata = {};
  }
  return definition;
}
