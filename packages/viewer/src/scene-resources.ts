import * as THREE from "three";
import { NODE_PRESENTATION, type FlowNode, type NodeRunStatus, type NodeType } from "@flowverse/core";

/**
 * Caché de recursos de la escena tridimensional.
 *
 * El editor construía una geometría, un material y una textura por nodo. Con
 * 482 nodos eso son 482 geometrías y 482 materiales que solo se diferencian en
 * un puñado de colores, además de casi 40 megapíxeles de lienzo para las
 * etiquetas. Aquí se comparte todo lo que es visualmente idéntico: el resultado
 * en pantalla no cambia ni un píxel, pero el trabajo de construcción y la
 * memoria caen en un orden de magnitud.
 */

export const STATUS_COLORS: Record<NodeRunStatus, string> = {
  idle: "#7f8ba8",
  queued: "#f4c261",
  running: "#69a3ff",
  success: "#37d6a0",
  failed: "#ff617d",
  skipped: "#596174",
  waiting: "#f49a59",
};

// Medidas heredadas del diseño original. El lienzo de referencia era siempre de
// 640 × 128 y el sprite de 90 × 18, así que la densidad de téxeles por unidad de
// mundo es 640/90. Recortar el lienzo al ancho real del texto y escalar el
// sprite en la misma proporción conserva esa densidad exacta.
const LABEL_REFERENCE_WIDTH = 640;
const LABEL_HEIGHT = 128;
const SPRITE_REFERENCE_WIDTH = 90;
const SPRITE_HEIGHT = 18;
const LABEL_MAX_BOX = 590;
const LABEL_PADDING = 48;
const STROKE_MARGIN = 8;

export interface MaterialSpec {
  color: string;
  status: NodeRunStatus;
  selected: boolean;
  container: boolean;
  type?: NodeType;
}

/**
 * Acabado físico por tipo de nodo. El material anterior era el mismo plástico
 * para todo; aquí cada forma tiene una superficie con sentido, que es lo que
 * hace que el mapa de entorno produzca reflejos creíbles en vez de un brillo
 * uniforme.
 */
interface Finish {
  metalness: number;
  roughness: number;
  clearcoat: number;
  clearcoatRoughness: number;
  transmission: number;
  ior: number;
  reflectivity: number;
}

const FINISHES: Record<NodeType, Finish> = {
  // Servicio externo: metal pulido, el más reflectante del conjunto.
  integration: { metalness: 0.92, roughness: 0.16, clearcoat: 0.6, clearcoatRoughness: 0.12, transmission: 0, ior: 1.5, reflectivity: 0.6 },
  // Datos: cristal con cuerpo. Con transmisión alta el nodo se desvanecía
  // contra el fondo oscuro y se perdía su color, que es lo que lo identifica.
  data: { metalness: 0.08, roughness: 0.1, clearcoat: 1, clearcoatRoughness: 0.05, transmission: 0.26, ior: 1.52, reflectivity: 0.55 },
  // Decisión: cristal facetado, más denso todavía que el de datos.
  decision: { metalness: 0.12, roughness: 0.08, clearcoat: 1, clearcoatRoughness: 0.04, transmission: 0.16, ior: 1.65, reflectivity: 0.6 },
  // Proceso: metal cepillado.
  process: { metalness: 0.75, roughness: 0.38, clearcoat: 0.35, clearcoatRoughness: 0.3, transmission: 0, ior: 1.5, reflectivity: 0.5 },
  // Inicio y fin: metal satinado, algo más suave.
  trigger: { metalness: 0.68, roughness: 0.28, clearcoat: 0.5, clearcoatRoughness: 0.2, transmission: 0, ior: 1.5, reflectivity: 0.55 },
  end: { metalness: 0.6, roughness: 0.24, clearcoat: 0.7, clearcoatRoughness: 0.15, transmission: 0, ior: 1.5, reflectivity: 0.6 },
  // Espera: anillo cromado.
  delay: { metalness: 0.88, roughness: 0.2, clearcoat: 0.5, clearcoatRoughness: 0.15, transmission: 0, ior: 1.5, reflectivity: 0.6 },
  // Contenedor: solo estructura, sin cuerpo.
  group: { metalness: 0.2, roughness: 0.6, clearcoat: 0, clearcoatRoughness: 0.5, transmission: 0, ior: 1.5, reflectivity: 0.3 },
};

