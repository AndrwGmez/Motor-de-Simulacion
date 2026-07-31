import type { EditableFlow, FlowDefinition, FlowEdge, FlowNode, NodeType } from "./flow-types";
import { NODE_PRESENTATION } from "./flow-types";

function node(
  id: string,
  type: NodeType,
  label: string,
  x: number,
  y: number,
  z: number,
  options: Partial<FlowNode> = {},
): FlowNode {
  const noInput = type === "trigger" || type === "group";
  const noOutput = type === "end" || type === "group";
  return {
    id,
    type,
    label,
    description: options.description ?? NODE_PRESENTATION[type].description,
    inputs: noInput ? [] : [{ id: "input", label: "Entrada" }],
    outputs: noOutput ? [] : [{ id: "output", label: "Salida" }],
    activationMode: "each",
    durationMs: type === "delay" ? 700 : type === "process" || type === "data" || type === "integration" ? 420 : 0,
    configuration: options.configuration ?? (
      type === "trigger"
        ? { eventName: "order.received" }
        : type === "process"
          ? { operations: [] }
          : type === "decision"
            ? { strategy: "first_match" }
            : type === "data"
              ? { operations: [{ op: "set", path: "/inventory/checked", value: true }] }
              : type === "integration"
                ? { service: "Servicio simulado", latencyMs: 420, outcome: "success" }
                : type === "delay"
                  ? { delayMs: 700 }
                  : type === "end"
                    ? { result: "success" }
                    : { collapsed: false }
    ),
    position: { x, y, z },
    locked: options.locked ?? false,
    metadata: {
      category: options.metadata?.category ?? (type === "integration" ? "servicios" : "operaciones"),
      color: options.metadata?.color ?? NODE_PRESENTATION[type].color,
      groupId: options.metadata?.groupId,
    },
    ...options,
  };
}

function edge(
  id: string,
  source: string,
  target: string,
  label = "",
  options: Partial<FlowEdge> = {},
): FlowEdge {
  return {
    id,
    source,
    target,
    sourcePort: "output",
    targetPort: "input",
    label,
    priority: 1,
    isDefault: false,
    ...options,
  };
}

