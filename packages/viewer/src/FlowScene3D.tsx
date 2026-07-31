"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ForceGraph3D, { type ForceGraphMethods, type LinkObject, type NodeObject } from "react-force-graph-3d";
import type * as THREE from "three";
import type { FlowDefinition, FlowEdge, FlowNode, NodeRunStatus } from "@flowverse/core";
import { NODE_PRESENTATION } from "@flowverse/core";
import { SceneResources } from "./scene-resources";
import { DEFAULT_PAN, PAN_KEYS, applyCameraFeel, applyStudioLighting, easePan, ignoresKeyboard, panCamera, panDirection, type CameraControls } from "./scene-camera";
import { BatchedLinks, InstancedBodies, applyLevelOfDetail, DEFAULT_LOD, type BatchedLink, type InstancedTarget, type LodLink } from "./scene-performance";

interface GraphNode extends FlowNode {
  x?: number;
  y?: number;
  z?: number;
  fx?: number;
  fy?: number;
  fz?: number;
}

interface GraphEdge extends Omit<FlowEdge, "source" | "target"> {
  source: string | GraphNode;
  target: string | GraphNode;
}

interface FlowScene3DProps {
  flow: FlowDefinition;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  activeEdgeId?: string;
  nodeStates: Record<string, string>;
  readOnly?: boolean;
  connectionSourceId?: string;
  fitRequest?: number;
  onNodeClick: (nodeId: string) => void;
  onEdgeClick: (edgeId: string) => void;
  onNodeMove: (nodeId: string, position: { x: number; y: number; z: number }) => void;
  onBackgroundClick: () => void;
}

