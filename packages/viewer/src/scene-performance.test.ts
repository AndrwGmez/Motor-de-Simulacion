import { beforeEach, describe, expect, it } from "vitest";
import * as THREE from "three";
import { BatchedLinks, InstancedBodies, applyLevelOfDetail, DEFAULT_LOD, type LodTarget, type LodLink } from "./scene-performance";
import { SceneResources } from "./scene-resources";
import type { FlowNode, NodeType } from "@flowverse/core";

function fakeCanvas() {
  const canvas = {
    width: 0,
    height: 0,
    getContext: () => ({
      clearRect() {}, beginPath() {}, roundRect() {}, fill() {}, stroke() {}, fillText() {},
      measureText: (text: string) => ({ width: text.length * 17 }),
      set font(_v: string) {}, set textAlign(_v: string) {}, set textBaseline(_v: string) {},
      set fillStyle(_v: string) {}, set strokeStyle(_v: string) {}, set lineWidth(_v: number) {},
    }),
  };
  return canvas as unknown as HTMLCanvasElement;
}

function node(id: string, type: NodeType, position = { x: 0, y: 0, z: 0 }, color?: string): FlowNode {
  return {
    id, type, label: `Etiqueta ${id}`, description: "",
    inputs: [], outputs: [], activationMode: "each", durationMs: 0,
    configuration: {}, position, locked: false,
    metadata: color ? { color } : {},
  } as FlowNode;
}

function lodTarget(x: number): LodTarget {
  return { x, y: 0, z: 0, __threeObj: { userData: { label: { visible: true } } } as unknown as THREE.Object3D };
}

describe("applyLevelOfDetail", () => {
  const camera = { x: 0, y: 0, z: 0 };

  it("oculta la etiqueta de un nodo lejano y conserva la del cercano", () => {
    const cerca = lodTarget(100);
    const lejos = lodTarget(DEFAULT_LOD.labelDistance + 500);
    applyLevelOfDetail(camera, [cerca, lejos], [], DEFAULT_LOD);
    expect(cerca.__threeObj!.userData.label.visible).toBe(true);
    expect(lejos.__threeObj!.userData.label.visible).toBe(false);
  });

  it("devuelve a mostrar la etiqueta cuando la cámara se acerca", () => {
    const objetivo = lodTarget(DEFAULT_LOD.labelDistance + 500);
    applyLevelOfDetail(camera, [objetivo], [], DEFAULT_LOD);
    expect(objetivo.__threeObj!.userData.label.visible).toBe(false);
    applyLevelOfDetail({ x: objetivo.x, y: 0, z: 0 }, [objetivo], [], DEFAULT_LOD);
    expect(objetivo.__threeObj!.userData.label.visible).toBe(true);
  });

  it("oculta las flechas por su propia distancia, medida en el punto medio", () => {
    const cerca: LodLink = {
      source: { x: 0, y: 0, z: 0 }, target: { x: 40, y: 0, z: 0 },
      __arrowObj: { visible: true } as unknown as THREE.Object3D,
    };
    const lejos: LodLink = {
      source: { x: 9000, y: 0, z: 0 }, target: { x: 9040, y: 0, z: 0 },
      __arrowObj: { visible: true } as unknown as THREE.Object3D,
    };
    applyLevelOfDetail(camera, [], [cerca, lejos], DEFAULT_LOD);
    expect(cerca.__arrowObj!.visible).toBe(true);
    expect(lejos.__arrowObj!.visible).toBe(false);
  });

  it("informa de cuántos elementos quedan visibles", () => {
    const resultado = applyLevelOfDetail(
      camera,
      [lodTarget(50), lodTarget(80), lodTarget(DEFAULT_LOD.labelDistance + 900)],
      [],
      DEFAULT_LOD,
    );
    expect(resultado.labelsVisible).toBe(2);
  });

  it("ignora los nodos cuyo objeto todavía no existe", () => {
    expect(() => applyLevelOfDetail(camera, [{ x: 0, y: 0, z: 0 }], [], DEFAULT_LOD)).not.toThrow();
  });

  it("mantiene siempre visible la etiqueta del nodo seleccionado, esté donde esté", () => {
    const lejano = { ...lodTarget(DEFAULT_LOD.labelDistance + 5000), id: "elegido" };
    applyLevelOfDetail(camera, [lejano], [], DEFAULT_LOD, "elegido");
    expect(lejano.__threeObj!.userData.label.visible).toBe(true);
  });
});

