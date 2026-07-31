"use client";

import { useEffect, useState } from "react";
import { NODE_PRESENTATION, type FlowEdge, type FlowNode } from "@flowverse/core";
import { useFlowStore } from "@/store/flow-store";

function ConfigurationEditor({
  node,
  disabled,
  onChange,
}: {
  node: FlowNode;
  disabled?: boolean;
  onChange: (configuration: Record<string, unknown>) => void;
}) {
  const [value, setValue] = useState(() => JSON.stringify(node.configuration, null, 2));
  const [error, setError] = useState("");
  useEffect(() => {
    setValue(JSON.stringify(node.configuration, null, 2));
    setError("");
  }, [node.id, node.configuration]);

  return (
    <div className="field">
      <label htmlFor="node-configuration">Configuración JSON</label>
      <textarea
        id="node-configuration"
        className={error ? "invalid" : ""}
        value={value}
        disabled={disabled}
        rows={6}
        spellCheck={false}
        onChange={(event) => setValue(event.target.value)}
        onBlur={() => {
          try {
            const parsed = JSON.parse(value) as unknown;
            if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error();
            onChange(parsed as Record<string, unknown>);
            setError("");
          } catch {
            setError("Debe ser un objeto JSON válido.");
          }
        }}
      />
      {error && <small className="field-error">{error}</small>}
    </div>
  );
}

function NodeInspector({ node, readOnly }: { node: FlowNode; readOnly?: boolean }) {
  const updateNode = useFlowStore((state) => state.updateNode);
  const toggleNodeLock = useFlowStore((state) => state.toggleNodeLock);
  const presentation = NODE_PRESENTATION[node.type];

  return (
    <>
      <div className="inspector-identity">
        <span className={`node-shape shape-${node.type}`} style={{ "--node-color": node.metadata.color ?? presentation.color } as React.CSSProperties}>
          {presentation.icon}
        </span>
        <div>
          <span className="eyebrow">{presentation.label.toUpperCase()}</span>
          <strong>{node.id}</strong>
        </div>
      </div>
      <div className="field">
        <label htmlFor="node-label">Nombre</label>
        <input
          id="node-label"
          value={node.label}
          disabled={readOnly}
          maxLength={120}
          onChange={(event) => updateNode(node.id, { label: event.target.value })}
        />
      </div>
      <div className="field">
        <label htmlFor="node-description">Descripción</label>
        <textarea
          id="node-description"
          value={node.description}
          disabled={readOnly}
          maxLength={2_000}
          rows={3}
          onChange={(event) => updateNode(node.id, { description: event.target.value })}
        />
      </div>
      {node.type !== "group" && (
        <div className="field-row">
          <div className="field">
            <label htmlFor="node-duration">Duración lógica</label>
            <div className="input-suffix">
              <input
                id="node-duration"
                type="number"
                min={0}
                max={86_400_000}
                value={node.durationMs}
                disabled={readOnly}
                onChange={(event) => updateNode(node.id, { durationMs: Math.max(0, Number(event.target.value)) })}
              />
              <span>ms</span>
            </div>
          </div>
          <div className="field">
            <label htmlFor="activation-mode">Activación</label>
            <select
              id="activation-mode"
              value={node.activationMode}
              disabled={readOnly}
              onChange={(event) => updateNode(node.id, { activationMode: event.target.value as FlowNode["activationMode"] })}
            >
              <option value="each">Cada token</option>
              <option value="any">Primero</option>
              <option value="all">Todos</option>
            </select>
          </div>
        </div>
      )}
      <div className="field">
        <label htmlFor="node-category">Categoría</label>
        <input
          id="node-category"
          value={node.metadata.category ?? ""}
          disabled={readOnly}
          maxLength={80}
          onChange={(event) => updateNode(node.id, { metadata: { ...node.metadata, category: event.target.value } })}
        />
      </div>
      <ConfigurationEditor
        node={node}
        disabled={readOnly}
        onChange={(configuration) => updateNode(node.id, { configuration })}
      />
      <div className="port-section">
        <span className="eyebrow">PUERTOS</span>
        <div className="ports">
          {node.inputs.map((port) => <span key={`in-${port.id}`}><i className="port input" />{port.label}</span>)}
          {node.outputs.map((port) => <span key={`out-${port.id}`}>{port.label}<i className="port output" /></span>)}
          {node.inputs.length === 0 && node.outputs.length === 0 && <small>Este nodo no admite conexiones.</small>}
        </div>
      </div>
      {!readOnly && (
        <button
          type="button"
          className={`lock-button ${node.locked ? "active" : ""}`}
          onClick={() => toggleNodeLock(node.id)}
        >
          <span aria-hidden="true">{node.locked ? "▣" : "▢"}</span>
          {node.locked ? "Posición bloqueada" : "Bloquear posición"}
        </button>
      )}
    </>
  );
}

