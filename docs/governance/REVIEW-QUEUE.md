# Documentation Review Queue

> **Last updated**: 2026-08-23
> **Purpose**: Doc items where the reviewer was **not confident enough** to mark the doc `superseded` or to leave it as-is. These are the next things a doc-governance reviewer must look at.
>
> **Rule**: A doc lands here only when the doc-governance reviewer cannot decide. Do **not** dump "all legacy docs" here — that is what `SUPERSEDED.md` is for. This file is for **uncertainty**, not for bulk work.

## How an item is added

A reviewer adds an item when they answer "I'm not sure" to any of these:

1. Is the doc actually outdated, or does it capture a niche contract that the new docs don't cover?
2. Does the inline claim of "完成 / implemented" still hold in code?
3. Is the replacement doc really equivalent, or does it drop a use-case the original covered?
4. Does the doc's evidence level belong in `EVIDENCE-LEDGER.md` but is missing a pin?

## How an item is removed

An item is removed only when one of:
1. The reviewer resolves the uncertainty and the doc is moved to `SUPERSEDED.md` (banner added) or promoted in the matrix.
2. The owner confirms the doc is still accurate and the matrix row is updated to reflect that.
3. The doc is deleted by ADR with a tombstone in `SUPERSEDED.md`.

## Items

### Q-001 — `docs/优化v4/11-并行执行提示词.md`

- **What it is**: a set of parallel-execution prompts written for an earlier sprint. The v3 audit does not re-issue them; the new `docs/新架构v1/03-roadmap/里程碑.md` is narrower in scope.
- **Uncertainty**: are any of the parallel-execution prompts in `优化v4/11` still useful as **inputs** to v3 sub-agents, even though v3 doesn't ship them as-is?
- **Reviewer action needed**: spot-check the section list against the v3 milestone table; either keep `优化v4/11` as a referenced input (banner: "used as input, not as-is") or move it fully to `SUPERSEDED.md`.
- **Owner**: doc-governance reviewer for the next sprint.

### Q-002 — `docs/superpowers/specs/2026-07-27-pocket-foldable-redclaw-integration-design.md`

- **What it is**: a design spec for the foldable + RedClaw integration. Replaced in spirit by `docs/新架构v1/02-modules/mobile-shell.md`.
- **Uncertainty**: does the foldable-specific UX guidance (layout, breakpoint, gesture) still need to live somewhere after the v3 supersede? Or is it fully covered by `mobile-shell.md`?
- **Reviewer action needed**: confirm whether foldable-specific guidance needs a dedicated section in `新架构v1/02-modules/mobile-shell.md`. If yes, fold the content in and mark this spec as `superseded`. If no, just mark `superseded`.
- **Owner**: mobile-UX reviewer for the next sprint.

### Q-003 — `docs/superpowers/specs/2026-07-24-opencode-supreme-programmer-mobile-platform-design.md`

- **What it is**: the original "supreme programmer" mobile platform design doc. Replaced by `docs/新架构v1/README.md`.
- **Uncertainty**: the doc claims several product-level wins ("全栈接入 / 全链路能力"). Some of those product claims (not the architecture) might still be referenced by marketing or planning. Should the product copy be preserved as a "what we wanted" doc, or fully retired?
- **Reviewer action needed**: product owner decision; if retired, move to `SUPERSEDED.md`.
- **Owner**: product owner.

### Q-004 — `OPENCODE_ADAPTER_FIXES.md`, `OPENCODE_API_ANALYSIS.md`, `OPENCODE_API_SETUP.md` (project root)

- **What they are**: three root-level `OPENCODE_*` files that don't end in `FINAL / COMPLETE / VERIFICATION`. The task scope mentioned only the FINAL/COMPLETE/VERIFICATION ones, but these three sit next to them and read like the same series.
- **Uncertainty**: are they part of the same legacy cluster and should also be `superseded`, or are some of them genuinely current (e.g. `OPENCODE_API_SETUP.md` could be a current setup guide that wasn't updated)?
- **Reviewer action needed**: open each, check date / last-touched-by, check if it references code paths that still exist. If clearly stale, move to `SUPERSEDED.md`. If ambiguous, leave and ask the code owner.
- **Owner**: OpenCode adapter code owner.

### Q-005 — `OPENCODE_MOBILE_MANAGEMENT_PLAN.md`, `OPENCODE_PLUGIN_ARCHITECTURE.md`, `OPENCODE_SESSION_OPTIMIZATION.md` (project root)

- **What they are**: three more root-level `OPENCODE_*` files. Same uncertainty as Q-004.
- **Reviewer action needed**: same as Q-004.
- **Owner**: OpenCode adapter code owner.

### Q-006 — `docs/opencode-task-management-architecture.md`

- **What it is**: the most detailed root-of-`docs/` OpenCode architecture doc. Likely `superseded` by `docs/新架构v1/02-modules/zagent-gateway.md`.
- **Uncertainty**: it has very specific schema diagrams and routing tables. Some of those might still be useful to a future contractor doing a deep dive, even if the architecture direction has changed. Confirm whether to keep as a `historical reference` with a banner, or fully retire.
- **Reviewer action needed**: skim the routing tables; if they match the new contracts in `docs/新架构v1/04-contracts/`, retire with `superseded` banner. If they document a unique failure mode the new docs don't cover, link from `新架构v1/` and keep.
- **Owner**: ZAG contract owner.

### Q-007 — `docs/superpowers/plans/2026-07-25-phase2-core-features.md` and other non-redclaw plans

- **What they are**: phase 2/3/4/a–g sprint plans. The task scope only flagged redclaw-related plans for `superseded`. But these plans pre-date the v3 audit and contain "完成 / implemented" claims.
- **Uncertainty**: should they be in `SUPERSEDED.md` too, or are they still useful as historical sprint records?
- **Reviewer action needed**: pick one of:
  - Bulk-retire all pre-audit phase plans with a `superseded` banner pointing to `docs/新架构v1/03-roadmap/里程碑.md`.
  - Keep them as sprint history with a banner pointing to `新架构v1/` and the audit (`docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md`).
- **Owner**: doc-governance reviewer for the next sprint. This is the highest-priority item on this list because it determines whether `REVIEW-QUEUE.md` doubles in size next round.

## Items already resolved this sprint

(none yet — this is the first round)
