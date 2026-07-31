/**
 * Genera un flujo grande y realista dentro de los límites del contrato
 * (500 nodos y 1.000 conexiones) para probar el rendimiento del editor 3D,
 * del validador, del analizador y del motor de simulación.
 *
 *   node samples/generar-plataforma.mjs > samples/plataforma-500.flow.json
 *
 * El resultado es determinista: no usa aleatoriedad ni la fecha del sistema.
 */

// Escala configurable: `node samples/generar-plataforma.mjs --escala grande`.
// El contrato admite hasta 5.000 nodos y 10.000 conexiones.
const ESCALA = process.argv.includes("--escala") ? process.argv[process.argv.indexOf("--escala") + 1] : "normal";
const GRANDE = ESCALA === "grande";
const NODE_BUDGET = GRANDE ? 5000 : 500;
const EDGE_BUDGET = GRANDE ? 10000 : 1000;
// Un router no puede exponer más de 20 puertos de salida, así que el ancho se
// reparte entre más dominios en lugar de engordar cada uno.
const DOMINIOS_OBJETIVO = GRANDE ? 20 : 4;
const AREAS_POR_DOMINIO = GRANDE ? 14 : 7;

const CATALOGO = [
  { id: "comercio", label: "Comercio", color: "#4D8DFF", areas: ["captacion", "catalogo", "carrito", "checkout", "postventa", "fidelizacion", "devoluciones", "promociones", "precios", "recomendador", "buscador", "resenas", "suscripciones", "afiliados"] },
  { id: "logistica", label: "Logística", color: "#F49A59", areas: ["recepcion", "almacenaje", "picking", "empaque", "expedicion", "ultimaMilla", "incidencias", "rutas", "flota", "aduanas", "inventario", "reposicion", "devolucionesLog", "trazabilidad"] },
  { id: "soporte", label: "Soporte", color: "#36C6D8", areas: ["triaje", "diagnostico", "escalado", "resolucion", "satisfaccion", "conocimiento", "auditoriaSop", "chat", "telefonia", "garantias", "reparaciones", "formacion", "encuestas", "comunidad"] },
  { id: "finanzas", label: "Finanzas", color: "#B078FF", areas: ["facturacion", "cobros", "conciliacion", "impuestos", "riesgo", "tesoreria", "cierreMes", "presupuesto", "auditoriaFin", "morosidad", "divisas", "nomina", "activos", "reporting"] },
  { id: "personas", label: "Personas", color: "#35D39D", areas: ["seleccion", "onboarding", "desempeno", "retribucion", "vacaciones", "salida", "clima", "talento", "movilidad", "prevencion", "diversidad", "sucesion", "beneficios", "convenios"] },
  { id: "producto", label: "Producto", color: "#F2C15D", areas: ["descubrimiento", "diseno", "prototipo", "validacion", "lanzamiento", "iteracion", "retirada", "investigacion", "roadmap", "metricas", "experimentos", "accesibilidad", "documentacion", "soporteProd"] },
  { id: "datos", label: "Datos", color: "#7F8CFF", areas: ["ingesta", "limpieza", "modelado", "publicacion", "gobierno", "calidad", "archivado", "catalogoDatos", "linaje", "privacidad", "anonimizado", "cuadros", "alertas", "retencion"] },
  { id: "plataforma", label: "Plataforma", color: "#FF9AA2", areas: ["provision", "despliegue", "observabilidad", "incidentes", "capacidad", "seguridad", "respaldos", "identidad", "redes", "costes", "cumplimiento", "parcheo", "recuperacion", "guardias"] },
  { id: "legal", label: "Legal", color: "#B7C4E8", areas: ["contratos", "litigios", "propiedad", "regulatorio", "consumo", "proveedoresLeg", "societario", "marcas", "licencias", "reclamaciones", "sanciones", "informes", "poderes", "archivoLeg"] },
  { id: "marketing", label: "Marketing", color: "#59C2F4", areas: ["campanas", "contenidos", "eventos", "prensa", "redes", "email", "seo", "analitica", "presupuestoMk", "creatividad", "medios", "patrocinios", "fidelidad", "estudios"] },
];

// Con escala grande se replica el catálogo hasta reunir los dominios pedidos.
const DOMAINS = Array.from({ length: DOMINIOS_OBJETIVO }, (_, index) => {
  const base = CATALOGO[index % CATALOGO.length];
  const vuelta = Math.floor(index / CATALOGO.length);
  return {
    id: vuelta === 0 ? base.id : `${base.id}${vuelta + 1}`,
    label: vuelta === 0 ? base.label : `${base.label} ${vuelta + 1}`,
    color: base.color,
    areas: base.areas.slice(0, AREAS_POR_DOMINIO),
  };
});

