import { describe, expect, it } from "vitest";
import * as THREE from "three";
import {
  buscarNodos,
  seguirNodo,
  posicionMenuContextual,
  DEFAULT_PAN,
  applyCameraFeel,
  easePan,
  ignoresKeyboard,
  panCamera,
  panDirection,
} from "./scene-camera";

function cameraLookingAtOrigin(distance: number) {
  const camera = new THREE.PerspectiveCamera(60, 1.6, 1, 10000);
  camera.position.set(0, 0, distance);
  camera.lookAt(0, 0, 0);
  camera.updateMatrixWorld(true);
  return camera;
}

describe("panDirection", () => {
  it("traduce cada flecha a su eje", () => {
    expect(panDirection(new Set(["ArrowRight"]))).toEqual({ x: 1, y: 0 });
    expect(panDirection(new Set(["ArrowLeft"]))).toEqual({ x: -1, y: 0 });
    expect(panDirection(new Set(["ArrowUp"]))).toEqual({ x: 0, y: 1 });
    expect(panDirection(new Set(["ArrowDown"]))).toEqual({ x: 0, y: -1 });
  });

  it("anula las flechas opuestas pulsadas a la vez", () => {
    expect(panDirection(new Set(["ArrowLeft", "ArrowRight"]))).toEqual({ x: 0, y: 0 });
  });

  it("normaliza la diagonal para que no sea más rápida que un eje", () => {
    const diagonal = panDirection(new Set(["ArrowRight", "ArrowUp"]));
    expect(Math.hypot(diagonal.x, diagonal.y)).toBeCloseTo(1, 5);
  });

  it("no devuelve nada cuando no hay flechas pulsadas", () => {
    expect(panDirection(new Set(["KeyA", "Shift"]))).toEqual({ x: 0, y: 0 });
  });
});

describe("easePan", () => {
  it("avanza hacia el objetivo sin alcanzarlo de un salto", () => {
    const paso = easePan({ x: 0, y: 0 }, { x: 1, y: 0 }, DEFAULT_PAN.smoothing);
    expect(paso.x).toBeGreaterThan(0);
    expect(paso.x).toBeLessThan(1);
  });

  it("converge en el objetivo tras varios cuadros", () => {
    let actual = { x: 0, y: 0 };
    for (let cuadro = 0; cuadro < 120; cuadro += 1) {
      actual = easePan(actual, { x: 1, y: 0 }, DEFAULT_PAN.smoothing);
    }
    expect(actual.x).toBeCloseTo(1, 2);
  });

  it("frena de forma progresiva al soltar la tecla", () => {
    const primero = easePan({ x: 1, y: 0 }, { x: 0, y: 0 }, DEFAULT_PAN.smoothing);
    const segundo = easePan(primero, { x: 0, y: 0 }, DEFAULT_PAN.smoothing);
    expect(primero.x).toBeLessThan(1);
    expect(segundo.x).toBeLessThan(primero.x);
    expect(segundo.x).toBeGreaterThan(0);
  });
});

