import * as THREE from "three";
import type { FlowNode, NodeRunStatus } from "@flowverse/core";
import { NODE_PRESENTATION } from "@flowverse/core";
import { SceneResources, STATUS_COLORS } from "./scene-resources";

/**
 * Dos técnicas para bajar el número de llamadas de dibujo sin perder ninguna
 * información del flujo.
 *
 * 1. Nivel de detalle: las etiquetas y las flechas se ocultan cuando están
 *    demasiado lejos para leerse, y reaparecen al acercarse. A pleno zoom de
 *    salida eso son 1.482 objetos menos en un grafo de 482 nodos.
 *
 * 2. Instanciación: los cuerpos de los nodos se dibujan en un lote por tipo y
 *    aspecto en lugar de uno por nodo. El cuerpo individual se marca invisible,
 *    pero `three` no comprueba la visibilidad al lanzar rayos, así que los nodos
 *    siguen siendo seleccionables y arrastrables exactamente igual.
 */

export interface LodSettings {
  labelDistance: number;
  arrowDistance: number;
  sharpLabelDistance: number;
  sharpLabelScale: number;
}

export const DEFAULT_LOD: LodSettings = {
  labelDistance: 1100,
  arrowDistance: 1600,
  /** Por debajo de esta distancia el texto de mapa de bits se ve escalonado. */
  sharpLabelDistance: 320,
  sharpLabelScale: 3,
};

interface Point {
  x?: number;
  y?: number;
  z?: number;
}

export interface LodTarget extends Point {
  id?: string;
  __threeObj?: THREE.Object3D;
}

export interface LodLink {
  source?: Point | string;
  target?: Point | string;
  __arrowObj?: THREE.Object3D;
}

export interface LodReport {
  labelsVisible: number;
  labelsHidden: number;
  arrowsVisible: number;
  arrowsHidden: number;
}

function distanceSquared(from: Point, x: number, y: number, z: number): number {
  const dx = (from.x ?? 0) - x;
  const dy = (from.y ?? 0) - y;
  const dz = (from.z ?? 0) - z;
  return dx * dx + dy * dy + dz * dz;
}

function coordinate(value: Point | string | undefined): Point {
  return value && typeof value === "object" ? value : { x: 0, y: 0, z: 0 };
}

export function applyLevelOfDetail(
  camera: Point,
  nodes: readonly LodTarget[],
  links: readonly LodLink[],
  settings: LodSettings = DEFAULT_LOD,
  selectedId?: string,
  requestLabel?: (node: LodTarget) => void,
  budget = 60,
  upgradeLabel?: (node: LodTarget, escala: number) => void,
): LodReport {
  const labelLimit = settings.labelDistance * settings.labelDistance;
  const sharpLimit = settings.sharpLabelDistance * settings.sharpLabelDistance;
  let upgraded = 0;
  const arrowLimit = settings.arrowDistance * settings.arrowDistance;
  const report: LodReport = { labelsVisible: 0, labelsHidden: 0, arrowsVisible: 0, arrowsHidden: 0 };

  let built = 0;
  for (const node of nodes) {
    // El nodo seleccionado conserva su etiqueta a cualquier distancia: es lo
    // que pide la especificación y evita perder de vista lo que estás editando.
    const visible = node.id !== undefined && node.id === selectedId
      ? true
      : distanceSquared(camera, node.x ?? 0, node.y ?? 0, node.z ?? 0) <= labelLimit;

    let label = node.__threeObj?.userData?.label as THREE.Object3D | undefined;
    if (!label) {
      // La etiqueta se fabrica la primera vez que hace falta, con un tope por
      // pasada para que acercarse de golpe no congele la escena.
      if (!visible || !requestLabel || !node.__threeObj || built >= budget) continue;
      requestLabel(node);
      built += 1;
      label = node.__threeObj.userData?.label as THREE.Object3D | undefined;
      if (!label) continue;
    }
    if (label.visible !== visible) label.visible = visible;

    // Solo las pocas etiquetas realmente cercanas suben de resolución, con el
    // mismo tope por pasada que su creación para no congelar la escena.
    if (visible && upgradeLabel && upgraded < budget) {
      const distancia = distanceSquared(camera, node.x ?? 0, node.y ?? 0, node.z ?? 0);
      const escala = distancia <= sharpLimit ? settings.sharpLabelScale : 1;
      if (node.__threeObj?.userData?.labelScale !== escala) {
        upgradeLabel(node, escala);
        upgraded += 1;
      }
    }
    if (visible) report.labelsVisible += 1;
    else report.labelsHidden += 1;
  }

  for (const link of links) {
    const arrow = link.__arrowObj;
    if (!arrow) continue;
    const source = coordinate(link.source);
    const target = coordinate(link.target);
    const middleX = ((source.x ?? 0) + (target.x ?? 0)) / 2;
    const middleY = ((source.y ?? 0) + (target.y ?? 0)) / 2;
    const middleZ = ((source.z ?? 0) + (target.z ?? 0)) / 2;
    const visible = distanceSquared(camera, middleX, middleY, middleZ) <= arrowLimit;
    if (arrow.visible !== visible) arrow.visible = visible;
    if (visible) report.arrowsVisible += 1;
    else report.arrowsHidden += 1;
  }

  return report;
}

