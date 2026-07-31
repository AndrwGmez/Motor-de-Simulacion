"use client";

import dynamic from "next/dynamic";
import type { FlowDefinition } from "@flowverse/core";

const FlowScene3D = dynamic(() => import("@flowverse/viewer").then((module) => module.FlowScene3D), {
  ssr: false,
  loading: () => (
    <div className="scene-loading" role="status">
      <span className="scene-loader" />
      <strong>Construyendo universo 3D…</strong>
      <small>Preparando geometrías y física</small>
    </div>
  ),
});

interface FlowSceneProps {
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

export function FlowScene(props: FlowSceneProps) {
  return <FlowScene3D {...props} />;
}