describe("panCamera", () => {
  it("traslada la cámara y su objetivo exactamente lo mismo, sin rotar", () => {
    const camera = cameraLookingAtOrigin(500);
    const target = new THREE.Vector3(0, 0, 0);
    const antesCamara = camera.position.clone();
    const antesObjetivo = target.clone();

    panCamera(camera, target, { x: 1, y: 0 }, DEFAULT_PAN);

    const deltaCamara = camera.position.clone().sub(antesCamara);
    const deltaObjetivo = target.clone().sub(antesObjetivo);
    expect(deltaCamara.distanceTo(deltaObjetivo)).toBeCloseTo(0, 6);
    expect(deltaCamara.length()).toBeGreaterThan(0);
  });

  it("mueve hacia la derecha de la cámara con la flecha derecha", () => {
    const camera = cameraLookingAtOrigin(500);
    const target = new THREE.Vector3(0, 0, 0);
    panCamera(camera, target, { x: 1, y: 0 }, DEFAULT_PAN);
    expect(camera.position.x).toBeGreaterThan(0);
    expect(Math.abs(camera.position.y)).toBeLessThan(0.001);
  });

  it("mueve hacia arriba con la flecha arriba", () => {
    const camera = cameraLookingAtOrigin(500);
    const target = new THREE.Vector3(0, 0, 0);
    panCamera(camera, target, { x: 0, y: 1 }, DEFAULT_PAN);
    expect(camera.position.y).toBeGreaterThan(0);
  });

  it("recorre más distancia cuanto más lejos está la cámara", () => {
    const cerca = cameraLookingAtOrigin(200);
    const lejos = cameraLookingAtOrigin(2000);
    const objetivoCerca = new THREE.Vector3();
    const objetivoLejos = new THREE.Vector3();
    panCamera(cerca, objetivoCerca, { x: 1, y: 0 }, DEFAULT_PAN);
    panCamera(lejos, objetivoLejos, { x: 1, y: 0 }, DEFAULT_PAN);
    expect(objetivoLejos.x).toBeGreaterThan(objetivoCerca.x);
  });

  it("no hace nada cuando la dirección es nula", () => {
    const camera = cameraLookingAtOrigin(500);
    const target = new THREE.Vector3(0, 0, 0);
    panCamera(camera, target, { x: 0, y: 0 }, DEFAULT_PAN);
    expect(camera.position.equals(new THREE.Vector3(0, 0, 500))).toBe(true);
  });
});

describe("ignoresKeyboard", () => {
  it("cede las flechas cuando se está escribiendo", () => {
    for (const etiqueta of ["input", "textarea", "select"]) {
      const elemento = document.createElement(etiqueta);
      expect(ignoresKeyboard(elemento)).toBe(true);
    }
  });

  it("cede las flechas dentro de un elemento editable", () => {
    const elemento = document.createElement("div");
    elemento.setAttribute("contenteditable", "true");
    expect(ignoresKeyboard(elemento)).toBe(true);
  });

  it("acepta las flechas sobre el resto de la página", () => {
    expect(ignoresKeyboard(document.createElement("div"))).toBe(false);
    expect(ignoresKeyboard(null)).toBe(false);
  });
});

describe("applyCameraFeel", () => {
  it("activa la inercia de la cámara para que el movimiento no sea brusco", () => {
    const controls = { enableDamping: false, dampingFactor: 0, rotateSpeed: 1, zoomSpeed: 1, panSpeed: 1 };
    applyCameraFeel(controls);
    expect(controls.enableDamping).toBe(true);
    expect(controls.dampingFactor).toBeGreaterThan(0);
    expect(controls.dampingFactor).toBeLessThan(0.15);
  });

  it("suaviza la rotación y el zoom respecto al valor por defecto", () => {
    const controls = { enableDamping: false, dampingFactor: 0, rotateSpeed: 1, zoomSpeed: 1, panSpeed: 1 };
    applyCameraFeel(controls);
    expect(controls.rotateSpeed).toBeLessThan(1);
    expect(controls.zoomSpeed).toBeLessThan(1);
  });
});