describe("InstancedBodies", () => {
  let resources: SceneResources;
  let scene: THREE.Scene;
  let bodies: InstancedBodies;

  beforeEach(() => {
    resources = new SceneResources(fakeCanvas);
    scene = new THREE.Scene();
    bodies = new InstancedBodies(resources);
  });

  const conObjeto = (item: FlowNode, x: number) => {
    const group = resources.nodeObject(item, "idle", false);
    group.position.set(x, 0, 0);
    return { ...item, x, y: 0, z: 0, __threeObj: group };
  };

  it("agrupa los cuerpos en un lote por tipo y aspecto, no uno por nodo", () => {
    const entradas = Array.from({ length: 120 }, (_, index) =>
      conObjeto(node(`n${index}`, index % 2 ? "process" : "decision", { x: index, y: 0, z: 0 }, "#4D8DFF"), index));
    bodies.sync(scene, entradas);
    const lotes = scene.children.filter((child) => child instanceof THREE.InstancedMesh);
    expect(lotes.length).toBe(2);
    expect(lotes.reduce((total, lote) => total + (lote as THREE.InstancedMesh).count, 0)).toBe(120);
  });

  it("coloca cada instancia en la posición de su nodo", () => {
    const entradas = [conObjeto(node("a", "process"), 250)];
    bodies.sync(scene, entradas);
    const lote = scene.children.find((child) => child instanceof THREE.InstancedMesh) as THREE.InstancedMesh;
    const matriz = new THREE.Matrix4();
    lote.getMatrixAt(0, matriz);
    expect(new THREE.Vector3().setFromMatrixPosition(matriz).x).toBe(250);
  });

  it("oculta el cuerpo individual para que no se dibuje dos veces", () => {
    const entrada = conObjeto(node("a", "process"), 0);
    bodies.sync(scene, [entrada]);
    expect(entrada.__threeObj.userData.body.visible).toBe(false);
  });

  it("no toca la etiqueta, que no se instancia", () => {
    const entrada = conObjeto(node("a", "process"), 0);
    resources.attachLabel(entrada.__threeObj);
    bodies.sync(scene, [entrada]);
    expect(entrada.__threeObj.userData.label.visible).toBe(true);
    expect(entrada.__threeObj.userData.body.visible).toBe(false);
  });

  it("devuelve los cuerpos a su estado original al desecharse", () => {
    const entrada = conObjeto(node("a", "process"), 0);
    bodies.sync(scene, [entrada]);
    bodies.dispose(scene);
    expect(entrada.__threeObj.userData.body.visible).toBe(true);
    expect(scene.children.filter((child) => child instanceof THREE.InstancedMesh)).toHaveLength(0);
  });
});

