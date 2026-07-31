"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  getCurrentUser,
  hasConfiguredApi,
  logout,
  type AccountUser,
} from "@/lib/account-service";
import { createProject, listProjects, type ProjectSummary } from "@/lib/workspace-service";
import { DEMO_PROJECTS } from "@/lib/demo-flow";

const demoStats = [
  ["12", "flujos activos"],
  ["1.248", "ejecuciones"],
  ["98,4%", "rutas exitosas"],
];

export default function HomePage() {
  const router = useRouter();
  const [projects, setProjects] = useState<ProjectSummary[]>(
    hasConfiguredApi
      ? []
      : DEMO_PROJECTS.map((project) => ({
          ...project,
          role: project.role as ProjectSummary["role"],
          createdAt: project.updatedAt,
        })),
  );
  const [user, setUser] = useState<AccountUser | undefined>(
    hasConfiguredApi
      ? undefined
      : {
          id: "demo-user",
          email: "demo@flowverse.dev",
          displayName: "Andrés",
          createdAt: "2026-07-30T00:00:00.000Z",
        },
  );
  const [checkingSession, setCheckingSession] = useState(hasConfiguredApi);
  const [projectDialog, setProjectDialog] = useState(false);
  const [projectError, setProjectError] = useState("");
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    if (!hasConfiguredApi) return;
    let cancelled = false;
    getCurrentUser()
      .then(async (currentUser) => {
        if (cancelled) return;
        if (!currentUser) {
          router.replace("/acceso");
          return;
        }
        setUser(currentUser);
        setProjects(await listProjects());
      })
      .catch((error: unknown) => {
        if (!cancelled) setProjectError(error instanceof Error ? error.message : "No se pudo validar tu sesión.");
      })
      .finally(() => {
        if (!cancelled) setCheckingSession(false);
      });
    return () => {
      cancelled = true;
    };
  }, [router]);

  if (checkingSession || (hasConfiguredApi && !user && !projectError)) {
    return (
      <main className="editor-loading">
        <span className="scene-loader" />
        <strong>Validando tu sesión…</strong>
      </main>
    );
  }

  const displayName = user?.displayName || "Usuario";
  const firstName = displayName.split(/\s+/)[0];
  const initials = displayName
    .split(/\s+/)
    .slice(0, 2)
    .map((part) => part[0]?.toLocaleUpperCase("es"))
    .join("");
  const stats = hasConfiguredApi
    ? [
        [String(projects.length), projects.length === 1 ? "proyecto" : "proyectos"],
        ["—", "ejecuciones"],
        ["—", "rutas exitosas"],
      ]
    : demoStats;

  return (
    <main className="dashboard-page">
      <header className="topbar">
        <Link className="brand" href="/">
          <span className="brand-mark" aria-hidden="true">FV</span>
          <span>FlowVerse <b>3D</b></span>
        </Link>
        <nav className="topbar-actions" aria-label="Navegación principal">
          <span className="status-pill"><i /> {hasConfiguredApi ? "API conectada" : "Modo demo"}</span>
          {hasConfiguredApi ? (
            <>
              <span className="avatar" aria-label={`Sesión de ${displayName}`}>{initials}</span>
              <button
                type="button"
                className="logout-button"
                onClick={async () => {
                  try {
                    await logout();
                  } finally {
                    router.replace("/acceso");
                  }
                }}
              >Cerrar sesión</button>
            </>
          ) : (
            <Link className="avatar" href="/acceso" aria-label="Abrir cuenta">{initials}</Link>
          )}
        </nav>
      </header>

      <section className="dashboard-content">
        <div className="eyebrow">
          {hasConfiguredApi ? "ESPACIO DE TRABAJO" : "ESPACIO DE TRABAJO / DEMO"}
        </div>
        <div className="welcome-row">
          <div>
            <h1>Buenas noches, {firstName}.</h1>
            <p>Convierte procesos complejos en sistemas que puedas ver y entender.</p>
          </div>
          <button className="primary-button" type="button" onClick={() => setProjectDialog(true)}>
            <span aria-hidden="true">＋</span> Nuevo proyecto
          </button>
        </div>

        <div className="stats-grid">
          {stats.map(([value, label]) => (
            <article className="stat-card" key={label}>
              <strong>{value}</strong>
              <span>{label}</span>
            </article>
          ))}
        </div>

        <div className="section-heading">
          <div>
            <span className="eyebrow">PROYECTOS RECIENTES</span>
            <h2>Universos en construcción</h2>
          </div>
          {projects[0] && <Link href={`/proyectos/${projects[0].id}`}>Ver todos →</Link>}
        </div>

        <div className="project-grid">
          {projects.slice(0, 2).map((project, index) => (
            <Link className={`project-card ${index === 0 ? "featured" : ""}`} href={`/proyectos/${project.id}`} key={project.id}>
              <div className={`mini-universe ${index ? "alternate" : ""}`} aria-hidden="true">
                <span className={`orbit ${index ? "orbit-three" : "orbit-one"}`} />
                {!index && <span className="orbit orbit-two" />}
                <i className={`planet ${index ? "planet-four" : "planet-one"}`} />
                <i className={`planet ${index ? "planet-five" : "planet-two"}`} />
                {!index && <i className="planet planet-three" />}
              </div>
              <div className="project-meta">
                <span className="project-icon">{index ? "⌬" : "◈"}</span>
                <div>
                  <h3>{project.name}</h3>
                  <p>{project.flowCount ?? "—"} flujos · {project.role}</p>
                </div>
                <span className="arrow">↗</span>
              </div>
            </Link>
          ))}
          <button className="new-project-card" type="button" onClick={() => setProjectDialog(true)}>
            <span>＋</span>
            <strong>Nuevo proyecto</strong>
            <small>Crea un espacio para tus flujos</small>
          </button>
        </div>

        {projects[0] ? (
          <aside className="continue-card">
            <div className="continue-icon">▶</div>
            <div>
              <span className="eyebrow">CONTINUAR DONDE LO DEJASTE</span>
              <h3>{hasConfiguredApi ? projects[0].name : "Procesamiento de pedidos"}</h3>
              <p>{hasConfiguredApi ? "Abre el proyecto para continuar" : "12 nodos · borrador guardado hace 4 min"}</p>
            </div>
            <Link
              className="secondary-button"
              href={hasConfiguredApi ? `/proyectos/${projects[0].id}` : `/proyectos/${projects[0].id}/flujos/pedidos`}
            >Abrir</Link>
          </aside>
        ) : (
          <aside className="continue-card empty-dashboard">
            <div className="continue-icon">＋</div>
            <div><h3>Aún no tienes proyectos</h3><p>Crea el primero para comenzar a diseñar flujos.</p></div>
            <button className="secondary-button" type="button" onClick={() => setProjectDialog(true)}>Crear proyecto</button>
          </aside>
        )}
        {projectError && <p className="dialog-error dashboard-error" role="alert">⚠ {projectError}</p>}
      </section>

      {projectDialog && (
        <div className="modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && setProjectDialog(false)}>
          <section
            className="modal-card"
            role="dialog"
            aria-modal="true"
            aria-labelledby="new-project-title"
            onKeyDown={(event) => {
              if (event.key === "Escape") setProjectDialog(false);
            }}
          >
            <header>
              <div><span className="eyebrow">NUEVO ESPACIO</span><h2 id="new-project-title">Crear proyecto</h2></div>
              <button type="button" className="close-button" onClick={() => setProjectDialog(false)} aria-label="Cerrar">×</button>
            </header>
            <form
              className="modal-body"
              onSubmit={async (event) => {
                event.preventDefault();
                const values = new FormData(event.currentTarget);
                setCreating(true);
                setProjectError("");
                try {
                  const project = await createProject({
                    name: String(values.get("name") ?? ""),
                    description: String(values.get("description") ?? ""),
                  });
                  setProjects((current) => [project, ...current]);
                  setProjectDialog(false);
                } catch (error) {
                  setProjectError(error instanceof Error ? error.message : "No se pudo crear el proyecto.");
                } finally {
                  setCreating(false);
                }
              }}
            >
              <div className="field"><label htmlFor="project-name">Nombre</label><input id="project-name" name="name" minLength={1} maxLength={120} required autoFocus /></div>
              <div className="field"><label htmlFor="project-description">Descripción</label><textarea id="project-description" name="description" rows={4} maxLength={2_000} /></div>
              {projectError && <p className="dialog-error" role="alert">⚠ {projectError}</p>}
              <button type="submit" className="primary-button full" disabled={creating}>{creating ? "Creando…" : "Crear proyecto"}</button>
            </form>
          </section>
        </div>
      )}
    </main>
  );
}
