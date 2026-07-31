"use client";

import { create } from "zustand";
import { DEMO_DOCUMENT } from "@/lib/demo-flow";
import { applyLayout } from "@/lib/layouts";
import {
  createNode,
  type EditableFlow,
  type FlowDefinition,
  type FlowEdge,
  type FlowNode,
  type LayoutMode,
  type NodeRunStatus,
  type NodeType,
  type Point3D,
  type RunEvent,
  type RunStatus,
  type RunSummary,
  type SimulationPlan,
  type ValidationIssue,
} from "@/lib/flow-types";
import { validateFlow } from "@/lib/validation";

type SaveStatus = "saved" | "dirty" | "saving" | "error" | "conflict";

interface FlowState {
  document: EditableFlow;
  past: FlowDefinition[];
  future: FlowDefinition[];
  selectedNodeId?: string;
  selectedEdgeId?: string;
  validationIssues: ValidationIssue[];
  saveStatus: SaveStatus;
  saveSource: "api" | "local";
  lastSavedAt?: string;
  nodeStates: Record<string, NodeRunStatus>;
  activeEdgeId?: string;
  runStatus: RunStatus;
  runSource: "local" | "api";
  remoteRunId?: string;
  streamStatus: "idle" | "connected" | "reconnecting" | "closed";
  plannedEvents: RunEvent[];
  eventCursor: number;
  visibleEvents: RunEvent[];
  activePlan?: SimulationPlan;
  runHistory: RunSummary[];
  speed: number;

  loadDocument: (document: EditableFlow) => void;
  replaceDefinition: (definition: FlowDefinition) => void;
  updateFlowMeta: (changes: Pick<Partial<FlowDefinition>, "name" | "description">) => void;
  addNode: (type: NodeType) => string;
  updateNode: (nodeId: string, changes: Partial<FlowNode>) => void;
  moveNode: (nodeId: string, position: Point3D) => void;
  toggleNodeLock: (nodeId: string) => void;
  duplicateSelected: () => void;
  deleteSelected: () => void;
  addEdge: (source: string, target: string) => string | undefined;
  updateEdge: (edgeId: string, changes: Partial<FlowEdge>) => void;
  selectNode: (nodeId?: string) => void;
  selectEdge: (edgeId?: string) => void;
  clearSelection: () => void;
  changeLayout: (mode: LayoutMode) => void;
  undo: () => void;
  redo: () => void;
  markSaving: () => void;
  markSaved: (revision: number, etag: string, source: "api" | "local") => void;
  markSaveError: (conflict?: boolean) => void;
  markPublished: (versionId: string, versionNumber: number, etag?: string) => void;
  setValidationIssues: (issues: ValidationIssue[]) => void;
  startSimulation: (plan: SimulationPlan) => void;
  startRemoteSimulation: (runId: string) => void;
  ingestRemoteEvent: (event: RunEvent) => void;
  setStreamStatus: (status: FlowState["streamStatus"]) => void;
  applyNextEvent: () => void;
  pauseSimulation: () => void;
  resumeSimulation: () => void;
  cancelSimulation: () => void;
  resetSimulation: () => void;
  setSpeed: (speed: number) => void;
}

const clone = <T,>(value: T): T => structuredClone(value);

function initialNodeStates(flow: FlowDefinition): Record<string, NodeRunStatus> {
  return Object.fromEntries(flow.nodes.filter((node) => node.type !== "group").map((node) => [node.id, "idle"]));
}

function changedState(state: FlowState, definition: FlowDefinition): Partial<FlowState> {
  return {
    document: {
      ...state.document,
      draftMatchesPublished: false,
      updatedAt: new Date().toISOString(),
      definition,
    },
    past: [...state.past, clone(state.document.definition)].slice(-100),
    future: [],
    validationIssues: validateFlow(definition),
    saveStatus: "dirty",
  };
}

const defaultHistory: RunSummary[] = [
  {
    id: "run-demo-3",
    flowVersionId: "pedidos-v3",
    createdAt: "2026-07-30T19:42:00.000Z",
    completedAt: "2026-07-30T19:42:04.180Z",
    status: "completed",
    durationMs: 4_180,
    visitedNodeIds: ["start", "validate-payment", "payment-approved", "reserve-inventory", "inventory-available", "prepare", "ship", "confirm", "completed"],
    eventCount: 31,
  },
  {
    id: "run-demo-2",
    flowVersionId: "pedidos-v3",
    createdAt: "2026-07-30T18:06:00.000Z",
    completedAt: "2026-07-30T18:06:02.630Z",
    status: "failed",
    durationMs: 2_630,
    visitedNodeIds: ["start", "validate-payment", "payment-approved", "refund", "cancelled"],
    eventCount: 18,
  },
];

