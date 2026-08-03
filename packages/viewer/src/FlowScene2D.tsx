"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { NODE_PRESENTATION, type FlowDefinition, type NodeRunStatus } from "@flowverse/core";
import { encuadrar, formaDeNodo, rutaDeArista, type Punto } from "./plane";

/**
 * Vista bidimensional del mismo flujo.
 *
 * Recibe exactamente las mismas propiedades que la escena 3D, así que es un
 * intercambio directo. Se dibuja en SVG por tres motivos concretos: es legible
 * con cientos de nodos —donde el 3D obliga a acercarse—, se imprime y se
 * exporta, y al ser DOM lo lee un lector de pantalla, cosa que un lienzo WebGL
 * no permite.
 */

const ESTADOS: Record<NodeRunStatus, string> = {
  idle: "#7f8ba8", queued: "#f4c261", running: "#69a3ff",
  success: "#37d6a0", failed: "#ff617d", skipped: "#596174", waiting: "#f49a59",
};

interface FlowScene2DProps {
  flow: FlowDefinition;
  selectedNodeId?: string;
  selectedEdgeId?: string;
  activeEdgeId?: string;
  nodeStates: Record<string, string>;
  onNodeClick: (nodeId: string, aditivo?: boolean) => void;
  onEdgeClick: (edgeId: string) => void;
  onBackgroundClick: () => void;
}

function Figura({ tipo, color, radio }: { tipo: ReturnType<typeof formaDeNodo>["tipo"]; color: string; radio: number }) {
  const comun = { fill: color, stroke: "rgba(255,255,255,.28)", strokeWidth: 1.2 };
  switch (tipo) {
    case "circulo": return <circle r={radio} {...comun} />;
    case "rombo": return <rect x={-radio} y={-radio} width={radio * 2} height={radio * 2} transform="rotate(45)" rx={2} {...comun} />;
    case "cilindro": return <rect x={-radio} y={-radio * 0.8} width={radio * 2} height={radio * 1.6} rx={radio * 0.7} {...comun} />;
    case "hexagono": {
      const p = Array.from({ length: 6 }, (_, i) => {
        const a = (Math.PI / 3) * i - Math.PI / 6;
        return `${(radio * Math.cos(a)).toFixed(1)},${(radio * Math.sin(a)).toFixed(1)}`;
      }).join(" ");
      return <polygon points={p} {...comun} />;
    }
    case "anillo": return <circle r={radio} fill="none" stroke={color} strokeWidth={3.4} />;
    case "contenedor": return <rect x={-radio * 1.6} y={-radio} width={radio * 3.2} height={radio * 2} rx={6} fill="none" stroke={color} strokeWidth={1} strokeDasharray="4 4" />;
    default: return <rect x={-radio} y={-radio} width={radio * 2} height={radio * 2} rx={3} {...comun} />;
  }
}

export function FlowScene2D({
  flow, selectedNodeId, selectedEdgeId, activeEdgeId, nodeStates,
  onNodeClick, onEdgeClick, onBackgroundClick,
}: FlowScene2DProps) {
  const contenedor = useRef<HTMLDivElement>(null);
  const [lienzo, setLienzo] = useState({ width: 960, height: 640 });

  useEffect(() => {
    const elemento = contenedor.current;
    if (!elemento) return;
    const observador = new ResizeObserver(([entrada]) => {
      setLienzo({
        width: Math.max(320, Math.floor(entrada.contentRect.width)),
        height: Math.max(320, Math.floor(entrada.contentRect.height)),
      });
    });
    observador.observe(elemento);
    return () => observador.disconnect();
  }, []);

  const { posiciones, viewBox } = useMemo(
    () => encuadrar(flow.nodes, lienzo, 56),
    [flow.nodes, lienzo],
  );

  // Las aristas repetidas entre el mismo par se separan para no solaparse.
  const repeticiones = new Map<string, number>();

  return (
    <div ref={contenedor} className="flow-scene flow-scene-2d" data-testid="flow-scene-2d" aria-label="Vista bidimensional del flujo">
      <svg viewBox={viewBox} width="100%" height="100%" role="img" onClick={onBackgroundClick}>
        <defs>
          <marker id="punta" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M 0 0 L 8 4 L 0 8 z" fill="#7187ca" />
          </marker>
        </defs>

        {flow.edges.map((arista) => {
          const desde = posiciones.get(typeof arista.source === "string" ? arista.source : "");
          const hasta = posiciones.get(typeof arista.target === "string" ? arista.target : "");
          if (!desde || !hasta) return null;
          const clave = `${arista.source}->${arista.target}`;
          const desvio = repeticiones.get(clave) ?? 0;
          repeticiones.set(clave, desvio + 1);
          const activa = arista.id === activeEdgeId;
          const elegida = arista.id === selectedEdgeId;
          return (
            <path
              key={arista.id}
              d={rutaDeArista(desde as Punto, hasta as Punto, desvio)}
              fill="none"
              stroke={activa ? "#52ddff" : elegida ? "#a5b4ff" : "rgba(110,132,190,.5)"}
              strokeWidth={activa ? 2.4 : elegida ? 2 : 1}
              markerEnd="url(#punta)"
              style={{ cursor: "pointer" }}
              onClick={(evento) => { evento.stopPropagation(); onEdgeClick(arista.id); }}
            >
              <title>{arista.label || clave}</title>
            </path>
          );
        })}

        {flow.nodes.map((nodo) => {
          const punto = posiciones.get(nodo.id);
          if (!punto) return null;
          const estado = (nodeStates[nodo.id] ?? "idle") as NodeRunStatus;
          const color = estado === "idle"
            ? nodo.metadata?.color ?? NODE_PRESENTATION[nodo.type].color
            : ESTADOS[estado];
          const forma = formaDeNodo(nodo.type);
          const elegido = nodo.id === selectedNodeId;
          return (
            <g
              key={nodo.id}
              transform={`translate(${punto.x} ${punto.y})`}
              style={{ cursor: "pointer" }}
              onClick={(evento) => { evento.stopPropagation(); onNodeClick(nodo.id, evento.ctrlKey || evento.metaKey); }}
            >
              {elegido && <circle r={forma.radio + 7} fill="none" stroke="#8299ff" strokeWidth={1.6} />}
              <Figura tipo={forma.tipo} color={color} radio={forma.radio} />
              <text y={forma.radio + 14} textAnchor="middle" fontSize={10.5} fill="#c7cee4" pointerEvents="none">
                {nodo.label.length > 26 ? `${nodo.label.slice(0, 25)}…` : nodo.label}
              </text>
              <title>{`${NODE_PRESENTATION[nodo.type].label}: ${nodo.label}`}</title>
            </g>
          );
        })}
      </svg>
    </div>
  );
}
