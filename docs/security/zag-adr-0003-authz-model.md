# ADR-0003: RBAC/ABAC Authorization Model for ZAgentGateway

- Status: Proposed
- Date: 2025-01-XX
- Authors: ZAG Security Working Group
- Relates to: ADR-0001 (Token Format), ADR-0002 (mTLS), ADR-0007 (Audit)

## Context

The ZAgentGateway (ZAG) brokers requests between many distributed principals
(tenants, workspaces, pods, agents, IDE connectors) and a set of control
plane resources (sessions, tasks, approvals). A single static role is not
sufficient because:

- Different organizations must be strictly isolated (tenant boundary).
- Operators must be able to escalate to perform sensitive actions
  (e.g. approving a control command) without holding a global admin token.
- Object-level scoping (which workspace / pod / agent a principal may act
  on) cannot be expressed with roles alone.

We need a hybrid model: **RBAC** for coarse action permissions and
**ABAC** for object-level and contextual rules. Every decision must be
**fail-closed**: if the policy engine cannot reach a verdict, the request
is denied and an audit record is written.

## Decision

### 1. Roles (RBAC layer)

Four roles are defined. Roles are additive; a principal MAY hold multiple
roles, in which case the union of capabilities applies, but no role grants
admin over the others.

| Role       | Intended holders                                |
|------------|-------------------------------------------------|
| `viewer`   | Read-only dashboards, logs, audit explorers     |
| `operator` | Day-to-day control plane operations             |
| `approver` | Two-person approval for sensitive operations    |
| `admin`    | Tenant-wide configuration, key rotation, IAM    |

Roles are encoded as the `roles` claim in the delegated token (see
ADR-0001). The gateway MUST reject any token whose `roles` claim contains
an unknown role string with `401 unauthorized_role`.

### 2. Resources

The gateway protects the following resource types. Every resource carries
a `tenant_id` and an `id`. Cross-tenant access is **never** allowed.

| Resource type   | Identifier           | Notes                                |
|-----------------|----------------------|--------------------------------------|
| `tenant`        | `tenant_id`          | Top-level isolation boundary         |
| `workspace`     | `workspace_id`       | Belongs to a tenant                  |
| `pod`           | `pod_id`             | Runtime sandbox; belongs to workspace |
| `agent`         | `agent_id`           | Logical agent; belongs to workspace   |
| `session`       | `session_id`         | Interactive session; belongs to agent |
| `task`          | `task_id`            | Discrete work item                   |
| `ide_connector` | `connector_id`       | IDE bridge principal                 |

### 3. Action matrix

The verb set is closed. New verbs require an ADR update.

| Verb           | viewer | operator | approver | admin |
|----------------|:------:|:--------:|:--------:|:-----:|
| `read`         |   Y    |    Y     |    Y     |   Y   |
| `list`         |   Y    |    Y     |    Y     |   Y   |
| `create`       |        |    Y     |    Y     |   Y   |
| `update`       |        |    Y     |    Y     |   Y   |
| `delete`       |        |    Y     |    Y     |   Y   |
| `approve`      |        |          |    Y     |   Y   |
| `rotate_key`   |        |          |          |   Y   |
| `manage_iam`   |        |          |          |   Y   |
| `audit_export` |        |          |    Y     |   Y   |

`approve` and `audit_export` require the `approver` or `admin` role.
`rotate_key` and `manage_iam` are admin-only and MUST be issued under a
freshly minted, short-lived token whose `auth_time` is within 5 minutes.

### 4. Object-level rules (ABAC layer)

Even when a role allows an action, ABAC rules MAY further restrict access:

1. **Tenant containment.** A principal's `tenant_id` claim MUST equal the
   resource's `tenant_id`. Mismatch yields `403 cross_tenant_denied`.
2. **Workspace scoping.** Operators are bound to a `workspace_ids` claim
   listing zero or more workspaces they may touch. Empty list means
   "no workspace-scoped access". A request referencing a workspace outside
   the list yields `403 workspace_forbidden`.
3. **Pod/agent containment.** A pod/agent MUST belong to a workspace the
   principal may access. The gateway MUST resolve the parent chain
   (`agent_id` -> `workspace_id` -> `tenant_id`) and verify each level.
4. **Session/task ownership.** `session` and `task` resources MUST have
   an `owner_principal` field. Only that principal, anyone with the
   `approver` role on the same tenant, or any `admin` on the same tenant
   may modify them.