export interface LabelMetrics {
  text: string;
  boxWidth: number;
  canvasWidth: number;
  canvasHeight: number;
  spriteWidth: number;
  spriteHeight: number;
}

type CanvasFactory = () => HTMLCanvasElement;

function fontFor(selected: boolean, escala = 1): string {
  return `${selected ? 650 : 520} ${34 * escala}px Inter, Arial, sans-serif`;
}

export class SceneResources {
  private readonly geometries = new Map<string, THREE.BufferGeometry>();
  private readonly materials = new Map<string, THREE.Material>();
  private readonly labels = new Map<string, THREE.SpriteMaterial>();
  private readonly metrics = new Map<string, LabelMetrics>();
  private readonly objects = new Map<string, THREE.Object3D>();
  private measuringCanvas?: HTMLCanvasElement;

  constructor(private readonly createCanvas: CanvasFactory = () => document.createElement("canvas")) {}

  get stats() {
    return {
      geometries: this.geometries.size,
      materials: this.materials.size,
      labels: this.labels.size,
      objects: this.objects.size,
    };
  }

  geometryFor(type: NodeType): THREE.BufferGeometry {
    const cached = this.geometries.get(type);
    if (cached) return cached;
    const geometry = buildGeometry(type);
    this.geometries.set(type, geometry);
    return geometry;
  }

  private extraGeometry(key: string, build: () => THREE.BufferGeometry): THREE.BufferGeometry {
    const cached = this.geometries.get(key);
    if (cached) return cached;
    const geometry = build();
    this.geometries.set(key, geometry);
    return geometry;
  }

  materialFor(spec: MaterialSpec): THREE.Material {
    const type = spec.type ?? "process";
    const key = `${spec.color}:${spec.status}:${spec.selected ? "sel" : "norm"}:${spec.container ? "grp" : "solid"}:${type}`;
    const cached = this.materials.get(key);
    if (cached) return cached;

    const finish = FINISHES[type];
    const translucido = finish.transmission > 0 && !spec.container && spec.status !== "skipped";
    const material = new THREE.MeshPhysicalMaterial({
      color: spec.color,
      transparent: spec.container || spec.status === "skipped" || translucido,
      opacity: spec.container ? 0.12 : spec.status === "skipped" ? 0.35 : 1,
      wireframe: spec.container,
      metalness: spec.container ? finish.metalness : finish.metalness,
      roughness: finish.roughness,
      clearcoat: finish.clearcoat,
      clearcoatRoughness: finish.clearcoatRoughness,
      transmission: translucido ? finish.transmission : 0,
      thickness: translucido ? 26 : 0,
      ior: finish.ior,
      reflectivity: finish.reflectivity,
      envMapIntensity: 1.15,
      // El emisivo deja de bañar la superficie: solo marca estado y selección,
      // que es lo que hacía que todo pareciera plástico iluminado por dentro.
      emissive: new THREE.Color(spec.color),
      emissiveIntensity: spec.selected ? 0.45 : spec.status === "idle" ? 0.02 : 0.6,
      side: THREE.DoubleSide,
    });
    this.materials.set(key, material);
    return material;
  }

  private basicMaterial(key: string, build: () => THREE.Material): THREE.Material {
    const cached = this.materials.get(key);
    if (cached) return cached;
    const material = build();
    this.materials.set(key, material);
    return material;
  }

