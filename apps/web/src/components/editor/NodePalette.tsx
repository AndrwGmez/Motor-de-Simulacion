"use client";

import { NODE_PRESENTATION, NODE_TYPES, type NodeType } from "@/lib/flow-types";

interface NodePaletteProps {
  disabled?: boolean;
  onAdd: (type: NodeType) => void;
  onOpenText: () => void;
  onOpenImport: () => void;
}

export function NodePalette({ disabled, onAdd, onOpenText, onOpenImport }: NodePaletteProps) {
  return (
    <aside className="node-palette" aria-label="Paleta de nodos">
      <div className="panel-title">
        <div>
          <span className="eyebrow">CONSTRUCCIÓN</span>
          <h2>Nodos</h2>
        </div>
      </div>

      <div className="palette-actions">
        <button type="button" className="ai-entry-button" onClick={onOpenText} disabled={disabled}>
          <span aria-hidden="true">✦</span>
          <span><strong>Describir con IA</strong><small>Texto → universo</small></span>
          <i aria-hidden="true">→</i>
        </button>
        <button type="button" className="import-entry-button" onClick={onOpenImport} disabled={disabled}>
          <span aria-hidden="true">⇧</span> Importar JSON
        </button>
      </div>

      <div className="palette-divider"><span>CREAR MANUALMENTE</span></div>
      <div className="node-type-list">
        {NODE_TYPES.map((type) => {
          const item = NODE_PRESENTATION[type];
          return (
            <button
              type="button"
              className="node-type-button"
              key={type}
              disabled={disabled}
              onClick={() => onAdd(type)}
              data-testid={`add-${type}`}
            >
              <span className={`node-shape shape-${type}`} style={{ "--node-color": item.color } as React.CSSProperties}>
                {item.icon}
              </span>
              <span>
                <strong>{item.label}</strong>
                <small>{item.description}</small>
              </span>
              <i aria-hidden="true">＋</i>
            </button>
          );
        })}
      </div>
      <div className="palette-footer">
        <span>Arrastra el universo para rotar</span>
        <span className="help-dot" title="Atajos: Supr elimina, Ctrl/Cmd+Z deshace">?</span>
      </div>
    </aside>
  );
}