export interface InstancedTarget extends Point {
  id: string;
  type: FlowNode["type"];
  label: string;
  metadata?: { color?: string };
  __threeObj?: THREE.Object3D;
}

interface Batch {
  mesh: THREE.InstancedMesh;
  members: InstancedTarget[];
}

const SCRATCH_MATRIX = new THREE.Matrix4();
const SCRATCH_POSITION = new THREE.Vector3();
const SCRATCH_SCALE = new THREE.Vector3(1, 1, 1);
const DECISION_ROTATION = new THREE.Quaternion().setFromEuler(new THREE.Euler(0, 0, Math.PI / 4));
const NO_ROTATION = new THREE.Quaternion();

export class InstancedBodies {
  private batches = new Map<string, Batch>();
  private hidden = new Set<THREE.Object3D>();
  /** Escena donde viven los lotes, para poder recolgarlos si desaparecen. */
  private scene?: THREE.Object3D;

  constructor(private readonly resources: SceneResources) {}

  get batchCount(): number {
    return this.batches.size;
  }

  /**
   * Reconstruye los lotes. Se llama cuando cambia el conjunto de nodos o su
   * aspecto, no en cada cuadro: las posiciones se refrescan aparte con
   * `updatePositions`, que es mucho más barato.
   */
  sync(
    scene: THREE.Object3D,
    nodes: readonly InstancedTarget[],
    statusOf: (node: InstancedTarget) => NodeRunStatus = () => "idle",
    selectedId?: string,
  ): boolean {
    this.scene = scene;
    this.clearBatches(scene);

    const grouped = new Map<string, { members: InstancedTarget[]; color: string; status: NodeRunStatus; selected: boolean }>();
    // Al refrescar el grafo, la librería rehace sus objetos y durante unos
    // cuadros los nodos no tienen `__threeObj`. Si se ocultan los cuerpos sin
    // poder construir el lote que los sustituye, desaparecen de la escena hasta
    // que el usuario recarga la página.
    let listos = 0;
    for (const node of nodes) {
      const body = node.__threeObj?.userData?.body as THREE.Mesh | undefined;
      if (!body) continue;
      listos += 1;
      const status = statusOf(node);
      const selected = node.id === selectedId;
      const color = status === "idle"
        ? node.metadata?.color ?? NODE_PRESENTATION[node.type].color
        : STATUS_COLORS[status];
      const key = `${node.type}|${color}|${status}|${selected ? "sel" : "norm"}`;
      const bucket = grouped.get(key);
      if (bucket) bucket.members.push(node);
      else grouped.set(key, { members: [node], color, status, selected });
    }

    if (nodes.length > 0 && listos === 0) {
      for (const body of this.hidden) body.visible = true;
      this.hidden.clear();
      return false;
    }

    for (const [key, bucket] of grouped) {
      const type = bucket.members[0].type;
      const mesh = new THREE.InstancedMesh(
        this.resources.geometryFor(type),
        this.resources.materialFor({ color: bucket.color, status: bucket.status, selected: bucket.selected, container: type === "group" }),
        bucket.members.length,
      );
      mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
      mesh.frustumCulled = false;
      mesh.renderOrder = 0;
      mesh.userData.flowverseBatch = key;
      scene.add(mesh);
      this.batches.set(key, { mesh, members: bucket.members });

      for (const member of bucket.members) {
        const body = member.__threeObj?.userData?.body as THREE.Mesh | undefined;
        if (body && body.visible) {
          body.visible = false;
          this.hidden.add(body);
        }
      }
    }

    this.updatePositions();
    return true;
  }

