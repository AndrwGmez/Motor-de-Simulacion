import assert from "node:assert/strict";
import { access, readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { parseDocument } from "yaml";
import { generateLargeFixture } from "./generate-large-fixture.mjs";

const packageDirectory = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  ".."
);

async function readJson(relativePath) {
  const absolutePath = path.join(packageDirectory, relativePath);
  return JSON.parse(await readFile(absolutePath, "utf8"));
}

async function parseYaml(relativePath) {
  const absolutePath = path.join(packageDirectory, relativePath);
  const source = await readFile(absolutePath, "utf8");
  const document = parseDocument(source, { uniqueKeys: true });
  assert.equal(
    document.errors.length,
    0,
    `${relativePath}: ${document.errors.map((error) => error.message).join("; ")}`
  );
  return document.toJS();
}

function collectReferences(value, references = []) {
  if (Array.isArray(value)) {
    value.forEach((entry) => collectReferences(entry, references));
    return references;
  }

  if (value && typeof value === "object") {
    for (const [key, entry] of Object.entries(value)) {
      if (key === "$ref" && typeof entry === "string") {
        references.push(entry);
      } else {
        collectReferences(entry, references);
      }
    }
  }
  return references;
}

async function assertLocalReferencesExist(document, sourceFile) {
  const directory = path.dirname(path.join(packageDirectory, sourceFile));
  for (const reference of collectReferences(document)) {
    if (
      reference.startsWith("#") ||
      reference.startsWith("https://") ||
      reference.startsWith("http://")
    ) {
      continue;
    }
    const filePart = reference.split("#", 1)[0];
    await access(path.resolve(directory, filePart));
  }
}

function resolveJsonPointer(document, reference) {
  const tokens = reference
    .slice(2)
    .split("/")
    .map((token) => token.replaceAll("~1", "/").replaceAll("~0", "~"));
  return tokens.reduce(
    (value, token) =>
      value && typeof value === "object" ? value[token] : undefined,
    document
  );
}

function assertInternalReferencesExist(document, sourceFile) {
  for (const reference of collectReferences(document)) {
    if (!reference.startsWith("#/")) {
      continue;
    }
    assert.notEqual(
      resolveJsonPointer(document, reference),
      undefined,
      `${sourceFile}: referencia interna inexistente ${reference}`
    );
  }
}

function assertUniqueOperationIds(openapi) {
  const methods = new Set([
    "get",
    "post",
    "put",
    "patch",
    "delete",
    "options",
    "head"
  ]);
  const operationIds = new Set();

  for (const pathItem of Object.values(openapi.paths ?? {})) {
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!methods.has(method) || !operation.operationId) {
        continue;
      }
      assert(
        !operationIds.has(operation.operationId),
        `operationId duplicado: ${operation.operationId}`
      );
      operationIds.add(operation.operationId);
    }
  }
  assert(operationIds.size > 0, "OpenAPI no contiene operaciones");
}

async function validateFixtureDirectory(directory, validate, expectedValidity) {
  const absoluteDirectory = path.join(packageDirectory, directory);
  const filenames = (await readdir(absoluteDirectory))
    .filter((filename) => filename.endsWith(".flow.json"))
    .sort();

  assert(filenames.length > 0, `${directory} no contiene fixtures`);
  for (const filename of filenames) {
    const fixture = await readJson(path.join(directory, filename));
    const valid = validate(fixture);
    assert.equal(
      valid,
      expectedValidity,
      `${filename}: ${JSON.stringify(validate.errors, null, 2)}`
    );
  }
}

export async function validateAllContracts() {
  const flowSchema = await readJson("schemas/flow-definition.schema.json");
  const simulationSchema = await readJson(
    "schemas/simulation-request.schema.json"
  );
  const eventSchema = await readJson("schemas/run-event.schema.json");
  const proposalSchema = await readJson("schemas/parse-proposal.schema.json");

  const ajv = new Ajv2020({
    allErrors: true,
    strict: true,
    validateFormats: true
  });
  addFormats(ajv);
  ajv.addSchema(flowSchema);
  ajv.addSchema(simulationSchema);
  ajv.addSchema(eventSchema);
  ajv.addSchema(proposalSchema);

  const validateFlow = ajv.getSchema(flowSchema.$id);
  assert(validateFlow, "No se compiló FlowDefinition");
  assert(ajv.getSchema(simulationSchema.$id), "No se compiló SimulationRequest");
  assert(ajv.getSchema(eventSchema.$id), "No se compiló RunEvent");
  assert(ajv.getSchema(proposalSchema.$id), "No se compiló ParseProposal");

  await validateFixtureDirectory("fixtures/valid", validateFlow, true);
  await validateFixtureDirectory("fixtures/invalid", validateFlow, false);

  const largeFixture = generateLargeFixture();
  assert.equal(largeFixture.nodes.length, 500);
  assert.equal(largeFixture.edges.length, 1000);
  assert(
    validateFlow(largeFixture),
    `Fixture 500/1000 inválido: ${JSON.stringify(validateFlow.errors, null, 2)}`
  );

  const openapi = await parseYaml("openapi.yaml");
  assert.match(openapi.openapi, /^3\.1\./);
  assert(openapi.paths && Object.keys(openapi.paths).length > 0);
  assertUniqueOperationIds(openapi);
  assertInternalReferencesExist(openapi, "openapi.yaml");
  await assertLocalReferencesExist(openapi, "openapi.yaml");

  const asyncapi = await parseYaml("asyncapi.yaml");
  assert.match(asyncapi.asyncapi, /^3\.0\./);
  assert(asyncapi.channels && Object.keys(asyncapi.channels).length > 0);
  assertInternalReferencesExist(asyncapi, "asyncapi.yaml");
  await assertLocalReferencesExist(asyncapi, "asyncapi.yaml");

  return {
    schemas: 4,
    fixturesChecked: 2,
    generatedNodes: largeFixture.nodes.length,
    generatedEdges: largeFixture.edges.length,
    openapiOperations: Object.values(openapi.paths).reduce(
      (count, pathItem) =>
        count +
        Object.values(pathItem).filter(
          (operation) =>
            operation &&
            typeof operation === "object" &&
            typeof operation.operationId === "string"
        ).length,
      0
    ),
    asyncapiChannels: Object.keys(asyncapi.channels).length
  };
}

const invokedDirectly =
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href;

if (invokedDirectly) {
  try {
    const summary = await validateAllContracts();
    process.stdout.write(
      `Contratos válidos: ${JSON.stringify(summary, null, 2)}\n`
    );
  } catch (error) {
    process.stderr.write(`${error.stack ?? error}\n`);
    process.exitCode = 1;
  }
}
