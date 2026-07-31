import { beforeEach, describe, expect, it } from "vitest";
import * as THREE from "three";
import { SceneResources } from "./scene-resources";
import type { FlowNode, NodeType } from "@flowverse/core";

// jsdom no implementa el contexto 2D, así que la fábrica de lienzos se inyecta.
// Además deja contar cuántos lienzos se crean, que es justo lo que medimos.
let canvasesCreated = 0;

function fakeCanvas() {
  canvasesCreated += 1;
  const canvas = {
    width: 0,
    height: 0,
    getContext: () => ({
      clearRect() {},
      measureText: (text: string) => ({ width: text.length * 17 }),
      beginPath() {},
      roundRect() {},
      fill() {},
      stroke() {},
      fillText() {},
      set font(_value: string) {},
      set textAlign(_value: string) {},
      set textBaseline(_value: string) {},
      set fillStyle(_value: string) {},
      set strokeStyle(_value: string) {},
      set lineWidth(_value: number) {},
    }),
  };
  return canvas as unknown as HTMLCanvasElement;
}

function node(id: string, type: NodeType, label = "Etiqueta", color?: string): FlowNode {
  return {
    id,
    type,
    label,
    description: "",
    inputs: [],
    outputs: [],
    activationMode: "each",
    durationMs: 0,
    configuration: {},
    position: { x: 0, y: 0, z: 0 },
    locked: false,
    metadata: color ? { color } : {},
  } as FlowNode;
}

let resources: SceneResources;

beforeEach(() => {
  canvasesCreated = 0;
  resources = new SceneResources(fakeCanvas);
});

describe("SceneResources", () => {
  it("comparte la geometría entre nodos del mismo tipo", () => {
    const primero = resources.geometryFor("process");
    const segundo = resources.geometryFor("process");
    expect(segundo).toBe(primero);
  });

  it("da una geometría distinta a cada tipo de nodo", () => {
    const tipos: NodeType[] = ["trigger", "process", "decision", "data", "integration", "delay", "end", "group"];
    const geometrias = new Set(tipos.map((tipo) => resources.geometryFor(tipo)));
    expect(geometrias.size).toBe(tipos.length);
  });

  it("comparte el material entre nodos con el mismo aspecto", () => {
    const a = resources.materialFor({ color: "#4D8DFF", status: "idle", selected: false, container: false });
    const b = resources.materialFor({ color: "#4D8DFF", status: "idle", selected: false, container: false });
    expect(b).toBe(a);
  });

  it("distingue el material cuando cambia el estado de ejecución", () => {
    const inactivo = resources.materialFor({ color: "#4D8DFF", status: "idle", selected: false, container: false });
    const corriendo = resources.materialFor({ color: "#4D8DFF", status: "running", selected: false, container: false });
    expect(corriendo).not.toBe(inactivo);
  });

  it("no vuelve a dibujar una etiqueta que ya existe", () => {
    resources.labelMaterialFor("Validar pago", false);
    const trasLaPrimera = canvasesCreated;
    resources.labelMaterialFor("Validar pago", false);
    expect(canvasesCreated).toBe(trasLaPrimera);
  });

  it("ajusta el lienzo de la etiqueta al ancho real del texto", () => {
    const corta = resources.labelMetricsFor("Fin", false);
    const larga = resources.labelMetricsFor("Consolidar la expedición de última milla", false);
    expect(corta.canvasWidth).toBeLessThan(larga.canvasWidth);
    // La densidad de téxeles no cambia: el sprite se escala en la misma
    // proporción en que se recorta el lienzo, así que se ve igual.
    expect(corta.canvasWidth / corta.spriteWidth).toBeCloseTo(larga.canvasWidth / larga.spriteWidth, 5);
  });

  it("mantiene los recursos acotados en un grafo de 482 nodos", () => {
    const tipos: NodeType[] = ["trigger", "process", "decision", "data", "integration", "delay", "end", "group"];
    const colores = ["#4D8DFF", "#F49A59", "#36C6D8", "#B078FF"];
    for (let index = 0; index < 482; index += 1) {
      const item = node(`n${index}`, tipos[index % tipos.length], `Nodo ${index}`, colores[index % colores.length]);
      resources.nodeObject(item, "idle", false);
    }
    // Un recurso por tipo y por aspecto, no uno por nodo.
    expect(resources.stats.geometries).toBeLessThanOrEqual(tipos.length + 2);
    expect(resources.stats.materials).toBeLessThanOrEqual(tipos.length * colores.length);
    // Un lienzo por etiqueta distinta, más el lienzo reutilizable de medición.
    expect(canvasesCreated).toBeLessThanOrEqual(483);
  });

  it("devuelve el mismo objeto para un nodo que no ha cambiado", () => {
    const item = node("n1", "process");
    const primero = resources.nodeObject(item, "idle", false);
    const segundo = resources.nodeObject(item, "idle", false);
    expect(segundo).toBe(primero);
  });

  it("libera geometrías, materiales y texturas al desecharse", () => {
    resources.nodeObject(node("n1", "process"), "idle", false);
    resources.dispose();
    expect(resources.stats).toEqual({ geometries: 0, materials: 0, labels: 0, objects: 0 });
  });
});