describe("BatchedLinks", () => {
  let scene: THREE.Scene;
  let links: BatchedLinks;

  beforeEach(() => {
    scene = new THREE.Scene();
    links = new BatchedLinks();
  });

  const conexion = (desde: [number, number, number], hasta: [number, number, number]) => ({
    id: `${desde.join()}-${hasta.join()}`,
    source: { x: desde[0], y: desde[1], z: desde[2] },
    target: { x: hasta[0], y: hasta[1], z: hasta[2] },
    __lineObj: { visible: true } as unknown as THREE.Object3D,
  });

  it("dibuja todas las conexiones en un único objeto", () => {
    const datos = Array.from({ length: 500 }, (_, i) => conexion([i, 0, 0], [i, 100, 0]));
    links.sync(scene, datos);
    const lotes = scene.children.filter((child) => child instanceof THREE.InstancedMesh);
    expect(lotes).toHaveLength(1);
    expect((lotes[0] as THREE.InstancedMesh).count).toBe(500);
  });

  it("coloca cada tubo entre el origen y el destino de su conexión", () => {
    links.sync(scene, [conexion([0, 0, 0], [0, 200, 0])]);
    const lote = scene.children.find((c) => c instanceof THREE.InstancedMesh) as THREE.InstancedMesh;
    const matriz = new THREE.Matrix4();
    lote.getMatrixAt(0, matriz);
    // El cilindro unitario va de y=0 a y=1: su centro debe caer en el punto medio.
    const centro = new THREE.Vector3(0, 0.5, 0).applyMatrix4(matriz);
    expect(centro.y).toBeCloseTo(100, 3);
    expect(centro.x).toBeCloseTo(0, 3);
  });

  it("orienta el tubo a lo largo de la conexión, no solo en vertical", () => {
    links.sync(scene, [conexion([0, 0, 0], [300, 0, 0])]);
    const lote = scene.children.find((c) => c instanceof THREE.InstancedMesh) as THREE.InstancedMesh;
    const matriz = new THREE.Matrix4();
    lote.getMatrixAt(0, matriz);
    const extremo = new THREE.Vector3(0, 1, 0).applyMatrix4(matriz);
    expect(extremo.x).toBeCloseTo(300, 2);
    expect(extremo.y).toBeCloseTo(0, 2);
  });

  it("oculta las conexiones originales para no dibujarlas dos veces", () => {
    const datos = [conexion([0, 0, 0], [10, 0, 0])];
    links.sync(scene, datos);
    expect(datos[0].__lineObj.visible).toBe(false);
  });

  it("resalta la conexión activa con su propio color", () => {
    const datos = [conexion([0, 0, 0], [10, 0, 0]), conexion([0, 0, 0], [0, 10, 0])];
    links.sync(scene, datos, { activeId: datos[1].id });
    const lote = scene.children.find((c) => c instanceof THREE.InstancedMesh) as THREE.InstancedMesh;
    const normal = new THREE.Color();
    const activa = new THREE.Color();
    lote.getColorAt(0, normal);
    lote.getColorAt(1, activa);
    expect(activa.getHexString()).not.toBe(normal.getHexString());
  });

  it("devuelve las conexiones originales a su estado al desecharse", () => {
    const datos = [conexion([0, 0, 0], [10, 0, 0])];
    links.sync(scene, datos);
    links.dispose(scene);
    expect(datos[0].__lineObj.visible).toBe(true);
    expect(scene.children.filter((c) => c instanceof THREE.InstancedMesh)).toHaveLength(0);
  });
});

describe("etiquetas bajo demanda", () => {
  it("no fabrica la etiqueta hasta que el nodo entra en distancia", () => {
    let lienzos = 0;
    const contador = () => { lienzos += 1; return fakeCanvas(); };
    const recursos = new SceneResources(contador);
    const item = node("n1", "process");
    recursos.nodeObject(item, "idle", false);
    const trasConstruir = lienzos;

    recursos.ensureLabel(item, false);
    expect(lienzos).toBeGreaterThan(trasConstruir);
  });

  it("solo la fabrica una vez por nodo", () => {
    const recursos = new SceneResources(fakeCanvas);
    const item = node("n2", "process");
    const grupo = recursos.nodeObject(item, "idle", false);
    recursos.ensureLabel(item, false);
    const primera = grupo.userData.label;
    recursos.ensureLabel(item, false);
    expect(grupo.userData.label).toBe(primera);
  });

  it("el nivel de detalle pide la etiqueta al acercarse y no antes", () => {
    const pedidos: string[] = [];
    const objetivo = { id: "a", x: 100, y: 0, z: 0, __threeObj: { userData: {} } as unknown as THREE.Object3D };
    const lejano = { id: "b", x: 99999, y: 0, z: 0, __threeObj: { userData: {} } as unknown as THREE.Object3D };
    applyLevelOfDetail({ x: 0, y: 0, z: 0 }, [objetivo, lejano], [], DEFAULT_LOD, undefined, (n) => { pedidos.push(n.id!); });
    expect(pedidos).toEqual(["a"]);
  });
});

describe("BatchedLinks · la librería recrea sus objetos", () => {
  it("vuelve a ocultar una conexión que reaparece visible", () => {
    const scene = new THREE.Scene();
    const links = new BatchedLinks();
    const datos = [{
      id: "e1",
      source: { x: 0, y: 0, z: 0 },
      target: { x: 10, y: 0, z: 0 },
      __lineObj: { visible: true } as unknown as THREE.Object3D,
    }];
    links.sync(scene, datos);
    expect(datos[0].__lineObj.visible).toBe(false);

    // Simula el refresco de la librería, que devuelve la línea a visible.
    datos[0].__lineObj.visible = true;
    links.updatePositions();
    expect(datos[0].__lineObj.visible).toBe(false);
  });
});

