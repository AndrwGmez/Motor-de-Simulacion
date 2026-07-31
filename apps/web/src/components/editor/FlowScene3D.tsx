"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ForceGraph3D, { type ForceGraphMethods, type LinkObject, type NodeObject } from "react-force-graph-3d";
import * as THREE from "three";
import type { FlowDefinition, FlowEdge, FlowNode, NodeRunStatus } from "@/lib/flow-types";
import { NODE_PRESENTATION } from "@/lib/flow-types";

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

const STATUS_COLORS: Record<NodeRunStatus, string> = {
  idle: "#7f8ba8",
  queued: "#f4c261",
  running: "#69a3ff",
  success: "#37d6a0",
  failed: "#ff617d",
  skipped: "#596174",
  waiting: "#f49a59",
};

function makeLabel(text: string, selected: boolean) {
  const canvas = document.createElement("canvas");
  canvas.width = 640;
  canvas.height = 128;
  const context = canvas.getContext("2d")!;
  context.clearRect(0, 0, canvas.width, canvas.height);
  context.font = `${selected ? 650 : 520} 34px Inter, Arial, sans-serif`;
  context.textAlign = "center";
  context.textBaseline = "middle";
  const width = Math.min(590, context.measureText(text).width + 48);
  context.fillStyle = selected ? "rgba(25, 32, 63, .96)" : "rgba(8, 12, 24, .86)";
  context.strokeStyle = selected ? "rgba(130, 153, 255, .9)" : "rgba(118, 131, 169, .35)";
  context.lineWidth = selected ? 4 : 2;
  const x = (canvas.width - width) / 2;
  context.beginPath();
  context.roundRect(x, 24, width, 74, 23);
  context.fill();
  context.stroke();
  context.fillStyle = selected ? "#ffffff" : "#d9deef";
  context.fillText(text.slice(0, 48), canvas.width / 2, 62);
  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  const material = new THREE.SpriteMaterial({ map: texture, transparent: true, depthTest: false });
  const sprite = new THREE.Sprite(material);
  sprite.scale.set(90, 18, 1);
  sprite.position.set(0, 28, 0);
  sprite.renderOrder = 5;
  return sprite;
}

function geometryFor(node: GraphNode): THREE.BufferGeometry {
  switch (node.type) {
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

function disposeObject(object: THREE.Object3D) {
  object.traverse((child) => {
    if ("geometry" in child && child.geometry instanceof THREE.BufferGeometry) child.geometry.dispose();
    if ("material" in child) {
      const materials = Array.isArray(child.material) ? child.material : [child.material];
      for (const material of materials) {
        if (material instanceof THREE.Material) {
          if ("map" in material && material.map instanceof THREE.Texture) material.map.dispose();
          material.dispose();
        }
      }
    }
  });
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
  const objectCache = useRef(new Map<string, THREE.Object3D>());
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

  useEffect(() => () => {
    for (const object of objectCache.current.values()) disposeObject(object);
    objectCache.current.clear();
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

  const nodeObject = useCallback((nodeValue: object) => {
    const node = nodeValue as GraphNode;
    const status = (nodeStates[node.id] ?? "idle") as NodeRunStatus;
    const selected = node.id === selectedNodeId || node.id === connectionSourceId;
    const key = `${node.id}:${status}:${selected ? "selected" : "normal"}:${node.metadata.color ?? ""}`;
    const cached = objectCache.current.get(key);
    if (cached) return cached.clone();

    const group = new THREE.Group();
    const color = status === "idle" ? node.metadata.color ?? NODE_PRESENTATION[node.type].color : STATUS_COLORS[status];
    const isContainer = node.type === "group";
    const material = new THREE.MeshStandardMaterial({
      color,
      transparent: isContainer || status === "skipped",
      opacity: isContainer ? 0.12 : status === "skipped" ? 0.35 : 0.96,
      wireframe: isContainer,
      roughness: 0.34,
      metalness: 0.28,
      emissive: new THREE.Color(color),
      emissiveIntensity: selected ? 0.72 : status === "running" ? 0.9 : 0.15,
      side: THREE.DoubleSide,
    });
    const mesh = new THREE.Mesh(geometryFor(node), material);
    if (node.type === "decision") mesh.rotation.z = Math.PI / 4;
    group.add(mesh);

    if (node.type === "end") {
      const ring = new THREE.Mesh(
        new THREE.TorusGeometry(15.5, 1.2, 8, 32),
        new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.65 }),
      );
      ring.rotation.x = Math.PI / 2;
      group.add(ring);
    }
    if (selected) {
      const halo = new THREE.Mesh(
        new THREE.SphereGeometry(node.type === "group" ? 65 : 19, 18, 12),
        new THREE.MeshBasicMaterial({ color: "#8299ff", transparent: true, opacity: 0.1, side: THREE.BackSide }),
      );
      group.add(halo);
    }
    group.add(makeLabel(node.label, selected));
    objectCache.current.set(key, group);
    return group.clone();
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
