import { RoomEnvironment } from "three/examples/jsm/environments/RoomEnvironment.js";
import * as THREE from "three";

/**
 * Comportamiento de la cámara: inercia de los controles de órbita y
 * desplazamiento con las flechas del teclado.
 *
 * Las flechas trasladan la cámara y su punto de mira a la vez, así que el
 * encuadre se desplaza sin girar: arriba y abajo suben y bajan, izquierda y
 * derecha recorren el grafo de lado a lado. El paso es proporcional a la
 * distancia al objetivo, de modo que a pleno zoom de salida se cubre terreno y
 * de cerca el movimiento sigue siendo fino.
 */

export interface CameraControls {
  enableDamping: boolean;
  dampingFactor: number;
  rotateSpeed: number;
  zoomSpeed: number;
  panSpeed: number;
}

export function applyCameraFeel(controls: CameraControls): void {
  controls.enableDamping = true;
  controls.dampingFactor = 0.075;
  controls.rotateSpeed = 0.45;
  controls.zoomSpeed = 0.55;
  controls.panSpeed = 0.5;
}

export interface PanSettings {
  /** Fracción de la distancia al objetivo recorrida por cuadro a plena marcha. */
  speed: number;
  /** Cuánto se acerca la velocidad a su objetivo en cada cuadro. */
  smoothing: number;
  /** Multiplicador al mantener Mayúsculas. */
  boost: number;
}

export const DEFAULT_PAN: PanSettings = {
  speed: 0.012,
  smoothing: 0.14,
  boost: 3,
};

export interface PanVector {
  x: number;
  y: number;
}

const ARROWS: Record<string, PanVector> = {
  ArrowRight: { x: 1, y: 0 },
  ArrowLeft: { x: -1, y: 0 },
  ArrowUp: { x: 0, y: 1 },
  ArrowDown: { x: 0, y: -1 },
};

export const PAN_KEYS = Object.keys(ARROWS);

/** Suma las flechas pulsadas y normaliza para que la diagonal no corra más. */
export function panDirection(keys: ReadonlySet<string>): PanVector {
  let x = 0;
  let y = 0;
  for (const key of keys) {
    const arrow = ARROWS[key];
    if (!arrow) continue;
    x += arrow.x;
    y += arrow.y;
  }
  const length = Math.hypot(x, y);
  if (length === 0) return { x: 0, y: 0 };
  return { x: x / length, y: y / length };
}

/** Interpolación exponencial: arranca y frena de forma progresiva. */
export function easePan(current: PanVector, target: PanVector, smoothing: number): PanVector {
  return {
    x: current.x + (target.x - current.x) * smoothing,
    y: current.y + (target.y - current.y) * smoothing,
  };
}

const RIGHT = new THREE.Vector3();
const UP = new THREE.Vector3();
const SHIFT = new THREE.Vector3();

export function panCamera(
  camera: THREE.Camera,
  target: THREE.Vector3,
  offset: PanVector,
  settings: PanSettings = DEFAULT_PAN,
): void {
  if (offset.x === 0 && offset.y === 0) return;
  const step = camera.position.distanceTo(target) * settings.speed;
  if (step === 0) return;

  // Ejes de la propia cámara: el desplazamiento siempre coincide con lo que se
  // ve en pantalla, gire como gire la órbita.
  RIGHT.setFromMatrixColumn(camera.matrixWorld, 0).normalize();
  UP.setFromMatrixColumn(camera.matrixWorld, 1).normalize();

  SHIFT.set(0, 0, 0)
    .addScaledVector(RIGHT, offset.x * step)
    .addScaledVector(UP, offset.y * step);

  camera.position.add(SHIFT);
  target.add(SHIFT);
}

/** Mientras se escribe, las flechas pertenecen al campo de texto. */
export function ignoresKeyboard(element: Element | null): boolean {
  if (!element) return false;
  return element.matches("input, textarea, select, [contenteditable=true]");
}

/**
 * Iluminación de estudio: mapa de entorno y mapeado tonal.
 *
 * Sin un entorno que reflejar, un material metálico se ve igual de plano que
 * uno plástico: no hay nada alrededor que devuelva luz. `RoomEnvironment` genera
 * una sala con paneles luminosos por código —sin descargar ningún archivo— y
 * PMREM la convierte en el mapa que consumen los materiales físicos.
 */
export interface StudioTargets {
  scene: { environment: THREE.Texture | null; environmentIntensity?: number };
  renderer: THREE.WebGLRenderer;
}

export function applyStudioLighting({ scene, renderer }: StudioTargets): THREE.Texture | undefined {
  if (scene.environment) return scene.environment;
  const generator = new THREE.PMREMGenerator(renderer);
  generator.compileEquirectangularShader();
  const environment = generator.fromScene(new RoomEnvironment(), 0.04).texture;
  scene.environment = environment;
  if ("environmentIntensity" in scene) scene.environmentIntensity = 0.85;

  renderer.toneMapping = THREE.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.05;
  renderer.outputColorSpace = THREE.SRGBColorSpace;

  generator.dispose();
  return environment;
}

/**
 * Búsqueda de nodos por nombre o identificador (§8.1).
 *
 * Se normalizan acentos en ambos lados: quien busca "expedicion" espera
 * encontrar "Expedición", y obligarle a escribir la tilde es una forma tonta de
 * que la función no sirva.
 */
export interface NodoBuscable {
  id: string;
  label: string;
}

function normalizar(valor: string): string {
  return valor
    .toLocaleLowerCase("es")
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "");
}

export function buscarNodos<T extends NodoBuscable>(nodos: readonly T[], consulta: string, limite = 50): T[] {
  const termino = normalizar(consulta.trim());
  if (termino.length === 0) return [];
  const encontrados: T[] = [];
  for (const nodo of nodos) {
    if (normalizar(nodo.label).includes(termino) || normalizar(nodo.id).includes(termino)) {
      encontrados.push(nodo);
      if (encontrados.length >= limite) break;
    }
  }
  return encontrados;
}

/**
 * Seguimiento de la ejecución.
 *
 * La cámara acompaña al nodo activo desplazándose junto a su punto de mira, así
 * que **conserva el ángulo y la distancia que el usuario había elegido**: sigue
 * el flujo sin arrebatarle el encuadre. El movimiento es una interpolación
 * exponencial, igual que la inercia de los controles, para que no dé tirones.
 */
export function seguirNodo(
  camera: THREE.Camera,
  target: THREE.Vector3,
  nodo: { x?: number; y?: number; z?: number } | undefined,
  factor = 0.08,
): void {
  if (!nodo) return;
  const destino = new THREE.Vector3(nodo.x ?? 0, nodo.y ?? 0, nodo.z ?? 0);
  const paso = destino.sub(target).multiplyScalar(factor);
  target.add(paso);
  camera.position.add(paso);
}

/**
 * Coloca el menú contextual junto al cursor sin que se salga del lienzo.
 * Abrirlo cerca del borde derecho o inferior dejaría media lista fuera de
 * pantalla, que es la forma habitual de que un menú así resulte inservible.
 */
export function posicionMenuContextual(
  cursor: { x: number; y: number },
  lienzo: { width: number; height: number },
  menu: { width: number; height: number },
): { x: number; y: number } {
  return {
    x: Math.max(0, Math.min(cursor.x, lienzo.width - menu.width)),
    y: Math.max(0, Math.min(cursor.y, lienzo.height - menu.height)),
  };
}
