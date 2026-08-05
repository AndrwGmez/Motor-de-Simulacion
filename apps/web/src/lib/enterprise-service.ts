import { apiFetch, httpError } from "./api-client";

export type OrganizationRole = "owner" | "admin" | "member" | "auditor";
export type OrganizationStatus = "active" | "suspended";
export type MembershipStatus = "invited" | "active" | "suspended";
export type SsoProtocol = "oidc" | "saml";
export type PolicyEffect = "allow" | "deny";
export type PolicyDecisionReason = "explicit_allow" | "explicit_deny" | "no_matching_rule";
export type PluginStatus = "active" | "disabled" | "revoked";
export type AuditOutcome = "succeeded" | "denied" | "failed";

export interface Organization {
  id: string;
  slug: string;
  name: string;
  status: OrganizationStatus;
  createdAt: string;
  updatedAt: string;
}

export interface OrganizationMember {
  organizationId: string;
  userId: string;
  email: string;
  displayName: string;
  role: OrganizationRole;
  status: MembershipStatus;
  createdAt: string;
  joinedAt?: string;
}

/**
 * El contrato de SSO solo contiene metadatos públicos de descubrimiento. Este
 * tipo no admite client secrets, claves, tokens ni certificados privados.
 */
export interface SsoConnection {
  id: string;
  organizationId: string;
  name: string;
  protocol: SsoProtocol;
  issuerUrl?: string;
  metadataUrl?: string;
  entityId?: string;
  signInUrl?: string;
  certificateFingerprint?: string;
  domains: string[];
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface PolicyRule {
  id: string;
  organizationId: string;
  description?: string;
  effect: PolicyEffect;
  actions: string[];
  resources: string[];
  conditions: { roles: OrganizationRole[] };
  disabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface PolicyDecision {
  allowed: boolean;
  effect: PolicyEffect;
  reason: PolicyDecisionReason;
  matchedRuleIds: string[];
}

export interface PluginRegistration {
  id: string;
  organizationId: string;
  pluginKey: string;
  version: string;
  status: PluginStatus;
  sourceUrl: string;
  checksum: string;
  capabilities: string[];
  installedBy?: string;
  createdAt: string;
  updatedAt: string;
}

export interface AuditEvent {
  id: string;
  organizationId: string;
  sequence: number;
  actorId?: string;
  action: string;
  resourceType: string;
  resourceId: string;
  outcome: AuditOutcome;
  requestId?: string;
  sourceIp?: string;
  metadata: Readonly<Record<string, unknown>>;
  occurredAt: string;
  previousHash: string;
  hash: string;
}

export interface AuditPage {
  items: AuditEvent[];
  afterSequence: number;
  nextAfterSequence: number;
  limit: number;
  hasMore: boolean;
}

export interface AuditCheckpoint {
  organizationId: string;
  lastSequence: number;
  lastHash: string;
}

export interface AuditVerification {
  valid: boolean;
  eventCount: number;
  checkpoint: AuditCheckpoint;
  failure?: { index?: number; eventId?: string; reason: string };
}

export interface OrganizationProject {
  id: string;
  name: string;
  description: string;
  role?: "owner" | "editor" | "viewer";
  createdAt: string;
  updatedAt: string;
}

export interface OrganizationCreateInput {
  slug: string;
  name: string;
}

export interface OrganizationMemberInput {
  email: string;
  role: OrganizationRole;
  status?: "active" | "suspended";
}

export interface SsoConnectionInput {
  name: string;
  protocol: SsoProtocol;
  issuerUrl?: string;
  metadataUrl?: string;
  entityId?: string;
  signInUrl?: string;
  certificateFingerprint?: string;
  domains: string[];
  enabled: boolean;
}

export interface PolicyRuleInput {
  description?: string;
  effect: PolicyEffect;
  actions: string[];
  resources: string[];
  conditions: { roles: OrganizationRole[] };
  disabled: boolean;
}

export interface PluginRegistrationInput {
  pluginKey: string;
  version: string;
  status?: "active" | "disabled";
  sourceUrl: string;
  checksum: string;
  capabilities: string[];
}

export class EnterpriseContractError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "EnterpriseContractError";
  }
}

type JsonRecord = Record<string, unknown>;

function invalid(path: string): never {
  throw new EnterpriseContractError(`La respuesta Enterprise no cumple el contrato en ${path}.`);
}

function record(value: unknown, path: string): JsonRecord {
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalid(path);
  return value as JsonRecord;
}

