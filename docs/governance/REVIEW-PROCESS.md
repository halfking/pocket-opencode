# Documentation Review Process

> **Last updated**: 2026-08-23
> **Purpose**: The review gate that any doc claiming a capability is `implemented` or `production-verified` MUST pass before merge. This file is the contract between doc author and reviewer.
>
> **Scope**: every Markdown doc under `docs/` that claims a capability status — implementation, verification, completeness, or production-readiness. This file does **not** govern code review (separate SKILL).

## When this gate applies

A doc triggers this gate when any of the following strings appear in its body, in any language:

- `implemented`, `完成`, `已完成`, `implemented and verified`
- `production-ready`, `production-verified`, `上线`, `生产就绪`
- `complete`, `completed`, `complete and verified`, `全链路完成`
- `final`, `最终`, `FINAL`
- `integration-tested`, `integration verified`, `集成测试通过`

If a doc claims one of those statuses, the doc MUST also carry:

1. A `## Evidence` (or `## 证据`) section, OR a row in `docs/governance/EVIDENCE-LEDGER.md` linked from the doc.
2. An explicit evidence level from the matrix in `STATUS-MATRIX.md`.
3. A pointer to the replacement doc if the doc is being superseded.

If any of those are missing, the reviewer **downgrades** the doc to `claimed (unverified)` and adds an inline banner; they do not block the PR.

## Required artifacts for the gate

A capability claim is only valid if it carries, in the doc body or in `EVIDENCE-LEDGER.md`:

| Claim | Required artifact |
|---|---|
| `implemented (unverified)` | Path:line of the source file that implements it. |
| `contract-tested` | Test path (file:line) + last green run log path. |
| `integration-tested` | Run log path that shows the call against a real upstream/downstream (not a mock). |
| `production-verified` | Production telemetry query path + an on-call owner + a runbook link. |
| `superseded` | Replacement doc link in `SUPERSEDED.md`. |

A doc that quotes a percentage (e.g. "完成度 80%") without an artifact is auto-downgraded to `claimed (unverified)`. Reviewer MUST add the banner in the same commit; the doc author MAY supply the artifact as a follow-up.

## Reviewer rules

1. **At least one reviewer** who did **not** author the doc and is not the code author of the referenced code.
2. The reviewer MUST confirm:
   - Every code reference (`file:line`) opens to a current commit on the branch under review.
   - Every test reference is green at the SHA listed in the Evidence Ledger row.
   - The status word in the doc body matches the evidence level assigned in the matrix.
3. The reviewer MUST either:
   - **Approve**: the doc passes the gate, the matrix row is updated, the ledger row is updated.
   - **Request changes**: the doc is downgraded to `claimed (unverified)` with an inline banner, the matrix row is updated, the ledger row is unchanged.
   - **Reject**: the doc is moved to `REVIEW-QUEUE.md` with the reviewer's uncertainty note and the author is asked to either substantiate or remove the claim.

## Failure modes and what happens next

| Failure | Action |
|---|---|
| Doc claims "完成 / implemented" but the code does not exist. | Downgrade to `claimed (unverified)`, add banner, update matrix. |
| Doc claims "已完成" but the code exists but is not on a tracked path. | Move doc to `REVIEW-QUEUE.md`; do not change status until path is found. |
| Doc references an external commit / endpoint that is no longer valid. | Replace with the current SHA, log the change in `EVIDENCE-LEDGER.md`, update matrix. |
| Doc has no `## Evidence` section AND is not in `SUPERSEDED.md`. | Reviewer MUST add the banner and downgrade in the same PR; the doc author cannot waive this. |
| Two docs disagree on status. | The lower-evidence row wins until the disagreement is resolved; both docs are flagged in `REVIEW-QUEUE.md`. |

## Commit convention

One doc = one commit. The commit message MUST include:
- `docs(<area>): <change>` prefix.
- For supersede: `docs(<area>): supersede — <reason>`. The body MUST link the new doc.
- For evidence upgrades: `docs(<area>): promote <component> to <status> — <artifact>`. The body MUST cite the test log.

A commit that bundles > 1 doc change MUST be split by the reviewer; the reviewer comments `Please split: one doc per commit.` and blocks until the author rebases.

## What this gate does NOT do

- It does not verify code correctness (code review / security review handles that).
- It does not enforce doc formatting / linting.
- It does not prevent doc deletion (a separate ADR is required to delete a doc that is referenced from anywhere else).
- It does not retroactively downgrade a doc that was merged before this gate existed; it only governs new and amended claims.

## When this gate is overridden

The only valid override is a security incident: if a doc leaks a credential, a path, or a PII sample, it is removed and a tombstone banner added. The override is recorded in `REVIEW-QUEUE.md` with a one-line rationale.
