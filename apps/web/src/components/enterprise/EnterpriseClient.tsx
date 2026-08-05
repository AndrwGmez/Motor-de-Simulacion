"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { type FormEvent, type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { getCurrentUser, hasConfiguredApi, logout, type AccountUser } from "@/lib/account-service";
import { ApiHttpError } from "@/lib/api-client";
import {
  attachProjectToOrganization,
  createOrganization,
  createPolicyRule,
  createSsoConnection,
  deletePolicyRule,
  evaluatePolicy,
  listAuditEvents,
  listOrganizationMembers,
  listOrganizationProjects,
  listOrganizations,
  listPluginRegistrations,
  listPolicyRules,
  listSsoConnections,
  registerPlugin,
  setOrganizationMember,
  setPluginStatus,
  updatePolicyRule,
  updateSsoConnection,
  verifyAudit,
  type AuditPage,
  type AuditVerification,
  type Organization,
  type OrganizationMember,
  type OrganizationProject,
  type OrganizationRole,
  type PluginRegistration,
  type PluginStatus,
  type PolicyDecision,
  type PolicyEffect,
  type PolicyRule,
  type SsoConnection,
  type SsoProtocol,
} from "@/lib/enterprise-service";
import { listProjects, type ProjectSummary } from "@/lib/workspace-service";

type TabId = "summary" | "projects" | "members" | "sso" | "policies" | "plugins" | "audit";
type SectionKey = "members" | "projects" | "sso" | "policies" | "plugins" | "audit";

const tabs: Array<{ id: TabId; label: string; icon: string }> = [
  { id: "summary", label: "Resumen", icon: "◫" },
  { id: "projects", label: "Proyectos", icon: "◇" },
  { id: "members", label: "Miembros", icon: "◎" },
  { id: "sso", label: "SSO", icon: "⌁" },
  { id: "policies", label: "Políticas", icon: "◆" },
  { id: "plugins", label: "Plugins", icon: "⌘" },
  { id: "audit", label: "Auditoría", icon: "≋" },
];

const roleLabels: Record<OrganizationRole, string> = {
  owner: "Owner",
  admin: "Admin",
  member: "Member",
  auditor: "Auditor",
};

const statusLabels = {
  active: "Activo",
  suspended: "Suspendido",
  invited: "Invitado",
  disabled: "Deshabilitado",
  revoked: "Revocado",
  succeeded: "Correcto",
  denied: "Denegado",
  failed: "Fallido",
} as const;

interface MemberDraft {
  email: string;
  role: OrganizationRole;
  status: "active" | "suspended";
}

interface SsoDraft {
  id?: string;
  name: string;
  protocol: SsoProtocol;
  issuerUrl: string;
  metadataUrl: string;
  entityId: string;
  signInUrl: string;
  certificateFingerprint: string;
  domains: string;
  enabled: boolean;
}

interface PolicyDraft {
  id?: string;
  description: string;
  effect: PolicyEffect;
  actions: string;
  resources: string;
  roles: OrganizationRole[];
  disabled: boolean;
}

const emptyMemberDraft: MemberDraft = { email: "", role: "member", status: "active" };
const emptySsoDraft: SsoDraft = {
  name: "",
  protocol: "oidc",
  issuerUrl: "",
  metadataUrl: "",
  entityId: "",
  signInUrl: "",
  certificateFingerprint: "",
  domains: "",
  enabled: false,
};
const emptyPolicyDraft: PolicyDraft = {
  description: "",
  effect: "deny",
  actions: "",
  resources: "",
  roles: [],
  disabled: false,
};

