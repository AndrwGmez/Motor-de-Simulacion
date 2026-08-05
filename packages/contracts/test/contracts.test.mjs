import assert from "node:assert/strict";
import test from "node:test";
import { generateLargeFixture } from "../scripts/generate-large-fixture.mjs";
import { validateAllContracts } from "../scripts/validate-contracts.mjs";

test("todos los contratos y fixtures son coherentes", async () => {
  const summary = await validateAllContracts();
  assert.equal(summary.schemas, 8);
  assert.equal(summary.fixturesChecked, 2);
  assert(summary.openapiOperations >= 1);
  assert.equal(summary.asyncapiChannels, 1);
});

test("el fixture de carga es determinista y respeta el presupuesto", () => {
  const first = generateLargeFixture();
  const second = generateLargeFixture();
  assert.deepEqual(first, second);
  assert.equal(first.nodes.length, 500);
  assert.equal(first.edges.length, 1000);
  assert.equal(new Set(first.nodes.map((node) => node.id)).size, 500);
  assert.equal(new Set(first.edges.map((edge) => edge.id)).size, 1000);
});
