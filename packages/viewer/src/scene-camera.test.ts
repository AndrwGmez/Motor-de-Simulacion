import { describe, expect, it } from "vitest";
import * as THREE from "three";
import {
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