const nodes = [];
const edges = [];
const decisions = [];

let edgeSeq = 0;
const port = (id, label) => ({ id, label });

function addNode(node) {
  nodes.push({
    activationMode: "each",
    durationMs: 0,
    locked: false,
    ...node,
    configuration: node.configuration ?? {},
    inputs: node.inputs ?? [port("entrada", "Entrada")],
    outputs: node.outputs ?? [port("salida", "Salida")],
  });
  return node.id;
}

function addEdge(source, target, options = {}) {
  edgeSeq += 1;
  edges.push({
    id: `c${String(edgeSeq).padStart(4, "0")}`,
    source,
    target,
    sourcePort: options.sourcePort ?? "salida",
    targetPort: options.targetPort ?? "entrada",
    ...(options.label ? { label: options.label } : {}),
    ...(options.condition ? { condition: options.condition } : {}),
    priority: options.priority ?? 0,
    isDefault: options.isDefault ?? false,
  });
}

// La malla se dibuja por columnas (dominio) y filas (área) para que los modos
// directional, layers y clusters produzcan lecturas distintas del mismo grafo.
// El espaciado se ajusta a la escala: si el grafo crece diez veces en altura y
// los nodos siguen midiendo 20 unidades, al alejarse dejan de ocupar un píxel.
const SEPARACION_FILA = GRANDE ? 42 : 150;
const SEPARACION_COLUMNA = GRANDE ? 150 : 260;
const SEPARACION_PROFUNDIDAD = GRANDE ? 90 : 220;
const place = (column, row, depth) => ({
  x: column * SEPARACION_COLUMNA - 1800,
  y: row * SEPARACION_FILA - 900,
  z: depth * SEPARACION_PROFUNDIDAD - 660,
});

addNode({
  id: "inicio",
  type: "trigger",
  label: "Solicitud recibida",
  description: "Punto de entrada único de la plataforma.",
  inputs: [],
  configuration: { eventName: "platform.request" },
  position: place(0, 6, 3),
  metadata: { category: "entrada", color: "#7F8CFF" },
});

addNode({
  id: "clasificar",
  type: "decision",
  label: "¿Qué dominio atiende?",
  outputs: DOMAINS.map((domain) => port(domain.id, domain.label)),
  durationMs: 30,
  configuration: { strategy: "first_match" },
  position: place(1, 6, 3),
  metadata: { category: "enrutado", color: "#B078FF" },
});
decisions.push("clasificar");
addEdge("inicio", "clasificar");