function unwrap(value: unknown): unknown {
  const candidate = value && typeof value === "object" && !Array.isArray(value)
    ? value as JsonRecord
    : undefined;
  return candidate && "data" in candidate ? candidate.data : value;
}

function text(value: unknown, path: string): string {
  if (typeof value !== "string") return invalid(path);
  return value;
}

function optionalText(value: unknown, path: string): string | undefined {
  if (value === undefined || value === null || value === "") return undefined;
  return text(value, path);
}

function timestamp(value: unknown, path: string): string {
  const result = text(value, path);
  if (!Number.isFinite(Date.parse(result))) return invalid(path);
  return result;
}

function bool(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") return invalid(path);
  return value;
}

function integer(value: unknown, path: string, minimum = 0): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum) return invalid(path);
  return value as number;
}

function choice<T extends string>(value: unknown, values: readonly T[], path: string): T {
  if (typeof value !== "string" || !values.includes(value as T)) return invalid(path);
  return value as T;
}

function stringList(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) return invalid(path);
  return value.map((entry, index) => text(entry, `${path}[${index}]`));
}

function itemList<T>(value: unknown, path: string, parser: (entry: unknown, path: string) => T): T[] {
  const payload = record(unwrap(value), path);
  if (!Array.isArray(payload.items)) return invalid(`${path}.items`);
  return payload.items.map((entry, index) => parser(entry, `${path}.items[${index}]`));
}

function parseOrganization(value: unknown, path = "organization"): Organization {
  const item = record(unwrap(value), path);
  return {
    id: text(item.id, `${path}.id`),
    slug: text(item.slug, `${path}.slug`),
    name: text(item.name, `${path}.name`),
    status: choice(item.status, ["active", "suspended"] as const, `${path}.status`),
    createdAt: timestamp(item.createdAt, `${path}.createdAt`),
    updatedAt: timestamp(item.updatedAt, `${path}.updatedAt`),
  };
}

function parseMember(value: unknown, path: string): OrganizationMember {
  const item = record(value, path);
  return {
    organizationId: text(item.organizationId, `${path}.organizationId`),
    userId: text(item.userId, `${path}.userId`),
    email: text(item.email, `${path}.email`),
    displayName: text(item.displayName, `${path}.displayName`),
    role: choice(item.role, ["owner", "admin", "member", "auditor"] as const, `${path}.role`),
    status: choice(item.status, ["invited", "active", "suspended"] as const, `${path}.status`),
    createdAt: timestamp(item.createdAt, `${path}.createdAt`),
    joinedAt: item.joinedAt === undefined ? undefined : timestamp(item.joinedAt, `${path}.joinedAt`),
  };
}

function parseSsoConnection(value: unknown, path: string): SsoConnection {
  const item = record(unwrap(value), path);
  return {
    id: text(item.id, `${path}.id`),
    organizationId: text(item.organizationId, `${path}.organizationId`),
    name: text(item.name, `${path}.name`),
    protocol: choice(item.protocol, ["oidc", "saml"] as const, `${path}.protocol`),
    issuerUrl: optionalText(item.issuerUrl, `${path}.issuerUrl`),
    metadataUrl: optionalText(item.metadataUrl, `${path}.metadataUrl`),
    entityId: optionalText(item.entityId, `${path}.entityId`),
    signInUrl: optionalText(item.signInUrl, `${path}.signInUrl`),
    certificateFingerprint: optionalText(item.certificateFingerprint, `${path}.certificateFingerprint`),
    domains: stringList(item.domains, `${path}.domains`),
    enabled: bool(item.enabled, `${path}.enabled`),
    createdAt: timestamp(item.createdAt, `${path}.createdAt`),
    updatedAt: timestamp(item.updatedAt, `${path}.updatedAt`),
  };
}

function parsePolicyRule(value: unknown, path: string): PolicyRule {
  const item = record(unwrap(value), path);
  const conditions = record(item.conditions, `${path}.conditions`);
  return {
    id: text(item.id, `${path}.id`),
    organizationId: text(item.organizationId, `${path}.organizationId`),
    description: optionalText(item.description, `${path}.description`),
    effect: choice(item.effect, ["allow", "deny"] as const, `${path}.effect`),
    actions: stringList(item.actions, `${path}.actions`),
    resources: stringList(item.resources, `${path}.resources`),
    conditions: {
      roles: conditions.roles === undefined
        ? []
        : stringList(conditions.roles, `${path}.conditions.roles`).map((role, index) => (
          choice(role, ["owner", "admin", "member", "auditor"] as const, `${path}.conditions.roles[${index}]`)
        )),
    },
    disabled: bool(item.disabled, `${path}.disabled`),
    createdAt: timestamp(item.createdAt, `${path}.createdAt`),
    updatedAt: timestamp(item.updatedAt, `${path}.updatedAt`),
  };
}

