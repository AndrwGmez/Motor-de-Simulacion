import { describe, expect, it } from "vitest";
import { encuadrar, formaDeNodo, rutaDeArista } from "./plane";

const nodos = [
  { id: "a", position: { x: -100, y: -50, z: 0 } },
  { id: "b", position: { x: 300, y: 150, z: 0 } },
];

describe("encuadrar", () => {
  it("mete todos los nodos dentro del lienzo con margen", () => {
    const { posiciones, viewBox } = encuadrar(nodos, { width: 800, height: 600 }, 40);
    for (const { x, y } of posiciones.values()) {
      expect(x).toBeGreaterThanOrEqual(40);
      expect(y).toBeGreaterThanOrEqual(40);
      expect(x).toBeLessThanOrEqual(760);
      expect(y).toBeLessThanOrEqual(560);
    }
    expect(viewBox).toBe("0 0 800 600");
  });

  it("conserva las proporciones: no deforma el grafo", () => {
    const { posiciones } = encuadrar(nodos, { width: 800, height: 600 }, 40);
    const a = posiciones.get("a")!;
    const b = posiciones.get("b")!;
    const relacionOriginal = (300 - -100) / (150 - -50);
    const relacionProyectada = (b.x - a.x) / (b.y - a.y);
    expect(relacionProyectada).toBeCloseTo(relacionOriginal, 5);
  });

  it("centra un nodo único en lugar de dividir por cero", () => {
    const { posiciones } = encuadrar([{ id: "solo", position: { x: 5, y: 5, z: 0 } }], { width: 400, height: 300 }, 20);
    expect(posiciones.get("solo")).toEqual({ x: 200, y: 150 });
  });

  it("no falla con la lista vacía", () => {
    expect(() => encuadrar([], { width: 400, height: 300 }, 20)).not.toThrow();
  });
});

describe("formaDeNodo", () => {
  it("da una forma distinta a cada tipo, como en 3D", () => {
    const formas = new Set(["trigger", "process", "decision", "data", "integration", "delay", "end", "group"]
      .map((t) => formaDeNodo(t as never).tipo));
    expect(formas.size).toBeGreaterThanOrEqual(4);
  });

  it("da al inicio un círculo y al proceso un rectángulo", () => {
    expect(formaDeNodo("trigger").tipo).toBe("circulo");
    expect(formaDeNodo("process").tipo).toBe("rectangulo");
    expect(formaDeNodo("decision").tipo).toBe("rombo");
  });
});

describe("rutaDeArista", () => {
  it("traza una curva entre los dos puntos", () => {
    const d = rutaDeArista({ x: 0, y: 0 }, { x: 100, y: 50 });
    expect(d).toMatch(/^M 0 0/);
    expect(d).toContain("100 50");
  });

  it("separa las aristas paralelas para que no se solapen", () => {
    const recta = rutaDeArista({ x: 0, y: 0 }, { x: 100, y: 0 }, 0);
    const curva = rutaDeArista({ x: 0, y: 0 }, { x: 100, y: 0 }, 1);
    expect(curva).not.toBe(recta);
  });
});