function EdgeInspector({ edge, readOnly }: { edge: FlowEdge; readOnly?: boolean }) {
  const updateEdge = useFlowStore((state) => state.updateEdge);
  const [condition, setCondition] = useState(() => edge.condition ? JSON.stringify(edge.condition, null, 2) : "");
  const [error, setError] = useState("");
  useEffect(() => {
    setCondition(edge.condition ? JSON.stringify(edge.condition, null, 2) : "");
    setError("");
  }, [edge.id, edge.condition]);

  return (
    <>
      <div className="inspector-identity">
        <span className="edge-symbol">➜</span>
        <div>
          <span className="eyebrow">CONEXIÓN</span>
          <strong>{edge.source} → {edge.target}</strong>
        </div>
      </div>
      <div className="field">
        <label htmlFor="edge-label">Etiqueta</label>
        <input
          id="edge-label"
          value={edge.label}
          disabled={readOnly}
          maxLength={120}
          onChange={(event) => updateEdge(edge.id, { label: event.target.value })}
        />
      </div>
      <div className="field-row">
        <div className="field">
          <label htmlFor="edge-priority">Prioridad</label>
          <input
            id="edge-priority"
            type="number"
            min={0}
            max={10_000}
            value={edge.priority}
            disabled={readOnly}
            onChange={(event) => updateEdge(edge.id, { priority: Number(event.target.value) })}
          />
        </div>
        <label className="toggle-field">
          <input
            type="checkbox"
            checked={edge.isDefault}
            disabled={readOnly}
            onChange={(event) => updateEdge(edge.id, { isDefault: event.target.checked })}
          />
          <span />
          Predeterminado
        </label>
      </div>
      {!edge.isDefault && (
        <div className="field">
          <label htmlFor="edge-condition">Condición</label>
          <textarea
            id="edge-condition"
            className={error ? "invalid" : ""}
            value={condition}
            disabled={readOnly}
            rows={8}
            spellCheck={false}
            placeholder={'{\n  "field": "/status",\n  "operator": "equals",\n  "value": "ok"\n}'}
            onChange={(event) => setCondition(event.target.value)}
            onBlur={() => {
              if (!condition.trim()) {
                updateEdge(edge.id, { condition: undefined });
                return;
              }
              try {
                updateEdge(edge.id, { condition: JSON.parse(condition) as FlowEdge["condition"] });
                setError("");
              } catch {
                setError("La condición no contiene JSON válido.");
              }
            }}
          />
          {error && <small className="field-error">{error}</small>}
        </div>
      )}
      <div className="edge-route">
        <div><i /> <span>{edge.source}<small>{edge.sourcePort}</small></span></div>
        <b>→</b>
        <div><i /> <span>{edge.target}<small>{edge.targetPort}</small></span></div>
      </div>
    </>
  );
}

export function Inspector({ readOnly }: { readOnly?: boolean }) {
  const definition = useFlowStore((state) => state.document.definition);
  const selectedNodeId = useFlowStore((state) => state.selectedNodeId);
  const selectedEdgeId = useFlowStore((state) => state.selectedEdgeId);
  const node = definition.nodes.find((candidate) => candidate.id === selectedNodeId);
  const edge = definition.edges.find((candidate) => candidate.id === selectedEdgeId);

  return (
    <aside className="inspector" aria-label="Inspector de propiedades">
      <div className="panel-title">
        <div>
          <span className="eyebrow">INSPECTOR</span>
          <h2>Propiedades</h2>
        </div>
      </div>
      <div className="inspector-content">
        {node ? (
          <NodeInspector node={node} readOnly={readOnly} />
        ) : edge ? (
          <EdgeInspector edge={edge} readOnly={readOnly} />
        ) : (
          <div className="empty-inspector">
            <div className="empty-orbit"><i /><span /></div>
            <strong>Selecciona un elemento</strong>
          <p>
            Haz clic sobre un nodo o una conexión para {readOnly ? "consultar" : "editar"} sus propiedades.
          </p>
          </div>
        )}
      </div>
    </aside>
  );
}