function parsePolicyDecision(value: unknown, path = "policyDecision"): PolicyDecision {
  const item = record(unwrap(value), path);
  return {
    allowed: bool(item.allowed, `${path}.allowed`),
    effect: choice(item.effect, ["allow", "deny"] as const, `${path}.effect`),
    reason: choice(
      item.reason,
      ["explicit_allow", "explicit_deny", "no_matching_rule"] as const,
      `${path}.reason`,
    ),
    matchedRuleIds: stringList(item.matchedRuleIds, `${path}.matchedRuleIds`),
  };
}

function parsePlugin(value: unknown, path: string): PluginRegistration {
  const item = record(unwrap(value), path);
  return {
    id: text(item.id, `${path}.id`),
    organizationId: text(item.organizationId, `${path}.organizationId`),
    pluginKey: text(item.pluginKey, `${path}.pluginKey`),
    version: text(item.version, `${path}.version`),
    status: choice(item.status, ["active", "disabled", "revoked"] as const, `${path}.status`),
    sourceUrl: text(item.sourceUrl, `${path}.sourceUrl`),
    checksum: text(item.checksum, `${path}.checksum`),
    capabilities: stringList(item.capabilities, `${path}.capabilities`),
    installedBy: optionalText(item.installedBy, `${path}.installedBy`),
    createdAt: timestamp(item.createdAt, `${path}.createdAt`),
    updatedAt: timestamp(item.updatedAt, `${path}.updatedAt`),
  };
}

function parseAuditEvent(value: unknown, path: string): AuditEvent {
  const item = record(value, path);
  return {
    id: text(item.id, `${path}.id`),
    organizationId: text(item.organizationId, `${path}.organizationId`),
    sequence: integer(item.sequence, `${path}.sequence`, 1),
    actorId: optionalText(item.actorId, `${path}.actorId`),
    action: text(item.action, `${path}.action`),
    resourceType: text(item.resourceType, `${path}.resourceType`),
    resourceId: text(item.resourceId, `${path}.resourceId`),
    outcome: choice(item.outcome, ["succeeded", "denied", "failed"] as const, `${path}.outcome`),
    requestId: optionalText(item.requestId, `${path}.requestId`),
    sourceIp: optionalText(item.sourceIp, `${path}.sourceIp`),
    metadata: { ...record(item.metadata, `${path}.metadata`) },
    occurredAt: timestamp(item.occurredAt, `${path}.occurredAt`),
    previousHash: text(item.previousHash, `${path}.previousHash`),
    hash: text(item.hash, `${path}.hash`),
  };
}

function parseAuditPage(value: unknown): AuditPage {
  const item = record(unwrap(value), "auditPage");
  if (!Array.isArray(item.items)) return invalid("auditPage.items");
  return {
    items: item.items.map((entry, index) => parseAuditEvent(entry, `auditPage.items[${index}]`)),
    afterSequence: integer(item.afterSequence, "auditPage.afterSequence"),
    nextAfterSequence: integer(item.nextAfterSequence, "auditPage.nextAfterSequence"),
    limit: integer(item.limit, "auditPage.limit", 1),
    hasMore: bool(item.hasMore, "auditPage.hasMore"),
  };
}

function parseAuditVerification(value: unknown): AuditVerification {
  const item = record(unwrap(value), "auditVerification");
  const checkpoint = record(item.checkpoint, "auditVerification.checkpoint");
  const failure = item.failure === undefined
    ? undefined
    : record(item.failure, "auditVerification.failure");
  return {
    valid: bool(item.valid, "auditVerification.valid"),
    eventCount: integer(item.eventCount, "auditVerification.eventCount"),
    checkpoint: {
      organizationId: text(checkpoint.organizationId, "auditVerification.checkpoint.organizationId"),
      lastSequence: integer(checkpoint.lastSequence, "auditVerification.checkpoint.lastSequence"),
      lastHash: text(checkpoint.lastHash, "auditVerification.checkpoint.lastHash"),
    },
    failure: failure
      ? {
          index: failure.index === undefined ? undefined : integer(failure.index, "auditVerification.failure.index"),
          eventId: optionalText(failure.eventId, "auditVerification.failure.eventId"),
          reason: text(failure.reason, "auditVerification.failure.reason"),
        }
      : undefined,
  };
}