DOMAINS.forEach((domain, domainIndex) => {
  const routerId = `router_${domain.id}`;
  addNode({
    id: routerId,
    type: "decision",
    label: `Prioridad en ${domain.label}`,
    outputs: domain.areas.map((area) => port(area, area)),
    durationMs: 25,
    configuration: { strategy: "first_match" },
    position: place(2, domainIndex * (AREAS_POR_DOMINIO + 2) + AREAS_POR_DOMINIO / 2, 3),
    metadata: { category: domain.id, color: domain.color },
  });
  decisions.push(routerId);
  addEdge("clasificar", routerId, {
    sourcePort: domain.id,
    label: domain.label,
    priority: domainIndex + 1,
    isDefault: domainIndex === DOMAINS.length - 1,
    ...(domainIndex === DOMAINS.length - 1
      ? {}
      : { condition: { field: "/request/domain", operator: "equals", value: domain.id } }),
  });

  domain.areas.forEach((area, areaIndex) => {
    const prefix = `${domain.id}_${area}`;
    const row = domainIndex * (AREAS_POR_DOMINIO + 2) + areaIndex;
    const meta = { category: `${domain.id}/${area}`, color: domain.color };

    const entrada = addNode({ id: `${prefix}_entrada`, type: "process", label: `Recibir ${area}`, durationMs: 90, configuration: { operations: [{ op: "set", path: `/${domain.id}/${area}/stage`, value: "received" }] }, position: place(3, row, 0), metadata: meta });
    const preparar = addNode({ id: `${prefix}_preparar`, type: "data", label: `Preparar ${area}`, durationMs: 120, configuration: { operations: [{ op: "copy", from: "/request/payload", path: `/${domain.id}/${area}/payload` }] }, position: place(4, row, 0), metadata: meta });
    const control = addNode({
      id: `${prefix}_control`,
      type: "decision",
      label: `¿Cómo resolver ${area}?`,
      outputs: [port("automatico", "Automático"), port("manual", "Manual"), port("excepcion", "Excepción")],
      durationMs: 35,
      configuration: { strategy: "first_match" },
      position: place(5, row, 0),
      metadata: meta,
    });
    decisions.push(control);

    const autoServicio = addNode({ id: `${prefix}_servicio`, type: "integration", label: `Servicio de ${area}`, configuration: { service: `${domain.label} · ${area}`, latencyMs: 150 + areaIndex * 25, outcome: "success" }, position: place(6, row, 1), metadata: meta });
    const autoEspera = addNode({ id: `${prefix}_espera`, type: "delay", label: `Ventana de ${area}`, configuration: { delayMs: 60000 * (areaIndex + 1) }, position: place(7, row, 1), metadata: meta });
    const autoCerrar = addNode({ id: `${prefix}_auto`, type: "process", label: `Cerrar ${area} automático`, durationMs: 80, configuration: { operations: [{ op: "set", path: `/${domain.id}/${area}/mode`, value: "auto" }] }, position: place(8, row, 1), metadata: meta });

    const manualCola = addNode({ id: `${prefix}_cola`, type: "process", label: `Encolar ${area}`, durationMs: 60, configuration: { operations: [{ op: "set", path: `/${domain.id}/${area}/queue`, value: true }] }, position: place(6, row, -1), metadata: meta });
    const manualEspera = addNode({ id: `${prefix}_turno`, type: "delay", label: `Turno de ${area}`, configuration: { delayMs: 900000 }, position: place(7, row, -1), metadata: meta });
    const manualCerrar = addNode({ id: `${prefix}_manual`, type: "process", label: `Resolver ${area} a mano`, durationMs: 400, configuration: { operations: [{ op: "set", path: `/${domain.id}/${area}/mode`, value: "manual" }] }, position: place(8, row, -1), metadata: meta });

    const excRegistrar = addNode({ id: `${prefix}_excepcion`, type: "process", label: `Registrar excepción de ${area}`, durationMs: 70, configuration: { operations: [{ op: "set", path: `/${domain.id}/${area}/incident`, value: true }] }, position: place(6, row, -2), metadata: meta });
    const excDecidir = addNode({
      id: `${prefix}_reintento`,
      type: "decision",
      label: `¿Reintentar ${area}?`,
      outputs: [port("reintentar", "Reintentar"), port("rendirse", "Rendirse")],
      durationMs: 25,
      configuration: { strategy: "first_match" },
      position: place(7, row, -2),
      metadata: meta,
    });
    decisions.push(excDecidir);

    const consolidar = addNode({ id: `${prefix}_consolidar`, type: "process", label: `Consolidar ${area}`, durationMs: 110, configuration: { operations: [{ op: "set", path: `/${domain.id}/${area}/stage`, value: "consolidated" }] }, position: place(9, row, 0), metadata: meta });
    const auditar = addNode({ id: `${prefix}_auditoria`, type: "data", label: `Auditar ${area}`, durationMs: 90, configuration: { operations: [{ op: "set", path: `/audit/${domain.id}/${area}`, value: "ok" }] }, position: place(10, row, 1), metadata: meta });
    const notificar = addNode({ id: `${prefix}_aviso`, type: "integration", label: `Avisar en ${area}`, configuration: { service: "Notificaciones", latencyMs: 120, outcome: "success" }, position: place(10, row, -1), metadata: meta });
    const cierre = addNode({ id: `${prefix}_cierre`, type: "process", label: `Cerrar ${area}`, activationMode: "all", durationMs: 70, configuration: { operations: [{ op: "set", path: `/${domain.id}/${area}/stage`, value: "closed" }] }, position: place(11, row, 0), metadata: meta });
    const fin = addNode({ id: `${prefix}_fin`, type: "end", label: `${area} resuelto`, outputs: [], configuration: { result: "success" }, position: place(12, row, 0), metadata: { category: `${domain.id}/${area}`, color: "#35D39D" } });
    const fallo = addNode({ id: `${prefix}_fallo`, type: "end", label: `${area} sin resolver`, outputs: [], configuration: { result: "failure" }, position: place(12, row, -2), metadata: { category: `${domain.id}/${area}`, color: "#FF6B6B" } });

    addEdge(routerId, entrada, { sourcePort: area, label: area, priority: areaIndex + 1, isDefault: areaIndex === domain.areas.length - 1, ...(areaIndex === domain.areas.length - 1 ? {} : { condition: { field: "/request/area", operator: "equals", value: area } }) });
    addEdge(entrada, preparar);
    addEdge(preparar, control);
    addEdge(control, autoServicio, { sourcePort: "automatico", label: "Automático", priority: 1, condition: { field: "/request/mode", operator: "equals", value: "auto" } });
    addEdge(control, manualCola, { sourcePort: "manual", label: "Manual", priority: 2, condition: { field: "/request/mode", operator: "equals", value: "manual" } });
    addEdge(control, excRegistrar, { sourcePort: "excepcion", label: "Excepción", priority: 3, isDefault: true });
    addEdge(autoServicio, autoEspera);
    addEdge(autoEspera, autoCerrar);
    addEdge(autoCerrar, consolidar);
    addEdge(manualCola, manualEspera);
    addEdge(manualEspera, manualCerrar);
    addEdge(manualCerrar, consolidar);
    addEdge(excRegistrar, excDecidir);
    addEdge(excDecidir, preparar, { sourcePort: "reintentar", label: "Reintentar", priority: 1, condition: { field: "/request/attempts", operator: "less_than", value: 2 } });
    addEdge(excDecidir, fallo, { sourcePort: "rendirse", label: "Rendirse", priority: 2, isDefault: true });
    addEdge(consolidar, auditar);
    addEdge(consolidar, notificar);
    addEdge(auditar, cierre);
    addEdge(notificar, cierre);
    addEdge(cierre, fin);
  });
});