export function FlowScene3D({
  flow,
  selectedNodeId,
  selectedEdgeId,
  activeEdgeId,
  nodeStates,
  readOnly,
  connectionSourceId,
  fitRequest,
  onNodeClick,
  onEdgeClick,
  onNodeMove,
  onBackgroundClick,
}: FlowScene3DProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const graphRef = useRef<
    ForceGraphMethods<NodeObject<GraphNode>, LinkObject<GraphNode, GraphEdge>> | undefined
  >(undefined);
  const [size, setSize] = useState({ width: 960, height: 640 });
  const resources = useRef<SceneResources>(undefined as unknown as SceneResources);
  if (!resources.current) resources.current = new SceneResources();
  const bodies = useRef<InstancedBodies>(undefined as unknown as InstancedBodies);
  if (!bodies.current) bodies.current = new InstancedBodies(resources.current);
  const rebuildBatches = useRef(true);
  const linkBatch = useRef<BatchedLinks>(undefined as unknown as BatchedLinks);
  if (!linkBatch.current) linkBatch.current = new BatchedLinks();
  const pressedKeys = useRef(new Set<string>());
  const panVelocity = useRef({ x: 0, y: 0 });
  const didInitialFit = useRef(false);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return;
    const observer = new ResizeObserver(([entry]) => {
      setSize({
        width: Math.max(320, Math.floor(entry.contentRect.width)),
        height: Math.max(320, Math.floor(entry.contentRect.height)),
      });
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const currentResources = resources.current;
    const currentBodies = bodies.current;
    const currentGraph = graphRef;
    const currentLinks = linkBatch.current;
    return () => {
      const scene = currentGraph.current?.scene();
      if (scene) {
        currentBodies.dispose(scene);
        currentLinks.dispose(scene);
      }
      currentResources.dispose();
    };
  }, []);

  /**
   * Flechas del teclado: desplazan el encuadre arriba, abajo y de lado a lado.
   * Solo se registran las teclas; el movimiento lo integra el bucle por cuadro,
   * que es lo que hace que arranque y frene de forma progresiva.
   */
  useEffect(() => {
    const pressed = pressedKeys.current;
    const down = (event: KeyboardEvent) => {
      if (!PAN_KEYS.includes(event.key)) return;
      if (ignoresKeyboard(event.target as Element | null)) return;
      event.preventDefault();
      pressed.add(event.key);
    };
    const up = (event: KeyboardEvent) => pressed.delete(event.key);
    const reset = () => pressed.clear();
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    window.addEventListener("blur", reset);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
      window.removeEventListener("blur", reset);
      pressed.clear();
    };
  }, []);

  // Inercia de la cámara: los controles de órbita frenan de forma progresiva en
  // lugar de detenerse en seco. La librería ya llama a `controls.update()` en su
  // bucle, así que basta con activarlo cuando el lienzo está montado.
  useEffect(() => {
    let cancelled = false;
    const apply = () => {
      if (cancelled) return;
      const graph = graphRef.current;
      const controls = graph?.controls() as CameraControls | undefined;
      if (controls && "enableDamping" in controls) {
        applyCameraFeel(controls);
        const scene = graph?.scene();
        const renderer = graph?.renderer();
        if (scene && renderer) applyStudioLighting({ scene, renderer });
      } else {
        window.setTimeout(apply, 60);
      }
    };
    apply();
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!fitRequest) return;
    const timer = window.setTimeout(() => graphRef.current?.zoomToFit(650, 80), 30);
    return () => window.clearTimeout(timer);
  }, [fitRequest]);

  useEffect(() => {
    if (flow.nodes.length === 0) return;
    const timer = window.setTimeout(() => {
      graphRef.current?.zoomToFit(500, 72);
      didInitialFit.current = true;
    }, 250);
    return () => window.clearTimeout(timer);
  }, [flow.edges.length, flow.layout.mode, flow.nodes.length, size.height, size.width]);

  const data = useMemo(() => {
    const fixedLayout = flow.layout.mode !== "force";
    const nodes: GraphNode[] = flow.nodes.map((node) => ({
      ...node,
      position: { ...node.position },
      x: node.position.x,
      y: node.position.y,
      z: node.position.z,
      fx: fixedLayout || node.locked ? node.position.x : undefined,
      fy: fixedLayout || node.locked ? node.position.y : undefined,
      fz: fixedLayout || node.locked ? node.position.z : undefined,
    }));
    const links: GraphEdge[] = flow.edges.map((edge) => ({ ...edge }));
    return { nodes, links };
  }, [flow]);

  /**
   * Bucle de rendimiento. Cada cuadro refresca las matrices de las instancias,
   * que es barato; el nivel de detalle se recalcula cada pocos cuadros porque
   * depende de la cámara y no necesita precisión absoluta.
   */
  useEffect(() => {
    let handle = 0;
    let frame = 0;
    const step = () => {
      handle = window.requestAnimationFrame(step);
      const graph = graphRef.current;
      if (!graph) return;
      const scene = graph.scene();
      const camera = graph.camera();
      if (!scene || !camera) return;

      if (rebuildBatches.current) {
        const cuerpos = bodies.current.sync(
          scene,
          data.nodes as unknown as InstancedTarget[],
          (node) => (nodeStates[node.id] ?? "idle") as NodeRunStatus,
          selectedNodeId ?? connectionSourceId,
        );
        const agrupadas = linkBatch.current.sync(scene, data.links as unknown as BatchedLink[], {
          selectedId: selectedEdgeId,
          activeId: activeEdgeId,
        });
        // Si la librería aún no ha creado las conexiones, se reintenta.
        // Se reintenta mientras cualquiera de los dos lotes no esté listo: antes
        // solo se miraba el de conexiones, y si los nodos aún no tenían objeto
        // se quedaban ocultos y sin lote que los dibujara.
        rebuildBatches.current = !(cuerpos && agrupadas);
      } else {
        bodies.current.updatePositions();
        linkBatch.current.updatePositions();
      }

      const objetivo = panDirection(pressedKeys.current);
      panVelocity.current = easePan(panVelocity.current, objetivo, DEFAULT_PAN.smoothing);
      if (Math.hypot(panVelocity.current.x, panVelocity.current.y) > 0.0005) {
        const controls = graph.controls() as unknown as { target?: THREE.Vector3 } | undefined;
        if (controls?.target) panCamera(camera, controls.target, panVelocity.current, DEFAULT_PAN);
      }

      frame += 1;
      if (frame % 4 === 0) {
        applyLevelOfDetail(
          camera.position,
          data.nodes,
          data.links as unknown as LodLink[],
          DEFAULT_LOD,
          selectedNodeId ?? connectionSourceId,
          (node) => {
            const objeto = (node as { __threeObj?: THREE.Object3D }).__threeObj;
            if (objeto) resources.current.attachLabel(objeto);
          },
        );
      }
    };
    handle = window.requestAnimationFrame(step);
    return () => window.cancelAnimationFrame(handle);
  }, [activeEdgeId, connectionSourceId, data, nodeStates, selectedEdgeId, selectedNodeId]);

  // Los lotes se rehacen cuando cambia el grafo o el aspecto de algún nodo.
  useEffect(() => {
    rebuildBatches.current = true;
  }, [activeEdgeId, connectionSourceId, data, nodeStates, selectedEdgeId, selectedNodeId]);

  const nodeObject = useCallback((nodeValue: object) => {
    const node = nodeValue as GraphNode;
    const status = (nodeStates[node.id] ?? "idle") as NodeRunStatus;
    const selected = node.id === selectedNodeId || node.id === connectionSourceId;
    return resources.current.nodeObject(node, status, selected);
  }, [connectionSourceId, nodeStates, selectedNodeId]);

  const focusNode = useCallback((node: GraphNode) => {
    const x = node.x ?? node.position.x;
    const y = node.y ?? node.position.y;
    const z = node.z ?? node.position.z;
    graphRef.current?.cameraPosition({ x, y: y + 38, z: z + 125 }, { x, y, z }, 650);
  }, []);

  return (
    <div
      ref={containerRef}
      className={`flow-scene ${connectionSourceId ? "is-connecting" : ""}`}
      aria-label="Universo tridimensional del flujo"
      data-testid="flow-scene"
    >
      <ForceGraph3D
        ref={graphRef}
        width={size.width}
        height={size.height}
        graphData={data}
        backgroundColor="#050710"
        showNavInfo={false}
        nodeId="id"
        nodeLabel={(node) => {
          const item = node as GraphNode;
          return `${NODE_PRESENTATION[item.type].label}: ${item.label}${item.locked ? " · bloqueado" : ""}`;
        }}
        nodeThreeObject={nodeObject}
        nodeThreeObjectExtend={false}
        linkLabel={(link) => {
          const edge = link as GraphEdge;
          return edge.label || `${typeof edge.source === "string" ? edge.source : edge.source.id} → ${typeof edge.target === "string" ? edge.target : edge.target.id}`;
        }}
        linkColor={(link) => {
          const edge = link as GraphEdge;
          if (edge.id === activeEdgeId) return "#52ddff";
          if (edge.id === selectedEdgeId) return "#a5b4ff";
          return "rgba(110, 132, 190, .52)";
        }}
        linkOpacity={0.72}
        linkWidth={(link) => {
          const edge = link as GraphEdge;
          return edge.id === activeEdgeId ? 3 : edge.id === selectedEdgeId ? 2.2 : 1.1;
        }}
        linkDirectionalArrowLength={5}
        linkDirectionalArrowRelPos={0.94}
        linkDirectionalArrowColor={(link) => (link as GraphEdge).id === activeEdgeId ? "#52ddff" : "#7187ca"}
        linkDirectionalParticles={(link) => (link as GraphEdge).id === activeEdgeId ? 5 : 0}
        linkDirectionalParticleSpeed={0.012}
        linkDirectionalParticleWidth={2.8}
        linkDirectionalParticleColor={() => "#7ce8ff"}
        enableNodeDrag={!readOnly}
        enableNavigationControls
        controlType="orbit"
        d3AlphaDecay={flow.layout.mode === "force" ? 0.035 : 0.12}
        d3VelocityDecay={0.34}
        warmupTicks={flow.layout.mode === "force" ? 80 : 0}
        cooldownTicks={flow.layout.mode === "force" ? 240 : 0}
        onNodeClick={(value, event) => {
          const node = value as GraphNode;
          if ((event as MouseEvent).detail >= 2) focusNode(node);
          else onNodeClick(node.id);
        }}
        onNodeRightClick={(value) => onNodeClick((value as GraphNode).id)}
        onNodeDragEnd={(value) => {
          const node = value as GraphNode;
          if (readOnly || node.locked) return;
          onNodeMove(node.id, {
            x: Math.round(node.x ?? node.position.x),
            y: Math.round(node.y ?? node.position.y),
            z: Math.round(node.z ?? node.position.z),
          });
        }}
        onNodeHover={(value) => {
          if (containerRef.current) containerRef.current.style.cursor = value ? (connectionSourceId ? "crosshair" : "pointer") : "grab";
        }}
        onLinkClick={(value) => onEdgeClick((value as GraphEdge).id)}
        onBackgroundClick={onBackgroundClick}
        onEngineStop={() => {
          if (!didInitialFit.current) {
            didInitialFit.current = true;
            graphRef.current?.zoomToFit(500, 72);
          }
        }}
      />
      <div className="scene-hint" aria-hidden="true">
        <span>Arrastrar</span><i /> <span>Rotar</span><i /> <span>Zoom</span>
      </div>
    </div>
  );
}
