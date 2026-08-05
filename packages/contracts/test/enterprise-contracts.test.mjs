import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const schema = JSON.parse(
  await readFile(new URL("../schemas/enterprise.schema.json", import.meta.url), "utf8")
);
const ajv = new Ajv2020({ allErrors: true, strict: true, validateFormats: true });
addFormats(ajv);
ajv.addSchema(schema);

function validator(definition) {
  const validate = ajv.getSchema(`${schema.$id}#/$defs/${definition}`);
  assert(validate, `No se compiló $defs/${definition}`);
  return validate;
}

test("los DTO Enterprise aceptan respuestas canónicas", () => {
  const member = validator("OrganizationMemberView");
  assert.equal(
    member({
      organizationId: "00000000-0000-4000-8000-000000000001",
      userId: "00000000-0000-4000-8000-000000000002",
      email: "member@example.com",
      displayName: "Member",
      role: "member",
      status: "active",
      createdAt: "2026-08-04T12:00:00Z",
      joinedAt: "2026-08-04T12:00:00Z"
    }),
    true,
    JSON.stringify(member.errors)
  );

  const auditPage = validator("AuditEventList");
  assert.equal(
    auditPage({
      items: [
        {
          id: "00000000-0000-4000-8000-000000000003",
          organizationId: "00000000-0000-4000-8000-000000000001",
          sequence: 1,
          actorId: "00000000-0000-4000-8000-000000000002",
          action: "organization.policy.evaluate",
          resourceType: "policy_resource",
          resourceId: "project:demo",
          outcome: "denied",
          requestId: "request-1",
          sourceIp: "192.0.2.1",
          metadata: { allowed: false },
          occurredAt: "2026-08-04T12:00:00Z",
          previousHash: "0".repeat(64),
          hash: "a".repeat(64)
        }
      ],
      afterSequence: 0,
      nextAfterSequence: 1,
      limit: 100,
      hasMore: false
    }),
    true,
    JSON.stringify(auditPage.errors)
  );
});

test("los DTO Enterprise bloquean escalación de rol y secretos SSO", () => {
  const evaluation = validator("PolicyEvaluationInput");
  assert.equal(
    evaluation({ action: "project.read", resource: "project:demo", role: "owner" }),
    false
  );

  const sso = validator("SSOConnectionInput");
  assert.equal(
    sso({
      name: "Corporate OIDC",
      protocol: "oidc",
      issuerUrl: "https://identity.example.com",
      domains: ["example.com"],
      enabled: true,
      clientSecret: "must-never-enter-the-contract"
    }),
    false
  );
});