  /** Copia la posición de cada nodo a la matriz de su instancia. */
  updatePositions(): void {
    for (const batch of this.batches.values()) {
      // three-forcegraph reconstruye su árbol al refrescar y se lleva por
      // delante lo que haya colgado ahí. Recolgarlo aquí cuesta una comparación
      // por lote y evita que los nodos se esfumen mientras interactúas.
      if (this.scene && batch.mesh.parent !== this.scene) this.scene.add(batch.mesh);
      for (let index = 0; index < batch.members.length; index += 1) {
        const member = batch.members[index];
        const object = member.__threeObj;
        SCRATCH_POSITION.set(
          object?.position.x ?? member.x ?? 0,
          object?.position.y ?? member.y ?? 0,
          object?.position.z ?? member.z ?? 0,
        );
        SCRATCH_MATRIX.compose(
          SCRATCH_POSITION,
          member.type === "decision" ? DECISION_ROTATION : NO_ROTATION,
          SCRATCH_SCALE,
        );
        batch.mesh.setMatrixAt(index, SCRATCH_MATRIX);
      }
      batch.mesh.instanceMatrix.needsUpdate = true;
    }
  }

  private clearBatches(scene: THREE.Object3D): void {
    for (const batch of this.batches.values()) {
      scene.remove(batch.mesh);
      batch.mesh.dispose();
    }
    this.batches.clear();
  }

  dispose(scene: THREE.Object3D): void {
    this.clearBatches(scene);
    for (const body of this.hidden) body.visible = true;
    this.hidden.clear();
  }
}


export interface BatchedLink {
  id?: string;
  source?: Point | string;
  target?: Point | string;
  __lineObj?: THREE.Object3D;
}

export interface LinkColors {
  normal: string;
  selected: string;
  active: string;
  opacity: number;
  radius: number;
}

// El editor pinta las conexiones con `rgba(110, 132, 190, .52)` y además aplica
// `linkOpacity` 0.72, así que la opacidad efectiva es el producto de ambas.
export const DEFAULT_LINK_COLORS: LinkColors = {
  normal: "#6e84be",
  selected: "#a5b4ff",
  active: "#52ddff",
  opacity: 0.72 * 0.52,
  radius: 0.55,
};

const LINK_MATRIX = new THREE.Matrix4();
const LINK_FROM = new THREE.Vector3();
const LINK_TO = new THREE.Vector3();
const LINK_DIRECTION = new THREE.Vector3();
const LINK_QUATERNION = new THREE.Quaternion();
const LINK_SCALE = new THREE.Vector3();
const LINK_UP = new THREE.Vector3(0, 1, 0);
const LINK_COLOR = new THREE.Color();

/**
 * Todas las conexiones en un solo objeto instanciado.
 *
 * La librería dibuja un cilindro por conexión, así que diez mil conexiones son
 * diez mil llamadas de dibujo. Aquí se reutiliza el mismo cilindro unitario
 * —orientado y escalado por instancia— para obtener exactamente los mismos
 * tubos en una única llamada.
 */
export class BatchedLinks {
  private scene?: THREE.Object3D;
  private mesh?: THREE.InstancedMesh;
  private geometry?: THREE.CylinderGeometry;
  private material?: THREE.MeshLambertMaterial;
  private members: BatchedLink[] = [];
  private hidden = new Set<THREE.Object3D>();

  get count(): number {
    return this.mesh?.count ?? 0;
  }