function readableError(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function formatDate(value: string): string {
  try {
    return new Intl.DateTimeFormat("es-CO", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
  } catch {
    return value;
  }
}

function canonicalList(value: string, lowercase = false): string[] {
  return [...new Set(value
    .split(/[\n,]/)
    .map((entry) => entry.trim())
    .filter(Boolean)
    .map((entry) => lowercase ? entry.toLowerCase() : entry))]
    .sort((left, right) => left.localeCompare(right));
}

function shortHash(value: string): string {
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-8)}` : value;
}

function upsert<T extends { id: string }>(items: T[], item: T): T[] {
  const found = items.some((candidate) => candidate.id === item.id);
  return found
    ? items.map((candidate) => candidate.id === item.id ? item : candidate)
    : [item, ...items];
}

function SectionHeading({ eyebrow, title, copy, action }: {
  eyebrow: string;
  title: string;
  copy: string;
  action?: ReactNode;
}) {
  return (
    <header className="enterprise-section-heading">
      <div>
        <span className="enterprise-eyebrow">{eyebrow}</span>
        <h2>{title}</h2>
        <p>{copy}</p>
      </div>
      {action}
    </header>
  );
}

function EmptyState({ icon, title, copy, action }: {
  icon: string;
  title: string;
  copy: string;
  action?: ReactNode;
}) {
  return (
    <div className="enterprise-empty">
      <span aria-hidden="true">{icon}</span>
      <h3>{title}</h3>
      <p>{copy}</p>
      {action}
    </div>
  );
}

function PermissionWall({ feature }: { feature: string }) {
  return (
    <EmptyState
      icon="◇"
      title={`${feature} con acceso restringido`}
      copy="Tu rol se resolvió con mínimo privilegio. Un owner, admin o auditor puede consultar este apartado."
    />
  );
}

function SectionError({ message }: { message?: string }) {
  return message ? <p className="enterprise-alert error" role="alert">{message}</p> : null;
}

function RoleBadge({ role }: { role: OrganizationRole }) {
  return <span className={`enterprise-role role-${role}`}>{roleLabels[role]}</span>;
}

function Spinner({ label }: { label: string }) {
  return (
    <div className="enterprise-loading" role="status">
      <span className="scene-loader" aria-hidden="true" />
      <strong>{label}</strong>
    </div>
  );
}

export function EnterpriseClient() {
  const router = useRouter();
  const requestSerial = useRef(0);
  const [user, setUser] = useState<AccountUser>();
  const [organizations, setOrganizations] = useState<Organization[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [availableProjects, setAvailableProjects] = useState<ProjectSummary[]>([]);
  const [sessionLoading, setSessionLoading] = useState(hasConfiguredApi);
  const [bootError, setBootError] = useState("");
  const [activeTab, setActiveTab] = useState<TabId>("summary");
  const [showCreateOrganization, setShowCreateOrganization] = useState(false);

  const [detailsLoading, setDetailsLoading] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [role, setRole] = useState<OrganizationRole>("member");
  const [memberListRestricted, setMemberListRestricted] = useState(false);
  const [members, setMembers] = useState<OrganizationMember[]>([]);
  const [projects, setProjects] = useState<OrganizationProject[]>([]);
  const [ssoConnections, setSsoConnections] = useState<SsoConnection[]>([]);
  const [policies, setPolicies] = useState<PolicyRule[]>([]);
  const [plugins, setPlugins] = useState<PluginRegistration[]>([]);
  const [auditPage, setAuditPage] = useState<AuditPage>();
  const [auditCursors, setAuditCursors] = useState([0]);
  const [auditCursorIndex, setAuditCursorIndex] = useState(0);
  const [verification, setVerification] = useState<AuditVerification>();
  const [sectionErrors, setSectionErrors] = useState<Partial<Record<SectionKey, string>>>({});

  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");
  const [actionError, setActionError] = useState("");
  const [memberDraft, setMemberDraft] = useState<MemberDraft>(emptyMemberDraft);
  const [ssoDraft, setSsoDraft] = useState<SsoDraft>(emptySsoDraft);
  const [policyDraft, setPolicyDraft] = useState<PolicyDraft>(emptyPolicyDraft);
  const [pendingPolicyDelete, setPendingPolicyDelete] = useState<string>();
  const [policyDecision, setPolicyDecision] = useState<PolicyDecision>();
  const [pluginStatuses, setPluginStatuses] = useState<Record<string, PluginStatus>>({});

  useEffect(() => {
    if (!hasConfiguredApi) return;
    let cancelled = false;
    setSessionLoading(true);
    getCurrentUser()
      .then(async (currentUser) => {
        if (cancelled) return;
        if (!currentUser) {
          router.replace("/acceso");
          return;
        }
        setUser(currentUser);
        const [organizationItems, projectResult] = await Promise.all([
          listOrganizations(),
          listProjects()
            .then((items) => ({ items, error: "" }))
            .catch((error: unknown) => ({
              items: [] as ProjectSummary[],
              error: readableError(error, "No se pudieron cargar los proyectos disponibles."),
            })),
        ]);
        if (cancelled) return;
        setOrganizations(organizationItems);
        setAvailableProjects(projectResult.items);
        if (projectResult.error) {
          setSectionErrors((current) => ({ ...current, projects: projectResult.error }));
        }
        setSelectedId((current) => (
          organizationItems.some((organization) => organization.id === current)
            ? current
            : organizationItems[0]?.id ?? ""
        ));
      })
      .catch((error: unknown) => {
        if (!cancelled) setBootError(readableError(error, "No se pudo abrir la consola Enterprise."));
      })
      .finally(() => {
        if (!cancelled) setSessionLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [router]);

  useEffect(() => {
    if (!selectedId || !user) return;
    const userId = user.id;
    const serial = ++requestSerial.current;
    let cancelled = false;
    const isCurrent = () => !cancelled && requestSerial.current === serial;

    async function loadDetails() {
      setDetailsLoading(true);
      setNotice("");
      setActionError("");
      setVerification(undefined);
      setPolicyDecision(undefined);
      setAuditCursors([0]);
      setAuditCursorIndex(0);

      const errors: Partial<Record<SectionKey, string>> = {};
      let nextMembers: OrganizationMember[] = [];
      let nextRole: OrganizationRole = "member";
      let restricted = false;

      try {
        nextMembers = await listOrganizationMembers(selectedId);
        const membership = nextMembers.find((member) => member.userId === userId && member.status === "active");
        nextRole = membership?.role ?? "member";
        restricted = !membership;
      } catch (error) {
        nextRole = "member";
        restricted = error instanceof ApiHttpError && error.status === 404;
        if (!restricted) errors.members = readableError(error, "No se pudieron cargar los miembros.");
      }

      const capture = async <T,>(key: SectionKey, operation: () => Promise<T>, fallback: T): Promise<T> => {
        try {
          return await operation();
        } catch (error) {
          errors[key] = readableError(error, `No se pudo cargar ${key}.`);
          return fallback;
        }
      };

      const [nextProjects, nextPlugins] = await Promise.all([
        capture("projects", () => listOrganizationProjects(selectedId), [] as OrganizationProject[]),
        capture("plugins", () => listPluginRegistrations(selectedId), [] as PluginRegistration[]),
      ]);

      let nextSso: SsoConnection[] = [];
      let nextPolicies: PolicyRule[] = [];
      let nextAudit: AuditPage | undefined;
      const canReadGovernance = !restricted && nextRole !== "member";
      if (canReadGovernance) {
        [nextSso, nextPolicies, nextAudit] = await Promise.all([
          capture("sso", () => listSsoConnections(selectedId), [] as SsoConnection[]),
          capture("policies", () => listPolicyRules(selectedId), [] as PolicyRule[]),
          capture("audit", () => listAuditEvents(selectedId, { afterSequence: 0, limit: 25 }), undefined),
        ]);
      }

      if (!isCurrent()) return;
      setMembers(nextMembers);
      setRole(nextRole);
      setMemberListRestricted(restricted);
      setProjects(nextProjects);
      setPlugins(nextPlugins);
      setPluginStatuses(Object.fromEntries(nextPlugins.map((plugin) => [plugin.id, plugin.status])));
      setSsoConnections(nextSso);
      setPolicies(nextPolicies);
      setAuditPage(nextAudit);
      setSectionErrors((current) => ({
        ...(current.projects && availableProjects.length === 0 ? { projects: current.projects } : {}),
        ...errors,
      }));
      setDetailsLoading(false);
    }

    void loadDetails();
    return () => {
      cancelled = true;
    };
  }, [availableProjects.length, reloadToken, selectedId, user]);

  const selectedOrganization = organizations.find((organization) => organization.id === selectedId);
  const canManage = !memberListRestricted && (role === "owner" || role === "admin");
  const canViewGovernance = !memberListRestricted && role !== "member";
  const attachableProjects = useMemo(() => {
    const attached = new Set(projects.map((project) => project.id));
    return availableProjects.filter((project) => project.role === "owner" && !attached.has(project.id));
  }, [availableProjects, projects]);

  const execute = async <T,>(key: string, success: string, operation: () => Promise<T>): Promise<T | undefined> => {
    setBusy(key);
    setActionError("");
    setNotice("");
    try {
      const result = await operation();
      setNotice(success);
      return result;
    } catch (error) {
      setActionError(readableError(error, "La operación no pudo completarse."));
      return undefined;
    } finally {
      setBusy("");
    }
  };

  const createOrganizationFromForm = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const values = new FormData(event.currentTarget);
    const created = await execute("organization", "Organización creada y seleccionada.", () => createOrganization({
      name: String(values.get("name") ?? "").trim(),
      slug: String(values.get("slug") ?? "").trim().toLowerCase(),
    }));
    if (!created) return;
    setOrganizations((current) => [...current, created].sort((left, right) => left.name.localeCompare(right.name)));
    setSelectedId(created.id);
    setShowCreateOrganization(false);
  };

  const saveMember = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!selectedId) return;
    const saved = await execute("member", "Membresía guardada y auditada.", () => (
      setOrganizationMember(selectedId, { ...memberDraft, email: memberDraft.email.trim().toLowerCase() })
    ));
    if (!saved) return;
    setMembers((current) => {
      const found = current.some((member) => member.userId === saved.userId);
      return found
        ? current.map((member) => member.userId === saved.userId ? saved : member)
        : [...current, saved].sort((left, right) => left.email.localeCompare(right.email));
    });
    setMemberDraft(emptyMemberDraft);
    if (saved.userId === user?.id) setRole(saved.role);
  };

  const saveSso = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!selectedId) return;
    const input = {
      name: ssoDraft.name.trim(),
      protocol: ssoDraft.protocol,
      domains: canonicalList(ssoDraft.domains, true),
      enabled: ssoDraft.enabled,
      ...(ssoDraft.metadataUrl.trim() ? { metadataUrl: ssoDraft.metadataUrl.trim() } : {}),
      ...(ssoDraft.protocol === "oidc"
        ? { issuerUrl: ssoDraft.issuerUrl.trim() }
        : {
            entityId: ssoDraft.entityId.trim(),
            signInUrl: ssoDraft.signInUrl.trim(),
            certificateFingerprint: ssoDraft.certificateFingerprint.trim().toLowerCase(),
          }),
    };
    const saved = await execute("sso", "Metadatos SSO guardados. Esto no activa un inicio de sesión.", () => (
      ssoDraft.id
        ? updateSsoConnection(selectedId, ssoDraft.id, input)
        : createSsoConnection(selectedId, input)
    ));
    if (!saved) return;
    setSsoConnections((current) => upsert(current, saved));
    setSsoDraft(emptySsoDraft);
  };

  const savePolicy = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!selectedId) return;
    const input = {
      description: policyDraft.description.trim(),
      effect: policyDraft.effect,
      actions: canonicalList(policyDraft.actions),
      resources: canonicalList(policyDraft.resources),
      conditions: { roles: [...policyDraft.roles].sort() },
      disabled: policyDraft.disabled,
    };
    const saved = await execute("policy", "Política guardada y lista para evaluación determinista.", () => (
      policyDraft.id
        ? updatePolicyRule(selectedId, policyDraft.id, input)
        : createPolicyRule(selectedId, input)
    ));
    if (!saved) return;
    setPolicies((current) => upsert(current, saved));
    setPolicyDraft(emptyPolicyDraft);
  };

  const removePolicy = async (ruleId: string) => {
    if (!selectedId) return;
    const removed = await execute("policy-delete", "Política eliminada y operación auditada.", async () => {
      await deletePolicyRule(selectedId, ruleId);
      return true;
    });
    if (!removed) return;
    setPolicies((current) => current.filter((rule) => rule.id !== ruleId));
    setPendingPolicyDelete(undefined);
  };

  const loadAuditCursor = async (cursor: number, index: number, appendCursor = false) => {
    if (!selectedId) return;
    const page = await execute("audit-page", "Página de auditoría cargada.", () => (
      listAuditEvents(selectedId, { afterSequence: cursor, limit: 25 })
    ));
    if (!page) return;
    setAuditPage(page);
    setAuditCursorIndex(index);
    if (appendCursor) setAuditCursors((current) => [...current.slice(0, index), cursor]);
  };

  if (!hasConfiguredApi) {
    return (
      <main className="enterprise-unavailable">
        <div className="enterprise-lock" aria-hidden="true">FV</div>
        <span className="enterprise-eyebrow">CONTROL PLANE</span>
        <h1>Enterprise vive conectado a tu API</h1>
        <p>El modo demo no inventa organizaciones, roles ni auditoría. Configura <code>NEXT_PUBLIC_API_URL</code> para usar datos reales.</p>
        <Link className="enterprise-button primary" href="/">Volver al dashboard</Link>
      </main>
    );
  }

  if (sessionLoading) return <main className="enterprise-shell"><Spinner label="Validando sesión Enterprise…" /></main>;

  if (bootError) {
    return (
      <main className="enterprise-unavailable">
        <div className="enterprise-lock danger" aria-hidden="true">!</div>
        <span className="enterprise-eyebrow">CONEXIÓN INTERRUMPIDA</span>
        <h1>No pudimos abrir el control plane</h1>
        <p role="alert">{bootError}</p>
        <button className="enterprise-button primary" type="button" onClick={() => window.location.reload()}>Reintentar</button>
      </main>
    );
  }

  return (
    <main className="enterprise-shell">
      <header className="enterprise-topbar">
        <Link className="brand" href="/">
          <span className="brand-mark" aria-hidden="true">FV</span>
          <span>FlowVerse <b>Enterprise</b></span>
        </Link>
        <nav className="enterprise-top-actions" aria-label="Cuenta Enterprise">
          <span className="enterprise-api-state"><i /> API productiva</span>
          <span className="enterprise-user">{user?.displayName}<small>{user?.email}</small></span>
          <button
            className="enterprise-icon-button"
            type="button"
            aria-label="Cerrar sesión"
            onClick={async () => {
              try {
                await logout();
              } finally {
                router.replace("/acceso");
              }
            }}
          >↗</button>
        </nav>
      </header>

      <div className="enterprise-layout">
        <aside className="enterprise-sidebar">
          <div className="enterprise-sidebar-head">
            <span className="enterprise-eyebrow">ORGANIZACIONES</span>
            <button type="button" onClick={() => setShowCreateOrganization(true)} aria-label="Crear organización">＋</button>
          </div>
          {organizations.length ? (
            <div className="enterprise-org-list" role="list" aria-label="Organizaciones disponibles">
              {organizations.map((organization) => (
                <button
                  type="button"
                  role="listitem"
                  className={organization.id === selectedId ? "active" : ""}
                  key={organization.id}
                  onClick={() => {
                    setSelectedId(organization.id);
                    setActiveTab("summary");
                  }}
                >
                  <span aria-hidden="true">{organization.name.slice(0, 2).toUpperCase()}</span>
                  <div><strong>{organization.name}</strong><small>{organization.slug}</small></div>
                  {organization.id === selectedId && <i aria-label="Seleccionada" />}
                </button>
              ))}
            </div>
          ) : (
            <p className="enterprise-sidebar-empty">Aún no hay organizaciones.</p>
          )}
          <div className="enterprise-sidebar-foot">
            <span aria-hidden="true">◇</span>
            <p><strong>Aislamiento por tenant</strong>Los permisos se resuelven en servidor.</p>
          </div>
        </aside>

        <section className="enterprise-main">
          {!selectedOrganization ? (
            <EmptyState
              icon="＋"
              title="Crea tu primera organización"
              copy="Agrupa proyectos, gobierno, plugins registrados y evidencia auditable en un tenant aislado."
              action={<button className="enterprise-button primary" type="button" onClick={() => setShowCreateOrganization(true)}>Crear organización</button>}
            />
          ) : (
            <>
              <header className="enterprise-hero">
                <div className="enterprise-hero-orbit" aria-hidden="true"><i /><i /><i /></div>
                <div className="enterprise-hero-copy">
                  <div className="enterprise-hero-labels">
                    <span className="enterprise-eyebrow">ENTERPRISE CONTROL PLANE</span>
                    <span className={`enterprise-status ${selectedOrganization.status}`}>{statusLabels[selectedOrganization.status]}</span>
                  </div>
                  <h1>{selectedOrganization.name}</h1>
                  <p><code>{selectedOrganization.slug}</code> · Gobierno, identidad y evidencia en un solo lugar.</p>
                </div>
                <div className="enterprise-hero-role">
                  <span>Tu acceso</span>
                  <RoleBadge role={role} />
                  {memberListRestricted && <small>Derivado con mínimo privilegio</small>}
                </div>
                <button
                  className="enterprise-icon-button refresh"
                  type="button"
                  aria-label="Actualizar organización"
                  disabled={detailsLoading}
                  onClick={() => setReloadToken((current) => current + 1)}
                >↻</button>
              </header>

              <div className="enterprise-tabs" role="tablist" aria-label="Secciones de la organización">
                {tabs.map((tab) => (
                  <button
                    id={`enterprise-tab-${tab.id}`}
                    key={tab.id}
                    type="button"
                    role="tab"
                    aria-selected={activeTab === tab.id}
                    aria-controls={`enterprise-panel-${tab.id}`}
                    tabIndex={activeTab === tab.id ? 0 : -1}
                    className={activeTab === tab.id ? "active" : ""}
                    onClick={() => setActiveTab(tab.id)}
                  ><span aria-hidden="true">{tab.icon}</span>{tab.label}</button>
                ))}
              </div>

              {notice && <p className="enterprise-alert success" role="status">✓ {notice}</p>}
              {actionError && <p className="enterprise-alert error" role="alert">{actionError}</p>}

              {detailsLoading ? <Spinner label="Sincronizando el control plane…" /> : (
                <div
                  className="enterprise-panel"
                  id={`enterprise-panel-${activeTab}`}
                  role="tabpanel"
                  aria-labelledby={`enterprise-tab-${activeTab}`}
                >
                  {activeTab === "summary" && (
                    <>
                      <SectionHeading
                        eyebrow="POSTURA OPERATIVA"
                        title="Todo lo importante, de un vistazo"
                        copy="Estado real de los recursos visibles para tu rol actual."
                      />
                      <div className="enterprise-metric-grid">
                        <button type="button" onClick={() => setActiveTab("projects")}>
                          <span className="metric-icon violet">◇</span><strong>{projects.length}</strong><small>Proyectos adscritos</small><i>Ver inventario →</i>
                        </button>
                        <button type="button" onClick={() => setActiveTab("members")}>
                          <span className="metric-icon cyan">◎</span><strong>{memberListRestricted ? "—" : members.length}</strong><small>Identidades visibles</small><i>Revisar acceso →</i>
                        </button>
                        <button type="button" onClick={() => setActiveTab("policies")}>
                          <span className="metric-icon amber">◆</span><strong>{canViewGovernance ? policies.filter((rule) => !rule.disabled).length : "—"}</strong><small>Políticas activas</small><i>Evaluar control →</i>
                        </button>
                        <button type="button" onClick={() => setActiveTab("plugins")}>
                          <span className="metric-icon green">⌘</span><strong>{plugins.length}</strong><small>Plugins registrados</small><i>Ver registro →</i>
                        </button>
                      </div>
                      <div className="enterprise-overview-grid">
                        <article className="enterprise-card posture-card">
                          <header><span className="enterprise-eyebrow">GOBIERNO</span><span className={`enterprise-dot ${canViewGovernance ? "good" : "neutral"}`} /></header>
                          <h3>{canViewGovernance ? "Superficie de control disponible" : "Vista de miembro aplicada"}</h3>
                          <p>{canViewGovernance
                            ? "Puedes consultar identidad, políticas y cadena auditable con el alcance de tu rol."
                            : "La consola oculta mutaciones y datos restringidos de forma predeterminada."}</p>
                          <div className="posture-track"><i style={{ width: canViewGovernance ? "86%" : "42%" }} /></div>
                        </article>
                        <article className="enterprise-card timeline-card">
                          <header><span className="enterprise-eyebrow">ÚLTIMA ACTIVIDAD VISIBLE</span></header>
                          {auditPage?.items[0] ? (
                            <div className="overview-event"><span className={`event-outcome ${auditPage.items[0].outcome}`} />
                              <div><strong>{auditPage.items[0].action}</strong><small>{formatDate(auditPage.items[0].occurredAt)}</small></div>
                              <code>#{auditPage.items[0].sequence}</code>
                            </div>
                          ) : <p>{canViewGovernance ? "Todavía no hay eventos en esta página." : "La actividad requiere un rol de gobierno."}</p>}
                        </article>
                      </div>
                    </>
                  )}

                  {activeTab === "projects" && (
                    <>
                      <SectionHeading
                        eyebrow="INVENTARIO"
                        title="Proyectos del tenant"
                        copy="Adscribe proyectos sin permitir movimientos silenciosos entre organizaciones."
                      />
                      <SectionError message={sectionErrors.projects} />
                      {canManage && (
                        <form
                          className="enterprise-inline-form"
                          onSubmit={async (event) => {
                            event.preventDefault();
                            const projectId = String(new FormData(event.currentTarget).get("projectId") ?? "");
                            if (!projectId) return;
                            const attached = await execute("project", "Proyecto adscrito y operación auditada.", () => (
                              attachProjectToOrganization(selectedId, projectId)
                            ));
                            if (!attached) return;
                            setProjects((current) => upsert(current, attached));
                          }}
                        >
                          <div><span className="enterprise-eyebrow">ADSCRIBIR PROYECTO</span><p>Solo aparecen proyectos donde eres owner.</p></div>
                          <label className="sr-only" htmlFor="attach-project">Proyecto</label>
                          <select id="attach-project" name="projectId" required defaultValue="">
                            <option value="" disabled>{attachableProjects.length ? "Selecciona un proyecto" : "No hay proyectos elegibles"}</option>
                            {attachableProjects.map((project) => <option value={project.id} key={project.id}>{project.name}</option>)}
                          </select>
                          <button className="enterprise-button primary" type="submit" disabled={busy === "project" || !attachableProjects.length}>{busy === "project" ? "Adscribiendo…" : "Adscribir"}</button>
                        </form>
                      )}
                      {projects.length ? (
                        <div className="enterprise-resource-grid">
                          {projects.map((project) => (
                            <article className="enterprise-card project-resource" key={project.id}>
                              <div className="resource-visual" aria-hidden="true"><i /><i /><i /></div>
                              <div className="resource-copy"><span className="enterprise-eyebrow">PROYECTO</span><h3>{project.name}</h3><p>{project.description || "Sin descripción"}</p></div>
                              <Link href={`/proyectos/${project.id}`}>Abrir ↗</Link>
                            </article>
                          ))}
                        </div>
                      ) : <EmptyState icon="◇" title="Sin proyectos adscritos" copy={canManage ? "Elige uno de tus proyectos owner para incorporarlo a este tenant." : "Un owner o admin puede adscribir proyectos a esta organización."} />}
                    </>
                  )}

                  {activeTab === "members" && (
                    <>
                      <SectionHeading eyebrow="IDENTIDAD" title="Miembros y roles" copy="La API aplica mínimo privilegio y protege al último owner activo." />
                      <SectionError message={sectionErrors.members} />
                      {memberListRestricted ? <PermissionWall feature="Directorio de miembros" /> : (
                        <>
                          {canManage && (
                            <form className="enterprise-form compact" onSubmit={saveMember}>
                              <div className="enterprise-form-title"><div><span className="enterprise-eyebrow">GESTIONAR MEMBRESÍA</span><h3>Invita o actualiza por email</h3></div><span className="form-number">01</span></div>
                              <div className="enterprise-fields three">
                                <label><span>Email registrado</span><input type="email" required maxLength={320} value={memberDraft.email} onChange={(event) => setMemberDraft((current) => ({ ...current, email: event.target.value }))} placeholder="persona@empresa.com" /></label>
                                <label><span>Rol</span><select value={memberDraft.role} onChange={(event) => setMemberDraft((current) => ({ ...current, role: event.target.value as OrganizationRole }))}>
                                  {role === "owner" && <option value="owner">Owner</option>}<option value="admin">Admin</option><option value="member">Member</option><option value="auditor">Auditor</option>
                                </select></label>
                                <label><span>Estado</span><select value={memberDraft.status} onChange={(event) => setMemberDraft((current) => ({ ...current, status: event.target.value as MemberDraft["status"] }))}><option value="active">Activo</option><option value="suspended">Suspendido</option></select></label>
                              </div>
                              <button className="enterprise-button primary align-end" type="submit" disabled={busy === "member"}>{busy === "member" ? "Guardando…" : "Guardar membresía"}</button>
                            </form>
                          )}
                          {members.length ? (
                            <div className="enterprise-table-wrap"><table className="enterprise-table"><thead><tr><th>Identidad</th><th>Rol</th><th>Estado</th><th>Ingreso</th>{canManage && <th><span className="sr-only">Acciones</span></th>}</tr></thead><tbody>
                              {members.map((member) => <tr key={member.userId}><td><span className="member-avatar">{member.displayName.slice(0, 2).toUpperCase()}</span><div><strong>{member.displayName}</strong><small>{member.email}</small></div></td><td><RoleBadge role={member.role} /></td><td><span className={`enterprise-status ${member.status}`}>{statusLabels[member.status]}</span></td><td>{formatDate(member.joinedAt ?? member.createdAt)}</td>{canManage && <td><button type="button" className="enterprise-text-button" onClick={() => setMemberDraft({ email: member.email, role: member.role, status: member.status === "suspended" ? "suspended" : "active" })}>Gestionar</button></td>}</tr>)}
                            </tbody></table></div>
                          ) : <EmptyState icon="◎" title="Directorio vacío" copy="No se encontraron membresías visibles." />}
                        </>
                      )}
                    </>
                  )}

                  {activeTab === "sso" && (
                    <>
                      <SectionHeading eyebrow="IDENTIDAD FEDERADA" title="Metadatos SSO" copy="Directorio público para descubrimiento OIDC o SAML, sin material secreto." />
                      {!canViewGovernance ? <PermissionWall feature="Metadatos SSO" /> : (
                        <>
                          <div className="enterprise-boundary"><span aria-hidden="true">ⓘ</span><p><strong>Alcance real:</strong> aquí solo se registran metadatos públicos. No se almacenan secrets, claves privadas ni tokens; esta pantalla no implementa ni afirma un inicio de sesión SSO operativo.</p></div>
                          <SectionError message={sectionErrors.sso} />
                          {canManage && (
                            <form className="enterprise-form" onSubmit={saveSso}>
                              <div className="enterprise-form-title"><div><span className="enterprise-eyebrow">{ssoDraft.id ? "EDITAR METADATA" : "NUEVA CONEXIÓN"}</span><h3>{ssoDraft.id ? "Actualizar conexión" : "Registrar metadata pública"}</h3></div><span className="form-number">02</span></div>
                              <div className="enterprise-fields three">
                                <label><span>Nombre</span><input required maxLength={120} value={ssoDraft.name} onChange={(event) => setSsoDraft((current) => ({ ...current, name: event.target.value }))} placeholder="Identidad corporativa" /></label>
                                <label><span>Protocolo</span><select value={ssoDraft.protocol} onChange={(event) => setSsoDraft((current) => ({ ...current, protocol: event.target.value as SsoProtocol }))}><option value="oidc">OIDC</option><option value="saml">SAML 2.0</option></select></label>
                                <label className="toggle-field"><span>Metadata habilitada</span><input type="checkbox" checked={ssoDraft.enabled} onChange={(event) => setSsoDraft((current) => ({ ...current, enabled: event.target.checked }))} /></label>
                              </div>
                              <div className="enterprise-fields two">
                                <label><span>Dominios (coma o línea)</span><input required value={ssoDraft.domains} onChange={(event) => setSsoDraft((current) => ({ ...current, domains: event.target.value }))} placeholder="empresa.com, filial.com" /></label>
                                <label><span>Metadata URL (opcional)</span><input type="url" value={ssoDraft.metadataUrl} onChange={(event) => setSsoDraft((current) => ({ ...current, metadataUrl: event.target.value }))} placeholder="https://identity.example.com/metadata" /></label>
                              </div>
                              {ssoDraft.protocol === "oidc" ? (
                                <label><span>Issuer HTTPS</span><input type="url" required value={ssoDraft.issuerUrl} onChange={(event) => setSsoDraft((current) => ({ ...current, issuerUrl: event.target.value }))} placeholder="https://identity.example.com" /></label>
                              ) : (
                                <div className="enterprise-fields three">
                                  <label><span>Entity ID</span><input required value={ssoDraft.entityId} onChange={(event) => setSsoDraft((current) => ({ ...current, entityId: event.target.value }))} placeholder="urn:empresa:idp" /></label>
                                  <label><span>Sign-in URL</span><input type="url" required value={ssoDraft.signInUrl} onChange={(event) => setSsoDraft((current) => ({ ...current, signInUrl: event.target.value }))} placeholder="https://identity.example.com/sso" /></label>
                                  <label><span>Fingerprint SHA-256</span><input required pattern="sha256:[0-9a-f]{64}" value={ssoDraft.certificateFingerprint} onChange={(event) => setSsoDraft((current) => ({ ...current, certificateFingerprint: event.target.value }))} placeholder="sha256:…" /></label>
                                </div>
                              )}
                              <div className="enterprise-form-actions">{ssoDraft.id && <button type="button" className="enterprise-button ghost" onClick={() => setSsoDraft(emptySsoDraft)}>Cancelar</button>}<button type="submit" className="enterprise-button primary" disabled={busy === "sso"}>{busy === "sso" ? "Guardando…" : "Guardar metadata"}</button></div>
                            </form>
                          )}
                          {ssoConnections.length ? <div className="enterprise-resource-grid">{ssoConnections.map((connection) => (
                            <article className="enterprise-card sso-resource" key={connection.id}><header><span className="protocol-badge">{connection.protocol.toUpperCase()}</span><span className={`enterprise-status ${connection.enabled ? "active" : "disabled"}`}>{connection.enabled ? "Metadata habilitada" : "Metadata deshabilitada"}</span></header><h3>{connection.name}</h3><p>{connection.domains.join(" · ")}</p><dl><div><dt>{connection.protocol === "oidc" ? "Issuer" : "Entity ID"}</dt><dd>{connection.issuerUrl ?? connection.entityId}</dd></div><div><dt>Actualizada</dt><dd>{formatDate(connection.updatedAt)}</dd></div></dl>{canManage && <button className="enterprise-text-button" type="button" onClick={() => setSsoDraft({ id: connection.id, name: connection.name, protocol: connection.protocol, issuerUrl: connection.issuerUrl ?? "", metadataUrl: connection.metadataUrl ?? "", entityId: connection.entityId ?? "", signInUrl: connection.signInUrl ?? "", certificateFingerprint: connection.certificateFingerprint ?? "", domains: connection.domains.join(", "), enabled: connection.enabled })}>Editar metadata</button>}</article>
                          ))}</div> : <EmptyState icon="⌁" title="Sin metadata SSO" copy="No hay conexiones de descubrimiento registradas." />}
                        </>
                      )}
                    </>
                  )}

                  {activeTab === "policies" && (
                    <>
                      <SectionHeading eyebrow="POLICY ENGINE" title="Políticas explicables" copy="Reglas deterministas con deny por defecto y precedencia explícita de denegación." />
                      {canViewGovernance ? (
                        <>
                          <SectionError message={sectionErrors.policies} />
                          {canManage && (
                            <form className="enterprise-form" onSubmit={savePolicy}>
                              <div className="enterprise-form-title"><div><span className="enterprise-eyebrow">{policyDraft.id ? "EDITAR REGLA" : "NUEVA REGLA"}</span><h3>Define acción, recurso y alcance</h3></div><span className="form-number">03</span></div>
                              <div className="enterprise-fields three">
                                <label><span>Efecto</span><select value={policyDraft.effect} onChange={(event) => setPolicyDraft((current) => ({ ...current, effect: event.target.value as PolicyEffect }))}><option value="deny">Deny</option><option value="allow">Allow</option></select></label>
                                <label><span>Acciones (coma o línea)</span><input required value={policyDraft.actions} onChange={(event) => setPolicyDraft((current) => ({ ...current, actions: event.target.value }))} placeholder="flow:read, run:**" /></label>
                                <label><span>Recursos (coma o línea)</span><input required value={policyDraft.resources} onChange={(event) => setPolicyDraft((current) => ({ ...current, resources: event.target.value }))} placeholder="project/*, flow/**" /></label>
                              </div>
                              <label><span>Descripción</span><input maxLength={500} value={policyDraft.description} onChange={(event) => setPolicyDraft((current) => ({ ...current, description: event.target.value }))} placeholder="Qué protege esta regla" /></label>
                              <fieldset className="enterprise-checks"><legend>Roles condicionados · ninguno significa todos</legend>{(["owner", "admin", "member", "auditor"] as OrganizationRole[]).map((item) => <label key={item}><input type="checkbox" checked={policyDraft.roles.includes(item)} onChange={(event) => setPolicyDraft((current) => ({ ...current, roles: event.target.checked ? [...current.roles, item] : current.roles.filter((roleItem) => roleItem !== item) }))} />{roleLabels[item]}</label>)}<label><input type="checkbox" checked={policyDraft.disabled} onChange={(event) => setPolicyDraft((current) => ({ ...current, disabled: event.target.checked }))} />Guardar deshabilitada</label></fieldset>
                              <div className="enterprise-form-actions">{policyDraft.id && <button type="button" className="enterprise-button ghost" onClick={() => setPolicyDraft(emptyPolicyDraft)}>Cancelar</button>}<button className="enterprise-button primary" type="submit" disabled={busy === "policy"}>{busy === "policy" ? "Guardando…" : "Guardar política"}</button></div>
                            </form>
                          )}
                          {policies.length ? <div className="policy-list">{policies.map((rule) => <article className={`enterprise-card policy-rule ${rule.effect}`} key={rule.id}><div className="policy-effect"><span>{rule.effect === "deny" ? "D" : "A"}</span><small>{rule.effect.toUpperCase()}</small></div><div className="policy-copy"><header><h3>{rule.description || "Regla sin descripción"}</h3>{rule.disabled && <span className="enterprise-status disabled">Deshabilitada</span>}</header><div className="policy-patterns"><div><small>ACCIONES</small>{rule.actions.map((action) => <code key={action}>{action}</code>)}</div><div><small>RECURSOS</small>{rule.resources.map((resource) => <code key={resource}>{resource}</code>)}</div></div><p>Roles: {rule.conditions.roles.length ? rule.conditions.roles.map((item) => roleLabels[item]).join(", ") : "todos"}</p></div>{canManage && <div className="policy-actions"><button className="enterprise-text-button" type="button" onClick={() => setPolicyDraft({ id: rule.id, description: rule.description ?? "", effect: rule.effect, actions: rule.actions.join(", "), resources: rule.resources.join(", "), roles: rule.conditions.roles, disabled: rule.disabled })}>Editar</button>{pendingPolicyDelete === rule.id ? <><button className="enterprise-text-button danger" type="button" disabled={busy === "policy-delete"} onClick={() => void removePolicy(rule.id)}>Confirmar eliminación</button><button className="enterprise-text-button" type="button" onClick={() => setPendingPolicyDelete(undefined)}>Cancelar</button></> : <button className="enterprise-text-button danger" type="button" onClick={() => setPendingPolicyDelete(rule.id)}>Eliminar</button>}</div>}</article>)}</div> : <EmptyState icon="◆" title="Sin políticas" copy="Sin coincidencias, el evaluador deniega por defecto." />}
                        </>
                      ) : <PermissionWall feature="Catálogo de políticas" />}

                      <form className="enterprise-form evaluator" onSubmit={async (event) => {
                        event.preventDefault();
                        const values = new FormData(event.currentTarget);
                        const decision = await execute("evaluate", "Evaluación registrada en auditoría.", () => evaluatePolicy(selectedId, { action: String(values.get("action") ?? "").trim(), resource: String(values.get("resource") ?? "").trim() }));
                        if (decision) setPolicyDecision(decision);
                      }}>
                        <div className="enterprise-form-title"><div><span className="enterprise-eyebrow">SIMULADOR DE DECISIÓN</span><h3>Evalúa como tu identidad autenticada</h3><p>El rol nunca viaja desde el cliente; lo deriva la API.</p></div><span className="form-number">→</span></div>
                        <div className="evaluator-grid"><label><span>Acción exacta</span><input name="action" required maxLength={512} placeholder="flow:publish" /></label><label><span>Recurso exacto</span><input name="resource" required maxLength={512} placeholder="project/checkout/flow/orders" /></label><button className="enterprise-button primary" type="submit" disabled={busy === "evaluate"}>{busy === "evaluate" ? "Evaluando…" : "Evaluar"}</button></div>
                        {policyDecision && <div className={`policy-decision ${policyDecision.allowed ? "allowed" : "denied"}`} role="status"><span>{policyDecision.allowed ? "✓" : "×"}</span><div><strong>{policyDecision.allowed ? "Permitido" : "Denegado"}</strong><p>{policyDecision.reason.replaceAll("_", " ")} · {policyDecision.matchedRuleIds.length} regla(s) coincidente(s)</p></div></div>}
                      </form>
                    </>
                  )}

                  {activeTab === "plugins" && (
                    <>
                      <SectionHeading eyebrow="REGISTRO VERIFICABLE" title="Plugins registrados" copy="Inventario declarativo por checksum y capacidades; no es un runtime de ejecución." />
                      <div className="enterprise-boundary"><span aria-hidden="true">ⓘ</span><p><strong>Alcance real:</strong> registrar o activar conserva metadata de un artefacto verificable. Esta consola no descarga, instala ni ejecuta código de plugins.</p></div>
                      <SectionError message={sectionErrors.plugins} />
                      {canManage && (
                        <form className="enterprise-form" onSubmit={async (event) => {
                          event.preventDefault();
                          const values = new FormData(event.currentTarget);
                          const saved = await execute("plugin", "Plugin registrado como metadata verificable; no se ejecutó código.", () => registerPlugin(selectedId, {
                            pluginKey: String(values.get("pluginKey") ?? "").trim().toLowerCase(),
                            version: String(values.get("version") ?? "").trim(),
                            status: String(values.get("status")) as "active" | "disabled",
                            sourceUrl: String(values.get("sourceUrl") ?? "").trim(),
                            checksum: String(values.get("checksum") ?? "").trim().toLowerCase(),
                            capabilities: canonicalList(String(values.get("capabilities") ?? ""), true),
                          }));
                          if (!saved) return;
                          setPlugins((current) => upsert(current, saved));
                          setPluginStatuses((current) => ({ ...current, [saved.id]: saved.status }));
                          event.currentTarget.reset();
                        }}>
                          <div className="enterprise-form-title"><div><span className="enterprise-eyebrow">NUEVO REGISTRO</span><h3>Declara un artefacto verificable</h3></div><span className="form-number">04</span></div>
                          <div className="enterprise-fields three"><label><span>Plugin key</span><input name="pluginKey" required maxLength={128} pattern="[a-z0-9][a-z0-9._-]*[a-z0-9]" placeholder="flowverse.audit-export" /></label><label><span>Versión semántica</span><input name="version" required placeholder="1.0.0" /></label><label><span>Estado inicial</span><select name="status" defaultValue="disabled"><option value="disabled">Deshabilitado</option><option value="active">Activo</option></select></label></div>
                          <label><span>URL HTTPS u OCI del artefacto</span><input name="sourceUrl" required placeholder="oci://registry.example.com/flowverse/plugin:1.0.0" /></label>
                          <label><span>Checksum SHA-256</span><input name="checksum" required pattern="sha256:[0-9a-f]{64}" placeholder="sha256:…" /></label>
                          <label><span>Capacidades declaradas (coma o línea)</span><textarea name="capabilities" rows={2} placeholder="flow:read, audit:export" /></label>
                          <button className="enterprise-button primary align-end" type="submit" disabled={busy === "plugin"}>{busy === "plugin" ? "Registrando…" : "Registrar plugin"}</button>
                        </form>
                      )}
                      {plugins.length ? <div className="enterprise-resource-grid">{plugins.map((plugin) => <article className="enterprise-card plugin-resource" key={plugin.id}><header><div className="plugin-cube" aria-hidden="true">⌘</div><div><h3>{plugin.pluginKey}</h3><p>v{plugin.version}</p></div><span className={`enterprise-status ${plugin.status}`}>{statusLabels[plugin.status]}</span></header><dl><div><dt>Checksum</dt><dd><code title={plugin.checksum}>{shortHash(plugin.checksum)}</code></dd></div><div><dt>Capacidades</dt><dd>{plugin.capabilities.length ? plugin.capabilities.join(" · ") : "Ninguna declarada"}</dd></div><div><dt>Fuente</dt><dd className="truncate" title={plugin.sourceUrl}>{plugin.sourceUrl}</dd></div></dl>{canManage && plugin.status !== "revoked" && <div className="plugin-status-control"><label htmlFor={`plugin-status-${plugin.id}`}>Cambiar estado</label><select id={`plugin-status-${plugin.id}`} value={pluginStatuses[plugin.id] ?? plugin.status} onChange={(event) => setPluginStatuses((current) => ({ ...current, [plugin.id]: event.target.value as PluginStatus }))}><option value="active">Activo</option><option value="disabled">Deshabilitado</option><option value="revoked">Revocado (terminal)</option></select><button className="enterprise-button ghost" type="button" disabled={busy === `plugin-${plugin.id}` || pluginStatuses[plugin.id] === plugin.status} onClick={async () => {
                          const nextStatus = pluginStatuses[plugin.id] ?? plugin.status;
                          if (nextStatus === "revoked" && !window.confirm("Revocar es terminal e inmutable. ¿Continuar?")) return;
                          const updated = await execute(`plugin-${plugin.id}`, "Estado de registro actualizado; no se ejecutó código.", () => setPluginStatus(selectedId, plugin.id, nextStatus));
                          if (updated) setPlugins((current) => upsert(current, updated));
                        }}>Aplicar</button></div>}</article>)}</div> : <EmptyState icon="⌘" title="Registro vacío" copy="No hay plugins declarados para esta organización." />}
                    </>
                  )}

                  {activeTab === "audit" && (
                    <>
                      <SectionHeading
                        eyebrow="EVIDENCIA APPEND-ONLY"
                        title="Cadena de auditoría"
                        copy="Eventos paginados por secuencia y verificación contra el checkpoint persistido."
                        action={canViewGovernance ? <button className="enterprise-button primary" type="button" disabled={busy === "verify"} onClick={async () => {
                          const result = await execute("verify", "Verificación de integridad finalizada.", () => verifyAudit(selectedId));
                          if (result) setVerification(result);
                        }}>{busy === "verify" ? "Verificando…" : "Verificar cadena"}</button> : undefined}
                      />
                      {!canViewGovernance ? <PermissionWall feature="Auditoría" /> : (
                        <>
                          <SectionError message={sectionErrors.audit} />
                          {verification && <div className={`audit-verification ${verification.valid ? "valid" : "invalid"}`} role="status"><span>{verification.valid ? "✓" : "!"}</span><div><strong>{verification.valid ? "Cadena íntegra" : "Integridad comprometida"}</strong><p>{verification.eventCount} eventos · checkpoint #{verification.checkpoint.lastSequence}</p>{verification.failure && <small>{verification.failure.reason}</small>}</div><code title={verification.checkpoint.lastHash}>{shortHash(verification.checkpoint.lastHash)}</code></div>}
                          {auditPage?.items.length ? <div className="audit-stream">{auditPage.items.map((event) => <article key={event.id}><div className="audit-sequence"><span>#{event.sequence}</span><i className={event.outcome} /></div><div className="audit-event"><header><h3>{event.action}</h3><span className={`enterprise-status ${event.outcome}`}>{statusLabels[event.outcome]}</span></header><p><strong>{event.resourceType}</strong> · {event.resourceId}</p><footer><time dateTime={event.occurredAt}>{formatDate(event.occurredAt)}</time><code title={event.hash}>{shortHash(event.hash)}</code></footer></div></article>)}</div> : <EmptyState icon="≋" title="Sin eventos en esta página" copy="La cadena no contiene actividad visible para este cursor." />}
                          {auditPage && <nav className="audit-pagination" aria-label="Paginación de auditoría"><button className="enterprise-button ghost" type="button" disabled={auditCursorIndex === 0 || busy === "audit-page"} onClick={() => void loadAuditCursor(auditCursors[auditCursorIndex - 1] ?? 0, auditCursorIndex - 1)}>← Anterior</button><span>Página {auditCursorIndex + 1}<small>después de #{auditPage.afterSequence}</small></span><button className="enterprise-button ghost" type="button" disabled={!auditPage.hasMore || busy === "audit-page"} onClick={() => void loadAuditCursor(auditPage.nextAfterSequence, auditCursorIndex + 1, true)}>Siguiente →</button></nav>}
                        </>
                      )}
                    </>
                  )}
                </div>
              )}
            </>
          )}
        </section>
      </div>

      {showCreateOrganization && (
        <div className="enterprise-modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && setShowCreateOrganization(false)}>
          <section className="enterprise-modal" role="dialog" aria-modal="true" aria-labelledby="organization-dialog-title" onKeyDown={(event) => event.key === "Escape" && setShowCreateOrganization(false)}>
            <header><div><span className="enterprise-eyebrow">NUEVO TENANT</span><h2 id="organization-dialog-title">Crear organización</h2></div><button type="button" aria-label="Cerrar" onClick={() => setShowCreateOrganization(false)}>×</button></header>
            <form onSubmit={createOrganizationFromForm}><label><span>Nombre visible</span><input name="name" autoFocus required minLength={1} maxLength={120} placeholder="Acme Operations" /></label><label><span>Slug único</span><input name="slug" required minLength={1} maxLength={63} pattern="[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?" placeholder="acme-operations" /><small>Solo minúsculas, números y guiones.</small></label><button className="enterprise-button primary" type="submit" disabled={busy === "organization"}>{busy === "organization" ? "Creando…" : "Crear tenant"}</button></form>
          </section>
        </div>
      )}
    </main>
  );
}