describe("InstancedBodies · el grafo se refresca mientras interactúas", () => {
  it("avisa de que no pudo agrupar cuando los nodos aún no tienen objeto 3D", () => {
    const scene = new THREE.Scene();
    const resources = new SceneResources(fakeCanvas);
    const bodies = new InstancedBodies(resources);
    const sinObjeto = { id: "a", type: "process" as const, label: "A", x: 0, y: 0, z: 0 };
    expect(bodies.sync(scene, [sinObjeto])).toBe(false);
  });

  it("nunca deja los cuerpos ocultos sin un lote que los dibuje", () => {
    const scene = new THREE.Scene();
    const resources = new SceneResources(fakeCanvas);
    const bodies = new InstancedBodies(resources);
    const item = node("a", "process");
    const grupo = resources.nodeObject(item, "idle", false);
    const entrada = { ...item, x: 0, y: 0, z: 0, __threeObj: grupo };

    bodies.sync(scene, [entrada]);
    expect(grupo.userData.body.visible).toBe(false);

    // La librería rehace el grafo: por un instante los nodos pierden su objeto.
    // Si el lote no se puede reconstruir, los cuerpos deben volver a verse en
    // lugar de desaparecer hasta que el usuario recargue la página.
    bodies.sync(scene, [{ id: "a", type: "process" as const, label: "A", x: 0, y: 0, z: 0 }]);
    expect(grupo.userData.body.visible).toBe(true);
  });
});

describe("los lotes sobreviven a que la librería rehaga la escena", () => {
  it("vuelve a colgar el lote de cuerpos si algo lo descuelga", () => {
    const scene = new THREE.Scene();
    const resources = new SceneResources(fakeCanvas);
    const bodies = new InstancedBodies(resources);
    const item = node("a", "process");
    const grupo = resources.nodeObject(item, "idle", false);
    bodies.sync(scene, [{ ...item, x: 0, y: 0, z: 0, __threeObj: grupo }]);

    const lote = scene.children.find((c) => c instanceof THREE.InstancedMesh) as THREE.InstancedMesh;
    expect(lote).toBeDefined();

    // three-forcegraph reconstruye su árbol al refrescar y se lleva por delante
    // lo que haya colgado ahí. Si el lote desaparece y el cuerpo sigue oculto,
    // el nodo se esfuma de la pantalla hasta que se recarga la página.
    scene.remove(lote);
    bodies.updatePositions();
    expect(lote.parent).toBe(scene);
  });

  it("vuelve a colgar el lote de conexiones si algo lo descuelga", () => {
    const scene = new THREE.Scene();
    const links = new BatchedLinks();
    links.sync(scene, [{
      id: "e1",
      source: { x: 0, y: 0, z: 0 },
      target: { x: 10, y: 0, z: 0 },
      __lineObj: { visible: true } as unknown as THREE.Object3D,
    }]);
    const lote = scene.children.find((c) => c instanceof THREE.InstancedMesh) as THREE.InstancedMesh;
    scene.remove(lote);
    links.updatePositions();
    expect(lote.parent).toBe(scene);
  });
});

describe("el bucle por cuadro no toca lo que no debe", () => {
  it("no recalcula la esfera envolvente en cada cuadro", () => {
    const scene = new THREE.Scene();
    const resources = new SceneResources(fakeCanvas);
    const bodies = new InstancedBodies(resources);
    const item = node("a", "process");
    const grupo = resources.nodeObject(item, "idle", false);
    bodies.sync(scene, [{ ...item, x: 0, y: 0, z: 0, __threeObj: grupo }]);

    const lote = scene.children.find((c) => c instanceof THREE.InstancedMesh) as THREE.InstancedMesh;
    let recalculos = 0;
    lote.computeBoundingSphere = () => { recalculos += 1; };

    for (let cuadro = 0; cuadro < 10; cuadro += 1) bodies.updatePositions();

    // Con `frustumCulled = false` la esfera no la usa nadie, y recalcularla
    // sobre miles de instancias en cada cuadro hacía que el renderizador dejara
    // de dibujar los cuerpos en cuanto movías la cámara.
    expect(recalculos).toBe(0);
    expect(lote.frustumCulled).toBe(false);
  });
});