function parseProject(value: unknown, path: string): OrganizationProject {
  const item = record(unwrap(value), path);
  return {
    id: text(item.id, `${path}.id`),
    name: text(item.name, `${path}.name`),
    description: optionalText(item.description, `${path}.description`) ?? "",
    role: item.role === undefined
      ? undefined
      : choice(item.role, ["owner", "editor", "viewer"] as const, `${path}.role`),
    createdAt: timestamp(item.createdAt, `${path}.createdAt`),
    updatedAt: timestamp(item.updatedAt, `${path}.updatedAt`),
  };
}

async function json(response: Response): Promise<unknown> {
  return response.json() as Promise<unknown>;
}

async function request(
  path: string,
  fallback: string,
  init: RequestInit = {},
): Promise<Response> {
  const response = await apiFetch(path, init);
  if (!response.ok) throw await httpError(response, fallback);
  return response;
}

function body(value: unknown): RequestInit {
  return { body: JSON.stringify(value) };
}

function organizationPath(organizationId: string, suffix = ""): string {
  return `/v1/organizations/${encodeURIComponent(organizationId)}${suffix}`;
}

export async function listOrganizations(): Promise<Organization[]> {
  const response = await request("/v1/organizations", "No se pudieron cargar las organizaciones.");
  return itemList(await json(response), "organizations", parseOrganization);
}

export async function createOrganization(input: OrganizationCreateInput): Promise<Organization> {
  const response = await request("/v1/organizations", "No se pudo crear la organización.", {
    method: "POST",
    ...body({ slug: input.slug, name: input.name }),
  });
  return parseOrganization(await json(response));
}

export async function listOrganizationMembers(organizationId: string): Promise<OrganizationMember[]> {
  const response = await request(
    organizationPath(organizationId, "/members"),
    "No se pudieron cargar los miembros.",
  );
  return itemList(await json(response), "members", parseMember);
}

export async function setOrganizationMember(
  organizationId: string,
  input: OrganizationMemberInput,
): Promise<OrganizationMember> {
  const response = await request(
    organizationPath(organizationId, "/members"),
    "No se pudo guardar la membresía.",
    {
      method: "POST",
      ...body({ email: input.email, role: input.role, status: input.status ?? "active" }),
    },
  );
  return parseMember(await json(response), "member");
}

export async function listSsoConnections(organizationId: string): Promise<SsoConnection[]> {
  const response = await request(
    organizationPath(organizationId, "/sso-connections"),
    "No se pudieron cargar los metadatos SSO.",
  );
  return itemList(await json(response), "ssoConnections", parseSsoConnection);
}

function publicSsoInput(input: SsoConnectionInput): SsoConnectionInput {
  return {
    name: input.name,
    protocol: input.protocol,
    ...(input.issuerUrl ? { issuerUrl: input.issuerUrl } : {}),
    ...(input.metadataUrl ? { metadataUrl: input.metadataUrl } : {}),
    ...(input.entityId ? { entityId: input.entityId } : {}),
    ...(input.signInUrl ? { signInUrl: input.signInUrl } : {}),
    ...(input.certificateFingerprint ? { certificateFingerprint: input.certificateFingerprint } : {}),
    domains: input.domains,
    enabled: input.enabled,
  };
}

export async function createSsoConnection(
  organizationId: string,
  input: SsoConnectionInput,
): Promise<SsoConnection> {
  const response = await request(
    organizationPath(organizationId, "/sso-connections"),
    "No se pudieron guardar los metadatos SSO.",
    { method: "POST", ...body(publicSsoInput(input)) },
  );
  return parseSsoConnection(await json(response), "ssoConnection");
}

export async function updateSsoConnection(
  organizationId: string,
  connectionId: string,
  input: SsoConnectionInput,
): Promise<SsoConnection> {
  const response = await request(
    organizationPath(organizationId, `/sso-connections/${encodeURIComponent(connectionId)}`),
    "No se pudieron actualizar los metadatos SSO.",
    { method: "PUT", ...body(publicSsoInput(input)) },
  );
  return parseSsoConnection(await json(response), "ssoConnection");
}

export async function listPolicyRules(organizationId: string): Promise<PolicyRule[]> {
  const response = await request(
    organizationPath(organizationId, "/policy-rules"),
    "No se pudieron cargar las políticas.",
  );
  return itemList(await json(response), "policyRules", parsePolicyRule);
}

