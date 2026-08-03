import { describe, expect, it } from "vitest";
import { parseFlowCsv } from "./csv";

const CABECERA = "id,tipo,etiqueta,conecta_con";

describe("importación CSV", () => {
  it("construye un flujo con inicio, proceso y final", () => {
    const flujo = parseFlowCsv([CABECERA,
      "inicio,trigger,Pedido recibido,validar",
      "validar,process,Validar pago,fin",
      "fin,end,Entregado,",
    ].join("\n"));
    expect(flujo.nodes.map((n) => n.id)).toEqual(["inicio", "validar", "fin"]);
    expect(flujo.edges).toHaveLength(2);
  });

  it("admite varios destinos separados por punto y coma", () => {
    const flujo = parseFlowCsv([CABECERA,
      "decidir,decision,¿Aprobado?,si;no",
      "si,end,Aprobado,",
      "no,end,Rechazado,",
    ].join("\n"));
    expect(flujo.edges.filter((e) => e.source === "decidir")).toHaveLength(2);
  });

  it("da a la decisión su estrategia y un solo camino por defecto", () => {
    const flujo = parseFlowCsv([CABECERA,
      "d,decision,¿Sí?,a;b", "a,end,A,", "b,end,B,",
    ].join("\n"));
    const decision = flujo.nodes.find((n) => n.id === "d")!;
    expect(decision.configuration.strategy).toBe("first_match");
    expect(flujo.edges.filter((e) => e.source === "d" && e.isDefault)).toHaveLength(1);
  });

  it("rechaza una columna obligatoria ausente en vez de adivinar", () => {
    expect(() => parseFlowCsv("id,etiqueta\na,A")).toThrow(/tipo/i);
  });

  it("rechaza un tipo de nodo que no existe en el contrato", () => {
    expect(() => parseFlowCsv([CABECERA, "a,inventado,A,"].join("\n"))).toThrow(/inventado/i);
  });

  it("rechaza una conexión hacia un identificador inexistente", () => {
    expect(() => parseFlowCsv([CABECERA, "a,trigger,A,fantasma"].join("\n"))).toThrow(/fantasma/i);
  });

  it("tolera espacios, comillas y líneas en blanco", () => {
    const flujo = parseFlowCsv([CABECERA,
      '  inicio , trigger , "Pedido, urgente" , fin ',
      "",
      "fin,end,Entregado,",
    ].join("\n"));
    expect(flujo.nodes[0].label).toBe("Pedido, urgente");
    expect(flujo.nodes).toHaveLength(2);
  });
});
