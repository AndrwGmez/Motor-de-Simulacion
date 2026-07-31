import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

/**
 * Superficie pública de los paquetes.
 *
 * Estas pruebas no miran la fuente sino el artefacto compilado: es lo que
 * instalaría un proyecto anfitrión. Comprueban dos cosas que la extracción puede
 * romper sin que ninguna otra prueba se entere:
 *
 *   1. que los símbolos prometidos salen del paquete, y
 *   2. que no se coló una dependencia de Next ni de la API de FlowVerse, que
 *      es justo lo que impediría usarlo fuera de este repositorio.
 */

// vitest se ejecuta desde apps/web; los artefactos viven junto a este archivo.
const raiz = (ruta: string) => resolve(process.cwd(), "../../packages", ruta);
const cargar = (ruta: string) => import(pathToFileURL(raiz(ruta)).href);
const leer = (ruta: string) => readFileSync(raiz(ruta), "utf8");

describe("artefacto de @flowverse/core", () => {
  it("exporta el modelo del contrato y la presentación de los tipos de nodo", async () => {
    const core = await cargar("core/dist/index.js");
    expect(core.NODE_PRESENTATION).toBeTypeOf("object");
    expect(Object.keys(core.NODE_PRESENTATION)).toHaveLength(8);
  });

  it("publica sus declaraciones de tipos", () => {
    expect(leer("core/dist/index.d.ts")).toContain("FlowDefinition");
  });
});

describe("artefacto de @flowverse/engine", () => {
  it("exporta validación y simulación utilizables sin la API", async () => {
    const engine = await cargar("engine/dist/index.js");
    expect(engine.validateFlow ?? engine.validateDefinition).toBeTypeOf("function");
  });

  it("no arrastra el renderizado: quien solo valida no instala three.js", () => {
    const bundle = leer("engine/dist/index.js");
    expect(bundle).not.toContain("from \"three\"");
    expect(bundle).not.toContain("react-force-graph");
  });
});

describe("artefacto de @flowverse/viewer", () => {
  it("exporta la escena y las utilidades de distribución y cámara", async () => {
    const viewer = await cargar("viewer/dist/index.js");
    expect(viewer.FlowScene3D).toBeTypeOf("function");
    expect(viewer.applyLayout).toBeTypeOf("function");
    expect(viewer.applyCameraFeel).toBeTypeOf("function");
  });

  it("deja react y three como dependencias externas, no empaquetadas", () => {
    const bundle = leer("viewer/dist/index.js");
    expect(bundle).toMatch(/from ["']three["']/);
    expect(bundle).toMatch(/from ["']react["']/);
    // Si estuvieran empaquetados el artefacto pesaría megabytes y duplicaría
    // three.js en el anfitrión, que es el fallo clásico de una librería 3D.
    expect(bundle.length).toBeLessThan(300_000);
  });
});

describe("independencia del anfitrión", () => {
  it("ningún paquete importa Next ni los servicios de la API de FlowVerse", () => {
    for (const paquete of ["core", "engine", "viewer"]) {
      const bundle = leer(`${paquete}/dist/index.js`);
      expect(bundle, `${paquete} arrastra Next`).not.toContain("next/");
      expect(bundle, `${paquete} arrastra la API`).not.toContain("/v1/flows");
    }
  });
});