function policyInput(input: PolicyRuleInput): PolicyRuleInput {
  return {
    ...(input.description ? { description: input.description } : {}),
    effect: input.effect,
    actions: input.actions,
    resources: input.resources,
    conditions: { roles: input.conditions.roles },
    disabled: input.disabled,
  };
}

export async function createPolicyRule(
  organizationId: string,
  input: PolicyRuleInput,
): Promise<PolicyRule> {
  const response = await request(
    organizationPath(organizationId, "/policy-rules"),
    "No se pudo crear la política.",
    { method: "POST", ...body(policyInput(input)) },
  );
  return parsePolicyRule(await json(response), "policyRule");
}

export async function updatePolicyRule(
  organizationId: string,
  ruleId: string,
  input: PolicyRuleInput,
): Promise<PolicyRule> {
  const response = await request(
    organizationPath(organizationId, `/policy-rules/${encodeURIComponent(ruleId)}`),
    "No se pudo actualizar la política.",
    { method: "PUT", ...body(policyInput(input)) },
  );
  return parsePolicyRule(await json(response), "policyRule");
}

export async function deletePolicyRule(organizationId: string, ruleId: string): Promise<void> {
  await request(
    organizationPath(organizationId, `/policy-rules/${encodeURIComponent(ruleId)}`),
    "No se pudo eliminar la política.",
    { method: "DELETE" },
  );
}

export async function evaluatePolicy(
  organizationId: string,
  input: { action: string; resource: string },
): Promise<PolicyDecision> {
  const response = await request(
    organizationPath(organizationId, "/policy/evaluate"),
    "No se pudo evaluar la política.",
    { method: "POST", ...body({ action: input.action, resource: input.resource }) },
  );
  return parsePolicyDecision(await json(response));
}

export async function listPluginRegistrations(organizationId: string): Promise<PluginRegistration[]> {
  const response = await request(
    organizationPath(organizationId, "/plugins"),
    "No se pudieron cargar los plugins registrados.",
  );
  return itemList(await json(response), "plugins", parsePlugin);
}

export async function registerPlugin(
  organizationId: string,
  input: PluginRegistrationInput,
): Promise<PluginRegistration> {
  const response = await request(
    organizationPath(organizationId, "/plugins"),
    "No se pudo registrar el plugin.",
    {
      method: "POST",
      ...body({
        pluginKey: input.pluginKey,
        version: input.version,
        status: input.status ?? "disabled",
        sourceUrl: input.sourceUrl,
        checksum: input.checksum,
        capabilities: input.capabilities,
      }),
    },
  );
  return parsePlugin(await json(response), "plugin");
}

export async function setPluginStatus(
  organizationId: string,
  registrationId: string,
  status: PluginStatus,
): Promise<PluginRegistration> {
  const response = await request(
    organizationPath(organizationId, `/plugins/${encodeURIComponent(registrationId)}`),
    "No se pudo cambiar el estado del plugin.",
    { method: "PATCH", ...body({ status }) },
  );
  return parsePlugin(await json(response), "plugin");
}

export async function listAuditEvents(
  organizationId: string,
  options: { afterSequence?: number; limit?: number } = {},
): Promise<AuditPage> {
  const query = new URLSearchParams({
    afterSequence: String(options.afterSequence ?? 0),
    limit: String(options.limit ?? 25),
  });
  const response = await request(
    organizationPath(organizationId, `/audit?${query.toString()}`),
    "No se pudo cargar la auditoría.",
  );
  return parseAuditPage(await json(response));
}

export async function verifyAudit(organizationId: string): Promise<AuditVerification> {
  const response = await request(
    organizationPath(organizationId, "/audit/verify"),
    "No se pudo verificar la cadena de auditoría.",
  );
  return parseAuditVerification(await json(response));
}

export async function listOrganizationProjects(organizationId: string): Promise<OrganizationProject[]> {
  const response = await request(
    organizationPath(organizationId, "/projects"),
    "No se pudieron cargar los proyectos de la organización.",
  );
  return itemList(await json(response), "organizationProjects", parseProject);
}

export async function attachProjectToOrganization(
  organizationId: string,
  projectId: string,
): Promise<OrganizationProject> {
  const response = await request(
    organizationPath(organizationId, `/projects/${encodeURIComponent(projectId)}/attach`),
    "No se pudo adscribir el proyecto.",
    { method: "POST" },
  );
  return parseProject(await json(response), "project");
}
