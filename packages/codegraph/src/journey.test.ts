import { describe, expect, it } from "vitest";
import { resolve } from "node:path";
import { analizar } from "./index";
import type { FlowDefinition, FlowNode } from "@flowverse/core";

const FIXTURE = resolve(process.cwd(), "fixtures/recorrido");
const tipos = (f: FlowDefinition, t: FlowNode["type"]) => f.nodes.filter((n) => n.type === t);
const conTexto = (f: FlowDefinition, s: string) => f.nodes.filter((n) => n.label.toLowerCase().includes(s));

describe("extractor de recorrido de negocio", () => {
  it("convierte el punto de entrada en un nodo de inicio", async () => {
    const flujo = await analizar(FIXTURE, { modo: "journey" });
    const inicios = tipos(flujo, "trigger");
    expect(inicios).toHaveLength(1);
    expect(inicios[0].label.toLowerCase()).toContain("liquidaciones");
  });

  it("convierte cada condicional en una decisión", async () => {
    const flujo = await analizar(FIXTURE, { modo: "journey" });
    expect(tipos(flujo, "decision").length).toBeGreaterThanOrEqual(2);
  });

  it("da a cada decisión exactamente un camino por defecto", async () => {
    const flujo = await analizar(FIXTURE, { modo: "journey" });
    for (const decision of tipos(flujo, "decision")) {
      const salidas = flujo.edges.filter((e) => e.source === decision.id);
      expect(salidas.filter((e) => e.isDefault), decision.id).toHaveLength(1);
    }
  });

  it("reconoce la llamada al servicio externo como integración", async () => {
    const flujo = await analizar(FIXTURE, { modo: "journey" });
    expect(tipos(flujo, "integration").length).toBeGreaterThanOrEqual(1);
  });

  it("reconoce el acceso a la base de datos como nodo de datos", async () => {
    const flujo = await analizar(FIXTURE, { modo: "journey" });
    expect(tipos(flujo, "data").length).toBeGreaterThanOrEqual(1);
  });

  it("distingue el retorno correcto del lanzamiento de error", async () => {
    const flujo = await analizar(FIXTURE, { modo: "journey" });
    const finales = tipos(flujo, "end");
    expect(finales.some((n) => n.configuration.result === "success")).toBe(true);
    expect(finales.some((n) => n.configuration.result === "failure")).toBe(true);
  });

  it("representa el bucle como una espera, no como un ciclo sin salida", async () => {
    const flujo = await analizar(FIXTURE, { modo: "journey" });
    expect(conTexto(flujo, "cada").length + tipos(flujo, "delay").length).toBeGreaterThanOrEqual(1);
  });
});

it("da a cada decisión la estrategia que el contrato exige", async () => {
  const flujo = await analizar(FIXTURE, { modo: "journey" });
  for (const decision of tipos(flujo, "decision")) {
    expect(decision.configuration.strategy, decision.id).toBe("first_match");
  }
});
