import { describe, expect, it } from "vitest";
import { resolve } from "node:path";
import { analizar } from "./index";
import type { FlowDefinition } from "@flowverse/core";

const FIXTURE = resolve(process.cwd(), "fixtures/impuestos");
const etiquetas = (f: FlowDefinition) => f.nodes.map((n) => n.label);

describe("extractor de funciones", () => {
  it("crea un nodo por función declarada", async () => {
    const flujo = await analizar(FIXTURE, { modo: "functions" });
    expect(etiquetas(flujo)).toEqual(expect.arrayContaining([
      "postLiquidaciones", "calcularLiquidacion", "guardarLiquidacion", "consultarPadron", "formatearImporte",
    ]));
  });

  it("convierte cada llamada entre funciones propias en una conexión", async () => {
    const flujo = await analizar(FIXTURE, { modo: "functions" });
    const porId = new Map(flujo.nodes.map((n) => [n.id, n.label]));
    const pares = flujo.edges.map((e) => `${porId.get(e.source)}->${porId.get(e.target)}`);
    expect(pares).toContain("postLiquidaciones->calcularLiquidacion");
    expect(pares).toContain("calcularLiquidacion->guardarLiquidacion");
  });

  it("marca como integración la función que llama a un servicio externo", async () => {
    const flujo = await analizar(FIXTURE, { modo: "functions" });
    expect(flujo.nodes.find((n) => n.label === "consultarPadron")?.type).toBe("integration");
  });

  it("marca como datos la que accede a la base", async () => {
    const flujo = await analizar(FIXTURE, { modo: "functions" });
    expect(flujo.nodes.find((n) => n.label === "guardarLiquidacion")?.type).toBe("data");
  });

  it("acota el alcance con --incluir", async () => {
    const completo = await analizar(FIXTURE, { modo: "functions" });
    const acotado = await analizar(FIXTURE, { modo: "functions", incluir: "servicios" });
    expect(acotado.nodes.length).toBeLessThan(completo.nodes.length);
  });

  it("descarta lo que indique --excluir", async () => {
    const flujo = await analizar(FIXTURE, { modo: "functions", excluir: "externos" });
    expect(etiquetas(flujo)).not.toContain("consultarPadron");
  });

  it("respeta los límites del contrato con un mensaje claro", async () => {
    await expect(analizar(FIXTURE, { modo: "functions", limiteNodos: 2 }))
      .rejects.toThrow(/supera el límite/i);
  });
});