  /**
   * Devuelve `true` cuando ha podido agrupar. La librería resuelve `source` y
   * `target` de cadena a objeto y crea `__lineObj` unos cuadros después de
   * montar el grafo, así que el editor reintenta hasta que estén listos.
   */
  sync(
    scene: THREE.Object3D,
    links: readonly BatchedLink[],
    options: { selectedId?: string; activeId?: string; colors?: LinkColors } = {},
  ): boolean {
    const colors = options.colors ?? DEFAULT_LINK_COLORS;
    this.scene = scene;
    const listos = links.filter((link) =>
      typeof link.source === "object" && typeof link.target === "object" && link.__lineObj !== undefined);
    // Se agrupa lo que ya esté resuelto; si no hay nada, se reintenta.
    if (listos.length === 0) return links.length === 0;

    this.clear(scene);
    this.members = listos;
    if (this.members.length === 0) return true;

    // Cilindro unitario con la base en y=0: escalando en Y se estira desde el
    // origen de la conexión hasta su destino.
    this.geometry = new THREE.CylinderGeometry(1, 1, 1, 6, 1, true);
    this.geometry.translate(0, 0.5, 0);
    // Mismo material que usa la librería para sus tubos, para que el sombreado
    // con las luces de la escena coincida.
    // `InstancedMesh.setColorAt` ya activa el color por instancia; declarar
    // `vertexColors` sin atributo de color en la geometría los dejaba negros.
    this.material = new THREE.MeshLambertMaterial({
      transparent: true,
      opacity: colors.opacity,
    });
    this.mesh = new THREE.InstancedMesh(this.geometry, this.material, this.members.length);
    this.mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    this.mesh.frustumCulled = false;
    scene.add(this.mesh);

    for (let index = 0; index < this.members.length; index += 1) {
      const link = this.members[index];
      const color = link.id && link.id === options.activeId
        ? colors.active
        : link.id && link.id === options.selectedId
          ? colors.selected
          : colors.normal;
      this.mesh.setColorAt(index, LINK_COLOR.set(color));
      if (link.__lineObj && link.__lineObj.visible) {
        link.__lineObj.visible = false;
        this.hidden.add(link.__lineObj);
      }
    }
    if (this.mesh.instanceColor) this.mesh.instanceColor.needsUpdate = true;

    this.updatePositions(colors.radius);
    return true;
  }

  /** Reafirma que las conexiones originales siguen ocultas. */
  hideOriginals(): void {
    for (const link of this.members) {
      if (link.__lineObj && link.__lineObj.visible) {
        link.__lineObj.visible = false;
        this.hidden.add(link.__lineObj);
      }
    }
  }

  updatePositions(radius = DEFAULT_LINK_COLORS.radius): void {
    const mesh = this.mesh;
    if (!mesh) return;
    if (this.scene && mesh.parent !== this.scene) this.scene.add(mesh);
    this.hideOriginals();
    for (let index = 0; index < this.members.length; index += 1) {
      const link = this.members[index];
      const source = coordinate(link.source);
      const target = coordinate(link.target);
      LINK_FROM.set(source.x ?? 0, source.y ?? 0, source.z ?? 0);
      LINK_TO.set(target.x ?? 0, target.y ?? 0, target.z ?? 0);
      LINK_DIRECTION.subVectors(LINK_TO, LINK_FROM);
      const length = LINK_DIRECTION.length();
      if (length === 0) {
        LINK_SCALE.set(0, 0, 0);
        LINK_MATRIX.compose(LINK_FROM, LINK_QUATERNION.identity(), LINK_SCALE);
      } else {
        LINK_QUATERNION.setFromUnitVectors(LINK_UP, LINK_DIRECTION.clone().divideScalar(length));
        LINK_SCALE.set(radius, length, radius);
        LINK_MATRIX.compose(LINK_FROM, LINK_QUATERNION, LINK_SCALE);
      }
      mesh.setMatrixAt(index, LINK_MATRIX);
    }
    mesh.instanceMatrix.needsUpdate = true;
  }

  private clear(scene: THREE.Object3D): void {
    if (this.mesh) {
      scene.remove(this.mesh);
      this.mesh.dispose();
      this.mesh = undefined;
    }
    this.geometry?.dispose();
    this.material?.dispose();
    this.geometry = undefined;
    this.material = undefined;
    this.members = [];
  }

  dispose(scene: THREE.Object3D): void {
    this.clear(scene);
    for (const line of this.hidden) line.visible = true;
    this.hidden.clear();
  }
}