// Aspecto de metal y cristal: cada tipo de nodo recibe un material físico
// propio en lugar del plástico uniforme que tenían todos.
describe("materiales por tipo de nodo", () => {
  it("da a la integración un metal reflectante", () => {
    const material = resources.materialFor({ color: "#F49A59", status: "idle", selected: false, container: false, type: "integration" }) as THREE.MeshPhysicalMaterial;
    expect(material.metalness).toBeGreaterThan(0.7);
    expect(material.roughness).toBeLessThan(0.3);
  });

  it("da a los datos un cristal con cuerpo, no un vidrio invisible", () => {
    const material = resources.materialFor({ color: "#36C6D8", status: "idle", selected: false, container: false, type: "data" }) as THREE.MeshPhysicalMaterial;
    expect(material.transmission).toBeGreaterThan(0);
    // Por encima de este valor el nodo se desvanece contra el fondo y deja de
    // leerse su color, que es lo que distingue un tipo de otro.
    expect(material.transmission).toBeLessThanOrEqual(0.3);
    expect(material.metalness).toBeLessThan(0.2);
  });

  it("mantiene la decisión aún más sólida que los datos", () => {
    const datos = resources.materialFor({ color: "#36C6D8", status: "idle", selected: false, container: false, type: "data" }) as THREE.MeshPhysicalMaterial;
    const decision = resources.materialFor({ color: "#B078FF", status: "idle", selected: false, container: false, type: "decision" }) as THREE.MeshPhysicalMaterial;
    expect(decision.transmission).toBeLessThan(datos.transmission);
  });

  it("da al proceso un metal cepillado, ni espejo ni mate", () => {
    const material = resources.materialFor({ color: "#4D8DFF", status: "idle", selected: false, container: false, type: "process" }) as THREE.MeshPhysicalMaterial;
    expect(material.roughness).toBeGreaterThan(0.25);
    expect(material.roughness).toBeLessThan(0.6);
    expect(material.clearcoat).toBeGreaterThan(0);
  });

  it("no confunde los materiales de dos tipos con el mismo color", () => {
    const integracion = resources.materialFor({ color: "#FFFFFF", status: "idle", selected: false, container: false, type: "integration" });
    const datos = resources.materialFor({ color: "#FFFFFF", status: "idle", selected: false, container: false, type: "data" });
    expect(datos).not.toBe(integracion);
  });

  it("sigue compartiendo el material entre nodos idénticos", () => {
    const spec = { color: "#4D8DFF", status: "idle" as const, selected: false, container: false, type: "process" as const };
    expect(resources.materialFor(spec)).toBe(resources.materialFor(spec));
  });

  it("apaga el brillo cuando el nodo está en un estado de ejecución", () => {
    const corriendo = resources.materialFor({ color: "#4D8DFF", status: "running", selected: false, container: false, type: "process" }) as THREE.MeshPhysicalMaterial;
    expect(corriendo.emissiveIntensity).toBeGreaterThan(0);
  });
});
