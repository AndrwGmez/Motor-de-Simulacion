import { describe, expect, it } from "vitest";
import { resolve } from "node:path";
import Ajv2020 from "ajv/dist/2020";
import addFormats from "ajv-formats";
import esquema from "../../contracts/schemas/flow-definition.schema.json";
import { analizar } from "./index";
import type { FlowDefinition, FlowNode } from "@flowverse/core";

const FIXTURE = resolve(process.cwd(), "../../packages/codegraph/fixtures/impuestos");

const validador = (() => {
  const ajv = new Ajv2020({ allErrors: true, strict: false });
  addFormats(ajv);
  return ajv.compile(esquema);
})();

const porTipo = (flujo: FlowDefinition, tipo: FlowNode["type"]) =>
  flujo.nodes.filter((nodo) => nodo.type === tipo);

const buscar = (flujo: FlowDefinition, fragmento: string) =>
  flujo.nodes.find((nodo) => nodo.label.includes(fragmento));

describe("extractor de módulos sobre un proyecto de impuestos", () => {
  it("crea un nodo por archivo del proyecto", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    const archivos = flujo.nodes.filter((nodo) => nodo.type !== "group" && nodo.metadata?.category === "archivo");
    expect(archivos).toHaveLength(5);
  });

  it("agrupa por directorio con nodos contenedor", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    const grupos = porTipo(flujo, "group").map((nodo) => nodo.label).sort();
    expect(grupos).toEqual(["api", "datos", "externos", "servicios", "util"]);
  });

  it("reconoce el acceso a base de datos como nodo de datos", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    expect(buscar(flujo, "repositorio")?.type).toBe("data");
  });

  it("reconoce la llamada a un servicio externo como integración", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    expect(buscar(flujo, "padron")?.type).toBe("integration");
  });

  it("convierte cada importación interna en una conexión", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    const etiquetas = flujo.edges.map((e) => `${e.source}->${e.target}`);
    // liquidaciones importa calculo y formato; calculo importa repositorio y padron.
    expect(etiquetas.filter((e) => e.includes("calculo")).length).toBeGreaterThanOrEqual(3);
  });

  it("ignora las dependencias externas: pg y axios no son nodos", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    expect(buscar(flujo, "axios")).toBeUndefined();
    expect(buscar(flujo, "pg")).toBeUndefined();
  });
});

describe("el flujo derivado cumple el contrato", () => {
  it("valida contra el JSON Schema compartido", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    const valido = validador(flujo);
    expect(validador.errors ?? [], JSON.stringify(validador.errors?.slice(0, 3))).toHaveLength(0);
    expect(valido).toBe(true);
  });

  it("tiene inicio y final, que es lo que exige el validador de Go", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    expect(porTipo(flujo, "trigger").length).toBeGreaterThanOrEqual(1);
    expect(porTipo(flujo, "end").length).toBeGreaterThanOrEqual(1);
  });

  it("no conecta nodos de grupo, que el contrato prohíbe", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    const grupos = new Set(porTipo(flujo, "group").map((nodo) => nodo.id));
    for (const arista of flujo.edges) {
      expect(grupos.has(arista.source), `conecta el grupo ${arista.source}`).toBe(false);
      expect(grupos.has(arista.target), `conecta el grupo ${arista.target}`).toBe(false);
    }
  });

  it("usa puertos que existen en los nodos que conecta", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    const porId = new Map(flujo.nodes.map((nodo) => [nodo.id, nodo]));
    for (const arista of flujo.edges) {
      const origen = porId.get(arista.source)!;
      const destino = porId.get(arista.target)!;
      expect(origen.outputs.some((p) => p.id === arista.sourcePort)).toBe(true);
      expect(destino.inputs.some((p) => p.id === arista.targetPort)).toBe(true);
    }
  });

  it("genera identificadores canónicos aunque las rutas sean largas", async () => {
    const flujo = await analizar(FIXTURE, { modo: "modules" });
    const canonico = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/;
    for (const nodo of flujo.nodes) expect(nodo.id, nodo.id).toMatch(canonico);
    for (const arista of flujo.edges) expect(arista.id, arista.id).toMatch(canonico);
  });
});

describe("límites del contrato", () => {
  it("falla con un mensaje claro en vez de emitir un flujo que la API rechazará", async () => {
    await expect(
      analizar(FIXTURE, { modo: "modules", limiteNodos: 3 }),
    ).rejects.toThrow(/supera el límite/i);
  });
});