// Rutas alternativas de escalado entre áreas. Salen siempre de decisiones con
// condiciones improbables, así que densifican el grafo sin multiplicar los
// caminos que recorre una ejecución normal.
const escalables = decisions.filter((id) => id.endsWith("_reintento"));
const destinos = nodes.filter((node) => node.id.endsWith("_entrada")).map((node) => node.id);
let cursor = 0;
let escalationSeq = 0;

while (edges.length < EDGE_BUDGET && escalables.length > 0) {
  const decisionId = escalables[cursor % escalables.length];
  const decision = nodes.find((node) => node.id === decisionId);
  if (decision.outputs.length >= 20) {
    cursor += 1;
    if (cursor > escalables.length * 20) break;
    continue;
  }
  const target = destinos[(cursor * 7 + escalationSeq * 13) % destinos.length];
  if (target.startsWith(decisionId.replace(/_reintento$/, ""))) {
    cursor += 1;
    continue;
  }
  escalationSeq += 1;
  const portId = `escalar${decision.outputs.length}`;
  decision.outputs.push(port(portId, `Escalar ${decision.outputs.length}`));
  addEdge(decisionId, target, {
    sourcePort: portId,
    label: "Escalar",
    priority: 100 + decision.outputs.length,
    condition: { field: "/request/escalation", operator: "equals", value: `nivel-${escalationSeq}` },
  });
  cursor += 1;
}

if (nodes.length > NODE_BUDGET) throw new RangeError(`nodos: ${nodes.length} supera ${NODE_BUDGET}`);
if (edges.length > EDGE_BUDGET) throw new RangeError(`conexiones: ${edges.length} supera ${EDGE_BUDGET}`);

const flow = {
  schemaVersion: "1.0",
  name: `Plataforma de operaciones ${nodes.length}/${edges.length}`,
  description: "Cuatro dominios, veintiocho áreas y rutas de escalado cruzado. Generado para medir el editor 3D, el validador, el analizador y el simulador cerca de los límites del contrato.",
  metadata: {
    tags: ["ejemplo", "rendimiento", "operaciones"],
    createdWith: "samples/generar-plataforma.mjs",
  },
  variables: [
    { path: "/request/domain", type: "string", required: true, default: "comercio", description: "Dominio que debe atender la solicitud." },
    { path: "/request/area", type: "string", required: true, default: "checkout", description: "Área concreta dentro del dominio." },
    { path: "/request/mode", type: "string", required: true, default: "auto", description: "auto, manual o cualquier otro valor para forzar la excepción." },
    { path: "/request/attempts", type: "integer", required: true, default: 0, description: "Reintentos consumidos por la excepción." },
    { path: "/request/escalation", type: "string", required: false, description: "Activa una ruta de escalado cruzado." },
    // El nodo de datos de cada área copia este puntero, así que necesita un
    // valor por defecto para que la simulación funcione sin configuración.
    { path: "/request/payload", type: "object", required: false, default: { id: "REQ-0001", items: 3 }, description: "Cuerpo original de la solicitud." },
  ],
  layout: { mode: "clusters", clusterBy: "category" },
  nodes,
  edges,
};

process.stdout.write(`${JSON.stringify(flow, null, 2)}\n`);
process.stderr.write(`nodos: ${nodes.length} · conexiones: ${edges.length} · decisiones: ${decisions.length}\n`);
