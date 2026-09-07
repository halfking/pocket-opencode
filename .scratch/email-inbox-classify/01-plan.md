# Email Inbox Classify / Purge / Search Implementation Plan

> **For agentic workers:** implement task-by-task with TDD. Spec: `.scratch/email-inbox-classify/00-spec.md`.

**Goal:** Inbox navbar can AI-classify uncategorized mail, multi-select soft-delete (keep title/summary, purge body), and search within the current category.

**Architecture:** Pure helpers for category/search/select/purge/classify; local SQLCipher + PG `deleted_at`/`body_purged`; `POST /api/emails/classify` and `POST /api/emails/purge` registered before `/api/emails/`.

**Tech Stack:** Vue 3, node:test, Go + pgx, kxmemory ClassifyEmails.

## Files

- Create: `frontend/src/features/email/email-categories.ts` + test
- Create: `frontend/src/features/email/email-inbox-search.ts` + test
- Create: `frontend/src/features/email/email-inbox-select.ts` + test
- Create: `frontend/src/features/email/email-soft-delete.ts` + test
- Create: `frontend/src/features/email/email-classify-run.ts` + test
- Modify: `email-inbox-filter.ts`, `email-inbox-page.ts`, `emails-store.ts`, `EmailInboxView.vue`, `api/email.ts`, `schema.ts`, `local-db.ts`, `EmailDetailView.vue`
- Create: `backend/internal/email/classify.go` + test
- Create: `backend/internal/email/soft_delete.go` + test
- Create: `backend/internal/server/server_email_classify.go`
- Create: `backend/internal/server/server_email_purge.go`
- Modify: `store.go` migrate + list WHERE; `server.go` routes; `handleEmailBody`

## Task 1 — Category chips + filter

Chips: 全部 / 未分类 / 重要 / 工作 / 账单 / 私人 / 通知 / 广告 / 垃圾.
`__none` → `{ uncategorized: true }`. `marketing` → 广告.

## Task 2 — Current-list search

`matchInboxSearch(email, { q, from, subject, sinceMs, untilMs })` on from/subject/snippet/aiSummary.

## Task 3 — Selection + local purge patch

`toggleSelect` / `selectedIds`. `buildPurgePatch`: keep subject + aiSummary (or snippet→summary), clear snippet, set deletedAt/bodyPurged.

## Task 4 — Classify progress helper

`nextClassifyBatch(uncategorized, limit=20)`, `applyClassifyResult`, `classifyProgressLabel`.

## Task 5 — Backend normalize + purge + classify loop

Whitelist categories; ads→marketing. Purge SQL + body file delete. Sequential kxmemory, skip already categorized.

## Task 6 — Inbox UI

Header: search / label / delete / more. Chips + search chrome. Checkboxes. Progress banner. Detail: 正文已清除.
