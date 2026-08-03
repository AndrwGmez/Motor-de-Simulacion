import { describe, expect, it } from "vitest";
import { resolve } from "node:path";
import { analizar } from "./index";
import { hayGoDisponible } from "./adapters/go";
import type { FlowDefinition, FlowNode } from "@flowverse/core";

const FIXTURE = resolve(process.cwd(), "fixtures/impuestos-go");
const tipos = (f: FlowDefinition, t: FlowNode["type"]) => f.nodes.filter((n) => n.type === t);
const etiquetas = (f: FlowDefinition) => f.nodes.map((n) => n.label);

// Estas son deliberadamente las MISMAS aserciones que se hacen sobre
// TypeScript. Si para que pasen hubiera que tocar los extractores, el modelo
// intermedio estaría mal diseñado y el adaptador no serviría de nada.
describe.skipIf(!hayGoDisponible())("adaptador de Go", () => {
  it("crea un nodo por archivo del proyecto", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules", lenguaje: "go" });
    const archivos = flujo.nodes.filter((n) => n.metadata?.category === "archivo");
    expect(archivos).toHaveLength(3);
  });

  it("reconoce el acceso a base de datos como nodo de datos", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules", lenguaje: "go" });
    expect(flujo.nodes.find((n) => n.label.includes("repositorio"))?.type).toBe("data");
  });

  it("agrupa por directorio igual que en TypeScript", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules", lenguaje: "go" });
    expect(tipos(flujo, "group").map((n) => n.label).sort()).toEqual(["datos", "servicios"]);
  });

  it("extrae las funciones declaradas", async () => {
    const flujo = await analizar(FIXTURE, { modo: "functions", lenguaje: "go" });
    expect(etiquetas(flujo)).toEqual(expect.arrayContaining(["calcularLiquidacion", "guardarLiquidacion"]));
  });

  it("produce un flujo con inicio y final, como exige el contrato", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules", lenguaje: "go" });
    expect(tipos(flujo, "trigger").length).toBeGreaterThanOrEqual(1);
    expect(tipos(flujo, "end").length).toBeGreaterThanOrEqual(1);
  });

  it("genera identificadores canónicos", async () => {
    const flujo = await analizar(FIXTURE, { modo: "functions", lenguaje: "go" });
    const canonico = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/;
    for (const nodo of flujo.nodes) expect(nodo.id, nodo.id).toMatch(canonico);
  });
});