export const useFlowStore = create<FlowState>((set, get) => ({
  document: clone(DEMO_DOCUMENT),
  past: [],
  future: [],
  validationIssues: validateFlow(DEMO_DOCUMENT.definition),
  saveStatus: "saved",
  saveSource: "local",
  nodeStates: initialNodeStates(DEMO_DOCUMENT.definition),
  runStatus: "idle",
  runSource: "local",
  streamStatus: "idle",
  plannedEvents: [],
  eventCursor: 0,
  visibleEvents: [],
  runHistory: defaultHistory,
  speed: 1,

  loadDocument: (document) =>
    set({
      document: clone(document),
      past: [],
      future: [],
      selectedNodeId: undefined,
      selectedEdgeId: undefined,
      validationIssues: validateFlow(document.definition),
      saveStatus: "saved",
      nodeStates: initialNodeStates(document.definition),
      runStatus: "idle",
      runSource: "local",
      remoteRunId: undefined,
      streamStatus: "idle",
      plannedEvents: [],
      eventCursor: 0,
      visibleEvents: [],
      activePlan: undefined,
      activeEdgeId: undefined,
      runHistory: document.runHistory
        ? clone(document.runHistory)
        : document.flowId === "pedidos"
          ? defaultHistory
          : [],
    }),

  replaceDefinition: (definition) =>
    set((state) => ({
      ...changedState(state, clone(definition)),
      selectedNodeId: undefined,
      selectedEdgeId: undefined,
      nodeStates: initialNodeStates(definition),
      runStatus: "idle",
      runSource: "local",
      remoteRunId: undefined,
      streamStatus: "idle",
      plannedEvents: [],
      eventCursor: 0,
      visibleEvents: [],
      activePlan: undefined,
      activeEdgeId: undefined,
    })),

  updateFlowMeta: (changes) =>
    set((state) => changedState(state, { ...state.document.definition, ...changes })),

  addNode: (type) => {
    const state = get();
    const node = createNode(type, state.document.definition.nodes.length + 1, {
      x: (Math.random() - 0.5) * 180,
      y: (Math.random() - 0.5) * 160,
      z: (Math.random() - 0.5) * 80,
    });
    set({
      ...changedState(state, {
        ...state.document.definition,
        nodes: [...state.document.definition.nodes, node],
      }),
      selectedNodeId: node.id,
      selectedEdgeId: undefined,
    });
    return node.id;
  },

  updateNode: (nodeId, changes) =>
    set((state) =>
      changedState(state, {
        ...state.document.definition,
        nodes: state.document.definition.nodes.map((node) => node.id === nodeId ? { ...node, ...changes } : node),
      }),
    ),

  moveNode: (nodeId, position) =>
    set((state) =>
      changedState(state, {
        ...state.document.definition,
        nodes: state.document.definition.nodes.map((node) => node.id === nodeId ? { ...node, position } : node),
      }),
    ),

  toggleNodeLock: (nodeId) =>
    set((state) =>
      changedState(state, {
        ...state.document.definition,
        nodes: state.document.definition.nodes.map((node) => node.id === nodeId ? { ...node, locked: !node.locked } : node),
      }),
    ),

  duplicateSelected: () =>
    set((state) => {
      const selected = state.document.definition.nodes.find((node) => node.id === state.selectedNodeId);
      if (!selected) return {};
      const id = `${selected.type}-${Date.now().toString(36)}`;
      const copy: FlowNode = {
        ...clone(selected),
        id,
        label: `${selected.label} (copia)`,
        position: { x: selected.position.x + 38, y: selected.position.y + 38, z: selected.position.z + 12 },
        locked: false,
      };
      return {
        ...changedState(state, { ...state.document.definition, nodes: [...state.document.definition.nodes, copy] }),
        selectedNodeId: id,
      };
    }),

  deleteSelected: () =>
    set((state) => {
      if (state.selectedNodeId) {
        const id = state.selectedNodeId;
        return {
          ...changedState(state, {
            ...state.document.definition,
            nodes: state.document.definition.nodes.filter((node) => node.id !== id),
            edges: state.document.definition.edges.filter((edge) => edge.source !== id && edge.target !== id),
          }),
          selectedNodeId: undefined,
          selectedEdgeId: undefined,
        };
      }
      if (state.selectedEdgeId) {
        return {
          ...changedState(state, {
            ...state.document.definition,
            edges: state.document.definition.edges.filter((edge) => edge.id !== state.selectedEdgeId),
          }),
          selectedEdgeId: undefined,
        };
      }
      return {};
    }),

  addEdge: (sourceId, targetId) => {
    const state = get();
    const source = state.document.definition.nodes.find((node) => node.id === sourceId);
    const target = state.document.definition.nodes.find((node) => node.id === targetId);
    if (!source || !target || source.type === "group" || target.type === "group" || source.type === "end" || target.type === "trigger") {
      return undefined;
    }
    const outgoing = state.document.definition.edges.filter((edge) => edge.source === sourceId);
    const isDecision = source.type === "decision";
    const edge: FlowEdge = {
      id: `edge-${Date.now().toString(36)}`,
      source: sourceId,
      target: targetId,
      sourcePort: source.outputs[0]?.id ?? "output",
      targetPort: target.inputs[0]?.id ?? "input",
      label: isDecision ? (outgoing.length === 0 ? "Predeterminado" : `Condición ${outgoing.length}`) : "",
      priority: outgoing.length + 1,
      isDefault: isDecision && outgoing.length === 0,
      condition: isDecision && outgoing.length > 0
        ? { field: "/condition", operator: "equals", value: true }
        : undefined,
    };
    set({
      ...changedState(state, {
        ...state.document.definition,
        edges: [...state.document.definition.edges, edge],
      }),
      selectedNodeId: undefined,
      selectedEdgeId: edge.id,
    });
    return edge.id;
  },

  updateEdge: (edgeId, changes) =>
    set((state) =>
      changedState(state, {
        ...state.document.definition,
        edges: state.document.definition.edges.map((edge) => edge.id === edgeId ? { ...edge, ...changes } : edge),
      }),
    ),

  selectNode: (selectedNodeId) => set({ selectedNodeId, selectedEdgeId: undefined }),
  selectEdge: (selectedEdgeId) => set({ selectedEdgeId, selectedNodeId: undefined }),
  clearSelection: () => set({ selectedNodeId: undefined, selectedEdgeId: undefined }),

  changeLayout: (mode) =>
    set((state) => changedState(
      state,
      applyLayout(
        state.document.definition,
        mode,
        state.activePlan?.summary.visitedNodeIds ?? [],
      ),
    )),

  undo: () =>
    set((state) => {
      const previous = state.past.at(-1);
      if (!previous) return {};
      return {
        document: {
          ...state.document,
          definition: clone(previous),
          draftMatchesPublished: false,
          updatedAt: new Date().toISOString(),
        },
        past: state.past.slice(0, -1),
        future: [clone(state.document.definition), ...state.future].slice(0, 100),
        validationIssues: validateFlow(previous),
        saveStatus: "dirty",
        selectedNodeId: undefined,
        selectedEdgeId: undefined,
      };
    }),

  redo: () =>
    set((state) => {
      const next = state.future[0];
      if (!next) return {};
      return {
        document: {
          ...state.document,
          definition: clone(next),
          draftMatchesPublished: false,
          updatedAt: new Date().toISOString(),
        },
        past: [...state.past, clone(state.document.definition)].slice(-100),
        future: state.future.slice(1),
        validationIssues: validateFlow(next),
        saveStatus: "dirty",
        selectedNodeId: undefined,
        selectedEdgeId: undefined,
      };
    }),

  markSaving: () => set({ saveStatus: "saving" }),
  markSaved: (revision, etag, saveSource) =>
    set((state) => ({
      document: { ...state.document, revision, etag, updatedAt: new Date().toISOString() },
      saveStatus: "saved",
      saveSource,
      lastSavedAt: new Date().toISOString(),
    })),
  markSaveError: (conflict = false) => set({ saveStatus: conflict ? "conflict" : "error" }),
  markPublished: (publishedVersionId, publishedVersionNumber, etag) =>
    set((state) => ({
      document: {
        ...state.document,
        publishedVersionId,
        publishedVersionNumber,
        draftMatchesPublished: true,
        etag: etag ?? state.document.etag,
        updatedAt: new Date().toISOString(),
      },
      saveStatus: "saved",
    })),
  setValidationIssues: (validationIssues) => set({ validationIssues }),

  startSimulation: (plan) =>
    set((state) => ({
      activePlan: plan,
      plannedEvents: plan.events,
      eventCursor: 0,
      visibleEvents: [],
      nodeStates: initialNodeStates(state.document.definition),
      activeEdgeId: undefined,
      runStatus: "running",
      runSource: "local",
      remoteRunId: undefined,
      streamStatus: "idle",
    })),

  startRemoteSimulation: (remoteRunId) =>
    set((state) => ({
      activePlan: undefined,
      plannedEvents: [],
      eventCursor: 0,
      visibleEvents: [],
      nodeStates: initialNodeStates(state.document.definition),
      activeEdgeId: undefined,
      runStatus: "running",
      runSource: "api",
      remoteRunId,
      streamStatus: "reconnecting",
    })),

  ingestRemoteEvent: (event) =>
    set((state) => {
      if (state.visibleEvents.some((candidate) => candidate.sequence === event.sequence)) return {};
      const nextNodeStates = { ...state.nodeStates };
      let nextStatus = state.runStatus;
      let activeEdgeId = state.activeEdgeId;
      if (event.type === "node.queued" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "queued";
      if (event.type === "node.started" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "running";
      if (event.type === "node.waiting" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "waiting";
      if (event.type === "node.completed" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "success";
      if (event.type === "node.failed" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "failed";
      if (event.type === "node.skipped" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "skipped";
      if (event.type === "edge.traversed") activeEdgeId = event.payload.edgeId;
      if (event.type === "run.paused") nextStatus = "paused";
      if (event.type === "run.resumed") nextStatus = "running";
      if (event.type === "run.completed") nextStatus = "completed";
      if (event.type === "run.failed" || event.type === "run.limit_exceeded" || event.type === "run.interrupted") nextStatus = "failed";
      if (event.type === "run.cancelled") nextStatus = "cancelled";
      const terminal = ["run.completed", "run.failed", "run.limit_exceeded", "run.cancelled", "run.interrupted"].includes(event.type);
      const summaryStatus: RunSummary["status"] =
        event.type === "run.completed" ? "completed" : event.type === "run.cancelled" ? "cancelled" : "failed";
      const summary: RunSummary | undefined = terminal && state.remoteRunId
        ? {
            id: state.remoteRunId,
            flowVersionId: state.document.versionId,
            createdAt: state.visibleEvents[0]?.occurredAt ?? event.occurredAt,
            completedAt: event.occurredAt,
            status: summaryStatus,
            durationMs: event.logicalTimeMs,
            visitedNodeIds: Object.entries(nextNodeStates)
              .filter(([, status]) => status === "success" || status === "failed")
              .map(([nodeId]) => nodeId),
            eventCount: state.visibleEvents.length + 1,
          }
        : undefined;
      return {
        nodeStates: nextNodeStates,
        activeEdgeId,
        runStatus: nextStatus,
        eventCursor: Math.max(state.eventCursor, event.sequence),
        visibleEvents: [...state.visibleEvents, event].sort((a, b) => a.sequence - b.sequence).slice(-500),
        runHistory: summary
          ? [summary, ...state.runHistory.filter((run) => run.id !== summary.id)]
          : state.runHistory,
      };
    }),

  setStreamStatus: (streamStatus) => set({ streamStatus }),

  applyNextEvent: () =>
    set((state) => {
      const event = state.plannedEvents[state.eventCursor];
      if (!event) return {};
      const nextNodeStates = { ...state.nodeStates };
      let nextStatus = state.runStatus;
      let activeEdgeId = state.activeEdgeId;
      if (event.type === "node.queued" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "queued";
      if (event.type === "node.started" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "running";
      if (event.type === "node.waiting" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "waiting";
      if (event.type === "node.completed" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "success";
      if (event.type === "node.failed" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "failed";
      if (event.type === "node.skipped" && event.payload.nodeId) nextNodeStates[event.payload.nodeId] = "skipped";
      if (event.type === "edge.traversed") activeEdgeId = event.payload.edgeId;
      if (event.type === "run.completed") nextStatus = "completed";
      if (event.type === "run.failed") nextStatus = "failed";
      if (event.type === "run.limit_exceeded") nextStatus = "failed";
      if (event.type === "run.cancelled") nextStatus = "cancelled";
      if (event.type === "run.interrupted") nextStatus = "failed";
      const terminal = [
        "run.completed",
        "run.failed",
        "run.limit_exceeded",
        "run.cancelled",
        "run.interrupted",
      ].includes(event.type);
      return {
        nodeStates: nextNodeStates,
        activeEdgeId,
        runStatus: nextStatus,
        eventCursor: state.eventCursor + 1,
        visibleEvents: [...state.visibleEvents, event].slice(-500),
        runHistory: terminal && state.activePlan
          ? [state.activePlan.summary, ...state.runHistory.filter((run) => run.id !== state.activePlan?.summary.id)]
          : state.runHistory,
      };
    }),

  pauseSimulation: () => set((state) => state.runStatus === "running" ? { runStatus: "paused" } : {}),
  resumeSimulation: () => set((state) => state.runStatus === "paused" ? { runStatus: "running" } : {}),
  cancelSimulation: () => set({ runStatus: "cancelled", activeEdgeId: undefined }),
  resetSimulation: () =>
    set((state) => ({
      nodeStates: initialNodeStates(state.document.definition),
      activeEdgeId: undefined,
      runStatus: "idle",
      runSource: "local",
      remoteRunId: undefined,
      streamStatus: "idle",
      plannedEvents: [],
      eventCursor: 0,
      visibleEvents: [],
      activePlan: undefined,
    })),
  setSpeed: (speed) => set({ speed }),
}));
