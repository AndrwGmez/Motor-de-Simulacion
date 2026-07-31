"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  createFlow,
  getProject,
  listFlows,
  type FlowSummary,
  type ProjectSummary,
} from "@/lib/workspace-service";

export function ProjectClient({ projectId }: { projectId: string }) {
  const router = useRouter();
  const [project, setProject] = useState<ProjectSummary>();
  const [flows, setFlows] = useState<FlowSummary[]>([]);
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([getProject(projectId), listFlows(projectId)])
      .then(([loadedProject, loadedFlows]) => {
        setProject(loadedProject);
        setFlows(loadedFlows);
      })
      .catch((cause: unknown) => setError(cause instanceof Error ? cause.message : "No se pudo cargar el proyecto."));
  }, [projectId]);

  const filtered = flows.filter((flow) => `${flow.name} ${flow.description}`.toLocaleLowerCase("es").includes(query.toLocaleLowerCase("es")));

  return (
    <main className="project-page">
      <header className="topbar">
        <Link className="brand" href="/"><span className="brand-mark">FV</span><span>FlowVerse <b>3D</b></span></Link>
        <nav className="topbar-actions"><span className="status-pill"><i /> {project ? "Proyecto conectado" : "Cargando…"}</span><span className="avatar">AN</span></nav>
      </header>
      <div className="project-content">
        <Link className="back-link" href="/">← Todos los proyectos</Link>
        <div className="project-hero">
          <div>
            <span className="eyebrow">PROYECTO / {(project?.role ?? "OWNER").toUpperCase()}</span>
            <h1>{project?.name ?? "Abriendo proyecto…"}</h1>
            <p>{project?.description}</p>
          </div>
          {project && project.role !== "viewer" && (
            <button className="primary-button" type="button" onClick={() => setDialog(true)}>＋ Nuevo flujo</button>
          )}
        </div>
        <section className="flows-section">
          <div className="section-heading">
            <div><span className="eyebrow">FLUJOS</span><h2>Procesos del proyecto</h2></div>
            <label className="search-field">⌕ <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Buscar flujo…" /></label>
          </div>
          {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
          {filtered.map((flow, index) => (
            <Link className="flow-row-card" href={`/proyectos/${projectId}/flujos/${flow.id}`} key={flow.id}>
              <div className={`flow-preview-icon ${index % 2 ? "violet" : ""}`}><i /><i /><i /><span /></div>
              <div><strong>{flow.name}</strong><p>{flow.description || "Borrador listo para diseñar."}</p></div>
              <span className={flow.publishedVersionCount ? "published-pill" : "draft-pill"}>
                {flow.publishedVersionCount ? `V${flow.publishedVersionCount} PUBLICADA` : "BORRADOR"}
              </span>
              <div className="flow-row-stat"><strong>—</strong><small>nodos</small></div>
              <div className="flow-row-stat"><strong>—</strong><small>conexiones</small></div>
              <time>{new Intl.DateTimeFormat("es-CO", { dateStyle: "short" }).format(new Date(flow.updatedAt))}</time>
              <span>→</span>
            </Link>
          ))}
          {!error && filtered.length === 0 && <div className="empty-project-list">No hay flujos que coincidan con la búsqueda.</div>}
        </section>
      </div>

      {dialog && (
        <div className="modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && setDialog(false)}>
          <section
            className="modal-card"
            role="dialog"
            aria-modal="true"
            aria-labelledby="new-flow-title"
            onKeyDown={(event) => {
              if (event.key === "Escape") setDialog(false);
            }}
          >
            <header>
              <div><span className="eyebrow">NUEVO BORRADOR</span><h2 id="new-flow-title">Crear flujo</h2></div>
              <button type="button" className="close-button" onClick={() => setDialog(false)} aria-label="Cerrar">×</button>
            </header>
            <form
              className="modal-body"
              onSubmit={async (event) => {
                event.preventDefault();
                const data = new FormData(event.currentTarget);
                setCreating(true);
                setError("");
                try {
                  const flow = await createFlow(projectId, String(data.get("name") ?? ""));
                  setFlows((current) => [flow, ...current]);
                  setDialog(false);
                  router.push(`/proyectos/${projectId}/flujos/${flow.id}`);
                } catch (cause) {
                  setError(cause instanceof Error ? cause.message : "No se pudo crear el flujo.");
                } finally {
                  setCreating(false);
                }
              }}
            >
              <div className="field"><label htmlFor="flow-name">Nombre</label><input id="flow-name" name="name" required maxLength={120} autoFocus /></div>
              {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
              <button type="submit" className="primary-button full" disabled={creating}>{creating ? "Creando…" : "Crear y abrir borrador"}</button>
            </form>
          </section>
        </div>
      )}
    </main>
  );
}
