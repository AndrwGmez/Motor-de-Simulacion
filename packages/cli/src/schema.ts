import Ajv2020 from "ajv/dist/2020.js";
import type { ErrorObject } from "ajv";
import type { FlowDefinition, ValidationIssue } from "@flowverse/core";
import flowDefinitionSchema from "../../contracts/schemas/flow-definition.schema.json";

const ajv = new Ajv2020({ allErrors: true, strict: true });
const validate = ajv.compile<FlowDefinition>(flowDefinitionSchema);

function issue(error: ErrorObject, index: number): ValidationIssue {
  const path = error.instancePath || "/";
  return {
    id: `flow.schema.${error.keyword}-${index}`,
    code: `flow.schema.${error.keyword}`,
    severity: "error",
    message: `${path} ${error.message ?? "no cumple el contrato FlowDefinition"}`,
  };
}

export function validateFlowSchema(value: unknown): ValidationIssue[] {
  if (validate(value)) return [];
  return (validate.errors ?? []).map(issue);
}

export function isFlowDefinition(value: unknown): value is FlowDefinition {
  return validate(value);
}