5. **Time-bound approvals.** `approve` actions MUST additionally carry
   a valid `approval_grant_id` referencing a still-valid, unconsumed
   grant. Grants expire after 15 minutes by default.
6. **mTLS channel binding.** The client certificate's SAN MUST match the
   principal's `client_cert_fp` claim recorded at token issuance
   (see ADR-0002). A mismatch yields `401 cert_binding_failed`.

### 5. Decision pipeline

For every protected request the gateway runs, in order:

1. Parse and validate the delegated token (ADR-0001). Invalid -> `401`.
2. Verify mTLS and bind the certificate fingerprint (ADR-0002).
   Mismatch -> `401 cert_binding_failed`.
3. Resolve the target resource(s) and load their `tenant_id`.
4. Evaluate the action matrix for `(role, verb, resource_type)`.
   Deny -> `403 role_forbidden` and audit.
5. Evaluate ABAC rules (tenant containment, workspace scope, ownership,
   approval grant, freshness). Any failure -> `403` with a specific code
   and audit.
6. On success, attach an `authz_decision` blob to the request context
   and forward.

Any uncaught exception, timeout, or dependency failure inside the
pipeline is **fail-closed**: the request is denied with
`503 authz_unavailable` and an audit record of severity `critical`
is emitted.

### 6. Error codes

| Code                       | HTTP | Meaning                                  |
|----------------------------|:----:|------------------------------------------|
| `unauthorized_role`        | 401  | Token carries an unknown role            |
| `cert_binding_failed`      | 401  | mTLS fingerprint does not match token    |
| `role_forbidden`           | 403  | Role is not permitted to perform verb    |
| `cross_tenant_denied`      | 403  | Tenant claim does not match resource     |
| `workspace_forbidden`      | 403  | Workspace not in principal's scope list  |
| `ownership_required`       | 403  | Action needs resource ownership          |
| `approval_grant_invalid`   | 403  | Approval grant missing/expired/consumed  |
| `freshness_required`       | 401  | Admin action needs a freshly minted token |
| `authz_unavailable`        | 503  | Policy engine failure (fail-closed)      |

### 7. Audit integration

Every allow and deny decision MUST be recorded per ADR-0007, including:
`principal`, `tenant_id`, `resource_type`, `resource_id`, `verb`,
`decision` (`allow` / `deny`), `deny_reason` if applicable, and
`request_id`. Denials due to engine failure MUST be flagged
`severity=critical`.

### 8. Implementation notes

- Policy is expressed as a small in-process table; the package
  `internal/authz` is responsible for evaluation. No external policy
  engine is required for the skeleton.
- The role matrix and ABAC predicates live in code, signed at release
  time. Runtime policy reload is **out of scope** for this ADR.
- All authorization checks are unit tested. The skeleton ships with
  table-driven tests covering each role/verb/resource combination.

## Consequences

Positive:
- A single ADR describes both RBAC and ABAC and their interplay.
- Cross-tenant access is impossible by construction.
- Admin actions are pinned to short-lived tokens, limiting blast radius
  of stolen credentials.

Negative / Risks:
- ABAC rules live in code, so changing them requires a release. We accept
  this trade-off in exchange for verifiability.
- Per-request policy evaluation adds a small CPU cost; we accept this for
  fail-closed correctness.

## Alternatives considered

- **Pure RBAC.** Rejected: cannot model workspace scope or ownership
  without exploding the role set.
- **OPA / Rego.** Rejected for the skeleton: introduces a separate
  runtime and supply-chain risk. May be revisited later behind the same
  `internal/authz` interface.
- **Static capability strings in tokens.** Rejected: capabilities cannot
  be revoked granularly without reissuing every token.

## Failure modes (mandatory section)

- **Policy engine panic/timeout.** Fail-closed: deny with
  `503 authz_unavailable`; emit `severity=critical` audit.
- **Resource lookup failure.** Fail-closed: deny with `503 target_unknown`.
- **Missing `tenant_id` on a resource.** Fail-closed: deny with
  `403 tenant_unresolved`.
- **Clock skew > 60s on freshness check.** Treat as fail-closed: deny
  with `401 freshness_required`.
- **Audit write fails before decision is taken.** Fail-closed per
  ADR-0007: do not return a verdict; propagate `503 audit_unavailable`.