  labelMetricsFor(text: string, selected: boolean, escala = 1): LabelMetrics {
    const metricsKey = `${selected ? "sel" : "norm"}:${escala}:${text}`;
    const remembered = this.metrics.get(metricsKey);
    if (remembered) return remembered;

    const trimmed = text.slice(0, 48);
    if (!this.measuringCanvas) {
      this.measuringCanvas = this.createCanvas();
      // Un lienzo recién creado mide 300 × 150; reducirlo evita reservar
      // píxeles que nunca se dibujan.
      this.measuringCanvas.width = 1;
      this.measuringCanvas.height = 1;
    }
    const context = this.measuringCanvas.getContext("2d");
    let measured = trimmed.length * 17;
    if (context) {
      context.font = fontFor(selected);
      measured = context.measureText(trimmed).width;
    }
    const boxWidth = Math.min(LABEL_MAX_BOX, measured + LABEL_PADDING);
    const anchoBase = Math.ceil(boxWidth) + STROKE_MARGIN;
    // La escala multiplica los píxeles del lienzo pero no el tamaño del sprite:
    // el texto ocupa lo mismo en pantalla y se dibuja con más téxeles.
    const metrics: LabelMetrics = {
      text: trimmed,
      boxWidth: boxWidth * escala,
      canvasWidth: anchoBase * escala,
      canvasHeight: LABEL_HEIGHT * escala,
      spriteWidth: (SPRITE_REFERENCE_WIDTH * anchoBase) / LABEL_REFERENCE_WIDTH,
      spriteHeight: SPRITE_HEIGHT,
    };
    this.metrics.set(metricsKey, metrics);
    return metrics;
  }

  labelMaterialFor(text: string, selected: boolean, escala = 1): THREE.SpriteMaterial {
    const key = `${selected ? "sel" : "norm"}:${escala}:${text}`;
    const cached = this.labels.get(key);
    if (cached) return cached;

    const metrics = this.labelMetricsFor(text, selected, escala);
    const canvas = this.createCanvas();
    canvas.width = metrics.canvasWidth;
    canvas.height = metrics.canvasHeight;
    const context = canvas.getContext("2d");
    if (context) {
      context.clearRect(0, 0, canvas.width, canvas.height);
      context.font = fontFor(selected, escala);
      context.textAlign = "center";
      context.textBaseline = "middle";
      context.fillStyle = selected ? "rgba(25, 32, 63, .96)" : "rgba(8, 12, 24, .86)";
      context.strokeStyle = selected ? "rgba(130, 153, 255, .9)" : "rgba(118, 131, 169, .35)";
      context.lineWidth = (selected ? 4 : 2) * escala;
      context.beginPath();
      context.roundRect((canvas.width - metrics.boxWidth) / 2, 24 * escala, metrics.boxWidth, 74 * escala, 23 * escala);
      context.fill();
      context.stroke();
      context.fillStyle = selected ? "#ffffff" : "#d9deef";
      context.fillText(metrics.text, canvas.width / 2, 62 * escala);
    }
    // Se conservan los mipmaps del diseño original: sin ellos las etiquetas
    // lejanas aparecen dentadas, y eso sería cambiar lo que se ve.
    const texture = new THREE.CanvasTexture(canvas);
    texture.colorSpace = THREE.SRGBColorSpace;
    const material = new THREE.SpriteMaterial({ map: texture, transparent: true, depthTest: false });
    this.labels.set(key, material);
    return material;
  }

  private labelSprite(text: string, selected: boolean, escala = 1): THREE.Sprite {
    const metrics = this.labelMetricsFor(text, selected, escala);
    const sprite = new THREE.Sprite(this.labelMaterialFor(text, selected, escala));
    sprite.scale.set(metrics.spriteWidth, metrics.spriteHeight, 1);
    sprite.position.set(0, 28, 0);
    sprite.renderOrder = 5;
    return sprite;
  }

