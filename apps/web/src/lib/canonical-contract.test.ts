import Ajv2020 from "ajv/dist/2020";
import addFormats from "ajv-formats";
import { describe, expect, it } from "vitest";
import schema from "../../../../packages/contracts/schemas/flow-definition.schema.json";
import { DEMO_FLOW } from "./demo-flow";
import { parseImportedFlow } from "./flow-service";

describe("contrato FlowDefinition", () => {
  it("mantiene el documento demo compatible con el JSON Schema canónico", () => {
    const ajv = new Ajv2020({ strict: false, allErrors: true });
    addFormats(ajv);
    const validate = ajv.compile(schema);
    expect(validate(DEMO_FLOW), JSON.stringify(validate.errors, null, 2)).toBe(true);
  });

  it("importa únicamente la definición canónica", () => {
    const imported = parseImportedFlow({ flow: DEMO_FLOW });
    expect(Object.keys(imported).sort()).toEqual(
      ["description", "edges", "layout", "metadata", "name", "nodes", "schemaVersion", "variables"].sort(),
    );
    expect(imported).toEqual(DEMO_FLOW);
  });

  it("rechaza propiedades adicionales aunque existan nodos y conexiones", () => {
    expect(() => parseImportedFlow({
      ...structuredClone(DEMO_FLOW),
      propiedadNoCanonica: true,
    })).toThrow(/additional properties|propiedades adicionales|must NOT have/i);
  });

  it("rechaza configuraciones incompatibles con el tipo de nodo", () => {
    const invalid = structuredClone(DEMO_FLOW);
    invalid.nodes[0].configuration = { result: "success" };
    expect(() => parseImportedFlow(invalid)).toThrow(/configuration|eventName|additional properties|must NOT have/i);
  });
});
