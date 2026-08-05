import { afterEach, describe, expect, it, vi } from "vitest";

const organizationId = "11111111-1111-4111-8111-111111111111";
const userId = "22222222-2222-4222-8222-222222222222";
const connectionId = "33333333-3333-4333-8333-333333333333";
const now = "2026-08-04T12:00:00.000Z";

function response(payload: unknown, status = 200): Response {
  return new Response(status === 204 ? null : JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("enterprise service", () => {
  it("acepta el envelope data y elimina propiedades fuera del contrato", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({
      data: {
        items: [{
          id: organizationId,
          slug: "acme-labs",
          name: "Acme Labs",
          status: "active",
          createdAt: now,
          updatedAt: now,
          internalBillingKey: "must-not-leak",
        }],
      },
    })));

    const { listOrganizations } = await import("./enterprise-service");
    await expect(listOrganizations()).resolves.toEqual([{
      id: organizationId,
      slug: "acme-labs",
      name: "Acme Labs",
      status: "active",
      createdAt: now,
      updatedAt: now,
    }]);
  });

  it("rechaza respuestas parcialmente válidas en vez de propagar DTOs corruptos", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({
      items: [{
        id: organizationId,
        organizationId,
        description: "Regla incompleta",
        effect: "permit",
        actions: ["flow:read"],
        resources: ["flow/**"],
        conditions: {},
        disabled: false,
        createdAt: now,
        updatedAt: now,
      }],
    })));

    const { listPolicyRules, EnterpriseContractError } = await import("./enterprise-service");
    await expect(listPolicyRules(organizationId)).rejects.toBeInstanceOf(EnterpriseContractError);
  });

  it("conserva el estado HTTP del 404 de miembros para aplicar mínimo privilegio", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ message: "Not found" }, 404)));

    const { listOrganizationMembers } = await import("./enterprise-service");
    await expect(listOrganizationMembers(organizationId)).rejects.toMatchObject({
      name: "ApiHttpError",
      status: 404,
    });
  });

  it("serializa únicamente metadatos públicos de SSO", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue(response({
      id: connectionId,
      organizationId,
      name: "OIDC corporativo",
      protocol: "oidc",
      issuerUrl: "https://identity.example.com",
      domains: ["example.com"],
      enabled: true,
      createdAt: now,
      updatedAt: now,
      clientSecret: "server-must-never-return-this",
    }, 201));
    vi.stubGlobal("fetch", fetchMock);

    const { createSsoConnection } = await import("./enterprise-service");
    const created = await createSsoConnection(organizationId, {
      name: "OIDC corporativo",
      protocol: "oidc",
      issuerUrl: "https://identity.example.com",
      domains: ["example.com"],
      enabled: true,
      clientSecret: "client-must-never-send-this",
    } as Parameters<typeof createSsoConnection>[1] & { clientSecret: string });

    const request = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(JSON.parse(String(request.body))).toEqual({
      name: "OIDC corporativo",
      protocol: "oidc",
      issuerUrl: "https://identity.example.com",
      domains: ["example.com"],
      enabled: true,
    });
    expect(created).not.toHaveProperty("clientSecret");
  });

  it("parsea páginas de auditoría y construye un cursor estable", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const fetchMock = vi.fn().mockResolvedValue(response({
      items: [{
        id: "44444444-4444-4444-8444-444444444444",
        organizationId,
        sequence: 8,
        actorId: userId,
        action: "organization.policy.evaluate",
        resourceType: "policy_resource",
        resourceId: "flow/checkout",
        outcome: "succeeded",
        metadata: { allowed: true },
        occurredAt: now,
        previousHash: "a".repeat(64),
        hash: "b".repeat(64),
      }],
      afterSequence: 7,
      nextAfterSequence: 8,
      limit: 25,
      hasMore: true,
    }));
    vi.stubGlobal("fetch", fetchMock);

    const { listAuditEvents } = await import("./enterprise-service");
    const page = await listAuditEvents(organizationId, { afterSequence: 7, limit: 25 });
    expect(page.items[0]).toMatchObject({ sequence: 8, outcome: "succeeded" });
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain("afterSequence=7&limit=25");
  });

  it("usa la ruta estabilizada para adscribir un proyecto", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const projectId = "55555555-5555-4555-8555-555555555555";
    const fetchMock = vi.fn().mockResolvedValue(response({
      id: projectId,
      name: "Checkout",
      description: "Flujos críticos",
      ownerId: userId,
      createdAt: now,
      updatedAt: now,
    }));
    vi.stubGlobal("fetch", fetchMock);

    const { attachProjectToOrganization } = await import("./enterprise-service");
    await expect(attachProjectToOrganization(organizationId, projectId)).resolves.toMatchObject({
      id: projectId,
      name: "Checkout",
    });
    expect(fetchMock).toHaveBeenCalledWith(
      `http://api.flowverse.test/v1/organizations/${organizationId}/projects/${projectId}/attach`,
      expect.objectContaining({ method: "POST", credentials: "include" }),
    );
  });
});