export const DEMO_FLOW: FlowDefinition = {
  schemaVersion: "1.0",
  name: "Procesamiento de pedidos",
  description: "Valida el pago, reserva inventario y coordina la entrega de un pedido.",
  metadata: {
    tags: ["pedidos", "demo"],
    createdWith: "FlowVerse 3D",
  },
  layout: {
    mode: "directional",
    camera: {
      position: { x: 0, y: 40, z: 520 },
      target: { x: 120, y: 0, z: 0 },
    },
  },
  variables: [
    { path: "/payment/status", type: "string", required: true, default: "approved" },
    { path: "/inventory/available", type: "boolean", required: true, default: true },
    { path: "/inventory/checked", type: "boolean", required: false, default: false },
  ],
  nodes: [
    node("start", "trigger", "Pedido recibido", -350, 0, 0, { locked: true }),
    node("validate-payment", "integration", "Validar pago", -250, 0, 0, {
      description: "Consulta al proveedor de pagos simulado.",
      configuration: { service: "Proveedor de pagos", latencyMs: 420, outcome: "success" },
      metadata: { category: "pagos", color: NODE_PRESENTATION.integration.color },
    }),
    node("payment-approved", "decision", "¿Pago aprobado?", -145, 0, 0, {
      outputs: [
        { id: "approved", label: "Aprobado" },
        { id: "rejected", label: "Rechazado" },
      ],
    }),
    node("reserve-inventory", "data", "Consultar inventario", -25, 64, 0, {
      configuration: { operations: [{ op: "set", path: "/inventory/checked", value: true }] },
      metadata: { category: "inventario", color: NODE_PRESENTATION.data.color },
    }),
    node("inventory-available", "decision", "¿Hay inventario?", 90, 64, 0, {
      outputs: [
        { id: "yes", label: "Sí" },
        { id: "no", label: "No" },
      ],
    }),
    node("prepare", "process", "Preparar pedido", 205, 105, 0, {
      metadata: { category: "logística", color: NODE_PRESENTATION.process.color },
    }),
    node("handoff-delay", "delay", "Ventana de despacho", 315, 105, 0, {
      configuration: { delayMs: 700 },
      metadata: { category: "logística", color: NODE_PRESENTATION.delay.color },
    }),
    node("ship", "integration", "Enviar pedido", 425, 105, 0, {
      configuration: { service: "Transportadora", latencyMs: 420, outcome: "success" },
      metadata: { category: "logística", color: NODE_PRESENTATION.integration.color },
    }),
    node("confirm", "process", "Confirmar entrega", 535, 105, 0, {
      metadata: { category: "logística", color: NODE_PRESENTATION.process.color },
    }),
    node("completed", "end", "Pedido completado", 650, 105, 0, {
      configuration: { result: "success" },
      metadata: { category: "resultado", color: NODE_PRESENTATION.end.color },
    }),
    node("refund", "process", "Devolver dinero", 205, -78, 0, {
      metadata: { category: "pagos", color: "#ff617d" },
    }),
    node("cancelled", "end", "Pedido cancelado", 325, -78, 0, {
      configuration: { result: "failure" },
      metadata: { category: "resultado", color: "#ff617d" },
    }),
    node("logistics-group", "group", "Operación logística", 422, 105, -35, {
      description: "Agrupa las tareas de preparación y entrega.",
      position: { x: 425, y: 105, z: -35 },
      metadata: { category: "logística", color: NODE_PRESENTATION.group.color },
      configuration: { collapsed: false },
    }),
  ],
  edges: [
    edge("e-start-payment", "start", "validate-payment"),
    edge("e-payment-check", "validate-payment", "payment-approved"),
    edge("e-payment-approved", "payment-approved", "reserve-inventory", "Aprobado", {
      sourcePort: "approved",
      condition: { field: "/payment/status", operator: "equals", value: "approved" },
      priority: 1,
    }),
    edge("e-payment-rejected", "payment-approved", "refund", "Rechazado", {
      sourcePort: "rejected",
      isDefault: true,
      priority: 99,
    }),
    edge("e-inventory-check", "reserve-inventory", "inventory-available"),
    edge("e-stock-yes", "inventory-available", "prepare", "Sí", {
      sourcePort: "yes",
      condition: { field: "/inventory/available", operator: "equals", value: true },
      priority: 1,
    }),
    edge("e-stock-no", "inventory-available", "refund", "No", {
      sourcePort: "no",
      isDefault: true,
      priority: 99,
    }),
    edge("e-prepare-delay", "prepare", "handoff-delay"),
    edge("e-delay-ship", "handoff-delay", "ship"),
    edge("e-ship-confirm", "ship", "confirm"),
    edge("e-confirm-complete", "confirm", "completed"),
    edge("e-refund-cancelled", "refund", "cancelled"),
  ],
};

export const DEMO_DOCUMENT: EditableFlow = {
  flowId: "pedidos",
  versionId: "pedidos-draft",
  status: "draft",
  revision: 7,
  etag: '"demo-7"',
  draftMatchesPublished: false,
  updatedAt: "2026-07-30T19:46:00.000Z",
  definition: DEMO_FLOW,
};

export const DEMO_PROJECTS = [
  {
    id: "demo",
    name: "Operaciones comerciales",
    description: "Procesos de pedidos, pagos y atención.",
    flowCount: 4,
    role: "owner",
    updatedAt: "2026-07-30T19:46:00.000Z",
  },
  {
    id: "customer-journey",
    name: "Customer journey",
    description: "Flujos de adquisición, activación y retención.",
    flowCount: 8,
    role: "editor",
    updatedAt: "2026-07-29T16:12:00.000Z",
  },
];
