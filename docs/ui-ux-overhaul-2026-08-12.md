# Frontend UI/UX Overhaul — 2026-08-12

## Scope

Audited and refined the Vue 3 frontend at `frontend/` using the local dev server at `http://localhost:5173` and the `squirrel` website audit workflow.

- Squirrel version: `0.0.38`
- Surface audit: `squirrel audit http://localhost:5173 -C surface -f llm`
- Audit ID: `2f4a482e`
- Crawled pages: 1 (the SPA shell; authenticated route coverage requires an authenticated browser session)
- Initial audit score: 37/F
- Initial findings: 71 passed, 27 warnings, 4 failed rules

The Squirrel score is dominated by SPA/SEO concerns that are not the primary mobile-app UX surface: missing robots/sitemap metadata, missing canonical/social metadata, and a missing static H1 in the app shell. The app itself already has a runtime `<main>` landmark and accessible navigation from the foundation pass.

## Completed Foundation

Commit `3e4a9a3` established the shared UI foundation:

- Consolidated color, typography, spacing, radius, shadow, layout, and motion tokens in `frontend/src/styles/tokens.css`.
- Added `--color-*` aliases for legacy base components.
- Added dark-mode coverage, `prefers-reduced-motion`, `focus-visible`, overscroll behavior, and font tokens.
- Added the document charset/language/title baseline in `frontend/index.html`.
- Added the skip-link and `<main>` landmark in `AppLayout`.
- Added route-aware `aria-current` and focus treatment to `BottomNav`.

## Feature Fixes

### Notes

Commit `c1155be` plus the import fix in `0b300b4`:

- Removed the stray duplicate `computed` import from `NoteListView.vue`.
- Added visible reclassification and save error states instead of console-only failures.
- Added keyboard/focus support and ARIA labels to related-note cards.
- Replaced hardcoded category tints with token-derived colors.
- Replaced ad-hoc transition timings with `--duration-fast`.
- Added recording FAB labels, `aria-pressed`, decorative `aria-hidden` markers, and an `aria-live` status region.
- Moved the recorder FAB z-index to `--z-fab`.

### Tasks

Commits `cca192f` and `e78e1a8`:

- Replaced hardcoded animation durations and modal z-indexes with design tokens.
- Added visible load, status-update, delete, and session-attach error states.
- Replaced blocking `alert()` failure paths with inline `role="alert"` messages.
- Added keyboard navigation and labels to associated session rows.
- Added modal dialog semantics, form labels, and disabled/loading feedback.
- Added a full workstream ID as a title tooltip while retaining compact display.
- Added focus and hover treatment for interactive rows.

### Meetings

Commit `3068ede`:

- Added visible load errors and retry behavior to the meeting list.
- Added keyboard navigation and ARIA labels to meeting rows.
- Replaced summary truncation with a one-line line clamp.
- Added status semantics to detail messages.
- Added recording-button labels and pressed state.
- Replaced hardcoded radii/shadows/colors with shared tokens.
- Replaced the temporary meeting HTML escaping path with the shared DOMPurify-backed markdown sanitization helper (`a76cd02`).

### Vault

Commit `3068ede` plus the current follow-up refactor:

- Replaced password-generation and form-validation alerts with inline/status feedback.
- Added labels to new-entry and edit-mode fields.
- Added `role="alert"` for initialization/form errors and `role="status"` for sync updates.
- Added an icon-enhanced empty state.
- Added copy-button labels and a semantic TOTP code label.
- Fixed the `visibilitychange` cleanup bug by using a named listener reference.
- Added the shared `Textarea` component and migrated vault password, title, username, URL, TOTP, and notes fields to shared `Input` / `Textarea` controls.
- Added semantic `--cat-card`, `--cat-note`, and `--cat-identity` aliases, then replaced remaining Vault color, font, shadow, and transition literals with existing design tokens.
- Kept the native category `select` intentionally; a reusable `Select` component remains a future form-control task.

### Settings, Contacts, Common

Commit `3d0ec7d`:

- Replaced settings update alerts with an inline live status message.
- Improved logout confirmation wording.
- Added keyboard semantics to gateway operations and contact/email timeline rows.
- Added visible load-error states and retry controls for contacts.
- Added labels and focus rings to settings and LLM Gateway form fields.
- Added `role="status"` to the Coming Soon placeholder.
- Replaced several remaining hardcoded radius, color, and transition values with tokens.

## Verification

- `npx vue-tsc --noEmit` — passed.
- `npm run build` — passed.
- Vite emitted one existing Capacitor dynamic/static import chunking warning; it does not fail the build.

## Remaining Work

1. Run Squirrel with an authenticated browser session to crawl and audit protected routes individually (email, notes, tasks, meetings, vault, gateway, settings).
2. Decide whether the SPA shell should add SEO metadata (`robots.txt`, sitemap, canonical, Open Graph) even though the product is primarily a mobile application.
3. Consider a reusable `Select` component and migrating the remaining raw controls, starting with the Notes editor and Settings LLM Gateway API-key field.
4. Extend the authenticated route audit to validate the new shared Vault controls across light and dark mode.

## Commits

- `3e4a9a3` — design tokens and accessibility baseline
- `cca192f` — TasksView animation/z-index tokens
- `0b300b4` — NoteListView import fix and Squirrel config
- `c1155be` — notes accessibility, error states, and timing tokens
- `e78e1a8` — task detail feedback and accessibility
- `3068ede` — meetings and vault accessibility/state improvements
- `3d0ec7d` — settings, contacts, and common-state polish
- `a76cd02` — meeting DOMPurify sanitization and accessibility hardening
- `bd7da2c` — task detail soft-color token migration