// Búsqueda de nodos (§8.1): en un grafo de miles, encontrar uno por nombre o
// identificador es la diferencia entre usarlo y no poder.
describe("buscarNodos", () => {
  const nodos = [
    { id: "validar_pago", label: "Validar pago" },
    { id: "pago_aprobado", label: "¿Pago aprobado?" },
    { id: "enviar_pedido", label: "Enviar pedido" },
  ];

  it("encuentra por etiqueta sin distinguir mayúsculas ni acentos", () => {
    expect(buscarNodos(nodos, "envio").map((n) => n.id)).toEqual([]);
    expect(buscarNodos(nodos, "ENVIAR").map((n) => n.id)).toEqual(["enviar_pedido"]);
    expect(buscarNodos(nodos, "aprobado").map((n) => n.id)).toEqual(["pago_aprobado"]);
  });

  it("encuentra también por identificador", () => {
    expect(buscarNodos(nodos, "validar_").map((n) => n.id)).toEqual(["validar_pago"]);
  });

  it("devuelve todas las coincidencias, no solo la primera", () => {
    expect(buscarNodos(nodos, "pago")).toHaveLength(2);
  });

  it("con la búsqueda vacía no devuelve nada, en vez de todo", () => {
    expect(buscarNodos(nodos, "   ")).toEqual([]);
  });

  it("acota el número de resultados para no colapsar el panel", () => {
    const muchos = Array.from({ length: 500 }, (_, i) => ({ id: `n${i}`, label: `Nodo ${i}` }));
    expect(buscarNodos(muchos, "Nodo", 20)).toHaveLength(20);
  });
});

// Seguimiento de la ejecución (§8.3): la cámara acompaña al nodo activo sin
// saltos, conservando el encuadre que el usuario había elegido.
describe("seguirNodo", () => {
  const nodo = { x: 400, y: 0, z: 0 };

  it("acerca cámara y objetivo hacia el nodo activo", () => {
    const camera = new THREE.PerspectiveCamera();
    camera.position.set(0, 0, 500);
    const target = new THREE.Vector3(0, 0, 0);
    seguirNodo(camera, target, nodo, 0.2);
    expect(target.x).toBeGreaterThan(0);
    expect(target.x).toBeLessThan(400);
  });

  it("conserva la distancia y el ángulo de la cámara al objetivo", () => {
    const camera = new THREE.PerspectiveCamera();
    camera.position.set(0, 120, 500);
    const target = new THREE.Vector3(0, 0, 0);
    const desfaseAntes = camera.position.clone().sub(target);
    seguirNodo(camera, target, nodo, 0.3);
    const desfaseDespues = camera.position.clone().sub(target);
    expect(desfaseDespues.distanceTo(desfaseAntes)).toBeCloseTo(0, 5);
  });

  it("converge en el nodo tras varios cuadros", () => {
    const camera = new THREE.PerspectiveCamera();
    camera.position.set(0, 0, 500);
    const target = new THREE.Vector3(0, 0, 0);
    for (let cuadro = 0; cuadro < 200; cuadro += 1) seguirNodo(camera, target, nodo, 0.15);
    expect(target.x).toBeCloseTo(400, 1);
  });

  it("no hace nada sin nodo activo", () => {
    const camera = new THREE.PerspectiveCamera();
    camera.position.set(0, 0, 500);
    const target = new THREE.Vector3(0, 0, 0);
    seguirNodo(camera, target, undefined, 0.3);
    expect(target.equals(new THREE.Vector3(0, 0, 0))).toBe(true);
  });
});

// Menú contextual (§8.1): al pulsar el botón derecho sobre el fondo se ofrece
// crear un nodo ahí mismo, no en un punto arbitrario.
describe("posicionMenuContextual", () => {
  it("mantiene el menú dentro del lienzo cuando se abre junto al borde", () => {
    const lienzo = { width: 800, height: 600 };
    const menu = { width: 220, height: 300 };
    expect(posicionMenuContextual({ x: 780, y: 580 }, lienzo, menu)).toEqual({ x: 580, y: 300 });
  });

  it("respeta la posición del cursor cuando hay sitio de sobra", () => {
    expect(posicionMenuContextual({ x: 100, y: 120 }, { width: 800, height: 600 }, { width: 220, height: 300 }))
      .toEqual({ x: 100, y: 120 });
  });

  it("nunca devuelve coordenadas negativas en un lienzo pequeño", () => {
    const p = posicionMenuContextual({ x: 10, y: 10 }, { width: 200, height: 200 }, { width: 220, height: 300 });
    expect(p.x).toBeGreaterThanOrEqual(0);
    expect(p.y).toBeGreaterThanOrEqual(0);
  });
});