  /**
   * Devuelve el objeto del nodo. La clave incluye el identificador, así que cada
   * entrada pertenece a un único nodo y puede reutilizarse tal cual: el `clone()`
   * que hacía el editor duplicaba el trabajo sin aportar nada.
   */
  nodeObject(node: FlowNode, status: NodeRunStatus, selected: boolean): THREE.Object3D {
    const color = status === "idle"
      ? node.metadata?.color ?? NODE_PRESENTATION[node.type].color
      : STATUS_COLORS[status];
    const key = `${node.id}:${status}:${selected ? "selected" : "normal"}:${color}:${node.label}`;
    const cached = this.objects.get(key);
    if (cached) return cached;

    const container = node.type === "group";
    const group = new THREE.Group();
    const mesh = new THREE.Mesh(this.geometryFor(node.type), this.materialFor({ color, status, selected, container, type: node.type }));
    if (node.type === "decision") mesh.rotation.z = Math.PI / 4;
    group.add(mesh);
    // Referencias con nombre para que el nivel de detalle y la instanciación
    // puedan actuar sobre cada pieza sin recorrer el grafo de la escena.
    group.userData.body = mesh;
    group.userData.nodeId = node.id;

    if (node.type === "end") {
      const ring = new THREE.Mesh(
        this.extraGeometry("ring", () => new THREE.TorusGeometry(15.5, 1.2, 8, 32)),
        this.basicMaterial(`ring:${color}`, () => new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.65 })),
      );
      ring.rotation.x = Math.PI / 2;
      group.add(ring);
    }
    if (selected) {
      const halo = new THREE.Mesh(
        this.extraGeometry(container ? "halo-group" : "halo", () => new THREE.SphereGeometry(container ? 65 : 19, 18, 12)),
        this.basicMaterial("halo", () => new THREE.MeshBasicMaterial({ color: "#8299ff", transparent: true, opacity: 0.1, side: THREE.BackSide })),
      );
      group.add(halo);
    }
    // La etiqueta ya no se fabrica aquí: con miles de nodos eso son miles de
    // lienzos construidos antes de saber si alguno llegará a verse. La pide el
    // nivel de detalle cuando el nodo entra en distancia.
    group.userData.labelText = node.label;
    group.userData.labelSelected = selected;

    this.objects.set(key, group);
    return group;
  }

  /**
   * Crea la etiqueta de un nodo si todavía no existe y la cuelga de su grupo.
   * Es idempotente: llamarla de nuevo no vuelve a dibujar nada.
   */
  ensureLabel(node: FlowNode, selected: boolean): THREE.Sprite | undefined {
    for (const group of this.objects.values()) {
      if (group.userData.labelText === undefined) continue;
      if (group.userData.nodeId !== node.id) continue;
      return this.attachLabel(group, selected);
    }
    return undefined;
  }

  /** Cuelga la etiqueta de un grupo concreto, que es lo que usa el editor. */
  attachLabel(group: THREE.Object3D, selected?: boolean): THREE.Sprite | undefined {
    const existing = group.userData.label as THREE.Sprite | undefined;
    if (existing) return existing;
    const text = group.userData.labelText as string | undefined;
    if (text === undefined) return undefined;
    const sprite = this.labelSprite(text, selected ?? Boolean(group.userData.labelSelected));
    group.add(sprite);
    group.userData.label = sprite;
    group.userData.labelScale = 1;
    return sprite;
  }

  /**
   * Cambia la etiqueta de un nodo por una de más resolución. Se llama cuando la
   * cámara está lo bastante cerca como para que se notaría el escalonado.
   */
  upgradeLabel(group: THREE.Object3D, escala: number): THREE.Sprite | undefined {
    const text = group.userData.labelText as string | undefined;
    if (text === undefined) return undefined;
    if (group.userData.labelScale === escala) return group.userData.label as THREE.Sprite | undefined;

    const anterior = group.userData.label as THREE.Sprite | undefined;
    const selected = Boolean(group.userData.labelSelected);
    const sprite = this.labelSprite(text, selected, escala);
    if (anterior) group.remove(anterior);
    group.add(sprite);
    group.userData.label = sprite;
    group.userData.labelScale = escala;
    return sprite;
  }

  dispose(): void {
    for (const geometry of this.geometries.values()) geometry.dispose();
    for (const material of this.materials.values()) material.dispose();
    for (const material of this.labels.values()) {
      material.map?.dispose();
      material.dispose();
    }
    this.geometries.clear();
    this.materials.clear();
    this.labels.clear();
    this.metrics.clear();
    this.objects.clear();
    this.measuringCanvas = undefined;
  }
}

function buildGeometry(type: NodeType): THREE.BufferGeometry {
  switch (type) {
    case "trigger":
      return new THREE.SphereGeometry(11, 24, 16);
    case "process":
      return new THREE.BoxGeometry(20, 20, 20, 2, 2, 2);
    case "decision":
      return new THREE.OctahedronGeometry(14, 0);
    case "data":
      return new THREE.CylinderGeometry(11, 11, 22, 24);
    case "integration":
      return new THREE.CylinderGeometry(12, 12, 22, 6);
    case "delay":
      return new THREE.TorusGeometry(11, 3.7, 12, 32);
    case "end":
      return new THREE.SphereGeometry(12, 24, 16);
    case "group":
      return new THREE.BoxGeometry(112, 72, 48);
  }
}
