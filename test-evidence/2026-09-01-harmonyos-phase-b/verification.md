# HarmonyOS Phase B Verification

**Worktree:** `feat/harmonyos-phase-b` from `origin/main` at `50f172b`
**Commits:** `b0a60ce`, `86db98e`
**Run date:** 2026-09-01
**Scope:** HarmonyOS rawfile WebSocket endpoint correction, frontend regression, Android/iOS non-regression, and local toolchain readiness.

## Passed

| Command / check | Result | Evidence |
|---|---|---|
| `npm ci` in `frontend/` | PASS | Installed the lockfile dependencies successfully (227 packages); ignored `node_modules/` only. |
| `npm run typecheck` | PASS | `vue-tsc --noEmit` exited 0 after the WebSocket URL changes. |
| `node --test <all frontend/src/**/__tests__/*.test.mjs>` | PASS | 238 tests passed, 0 failed. Includes six URL-helper tests and three OpenCode network/source-contract tests. |
| `node --test src/api/__tests__/websocket-url.test.mjs src/native/__tests__/runtime-platform.test.mjs src/native/__tests__/capabilities-harmony.test.mjs` | PASS | 13 tests passed, 0 failed. URL conversion, token encoding, rawfile-origin separation, OpenCode API-client routing, and Harmony fail-closed guards passed. |
| `MOBILE_FAST=1 VITE_API_BASE=https://harmony-test.example.invalid node scripts/build-harmony.mjs dev` | PASS (asset-only) | Vite generated `dist/`; the script removed and copied `dist/` to ignored `harmony/entry/src/main/resources/rawfile/` and verified the API base was embedded in JavaScript. The `.invalid` endpoint is deliberately non-routable and is not network evidence. |
| `MOBILE_FAST=1 node scripts/build-mobile.mjs android dev` | PASS | Capacitor Android sync completed and found the existing six Android plugins. A temporary ignored `.env.android-dev` used only the non-secret placeholder `https://pocket.example.invalid`. |
| `ANDROID_HOME=/Users/xutaohuang/Library/Android/sdk ANDROID_SDK_ROOT=/Users/xutaohuang/Library/Android/sdk ./gradlew --no-daemon assembleDebug` | PASS | Android Gradle reported `BUILD SUCCESSFUL` (279 actionable tasks). The SDK path was supplied in the process environment; no local path file was added. |
| `MOBILE_FAST=1 node scripts/build-mobile.mjs ios dev` | PASS | Capacitor iOS sync completed and found the existing six iOS plugins. A temporary ignored `.env.ios-dev` used only the non-secret placeholder `https://pocket.example.invalid`. |
| `xcodebuild ... -disableAutomaticPackageResolution build` | PASS | iOS Simulator build reported `** BUILD SUCCEEDED **` after using the cached/locked SwiftPM dependencies. |
| Android/iOS boundary check | PASS | No tracked changes remain under `frontend/android/**`, `frontend/ios/**`, `frontend/capacitor.config.ts`, or Capacitor dependency manifests. |

## Code change under test

`frontend/src/api/websocket-url.ts` now validates an absolute HTTP(S) API base, converts it to WS/WSS, normalizes the `/ws` endpoint, and encodes an optional token. The shared WebSocket client and `frontend/src/stores/opencode.ts` use this helper. OpenCode realtime updates no longer derive their endpoint from `window.location.host`, which could resolve to the ArkWeb rawfile origin. OpenCode history and summary requests now use the shared HTTP client, including the configured API base, Bearer authentication, and encoded session IDs.

When the API base or authentication token is unavailable, the OpenCode subscription and shared client fail closed without opening an empty or unauthenticated WebSocket connection, including during reconnect. HarmonyOS marker capabilities remain disabled; no ArkTS bridge or Capacitor platform behavior was changed.

## Toolchain readiness

| Item | Result | Notes |
|---|---|---|
| `hvigorw` / `hvigor` | BLOCKED | Not found on `PATH`. |
| DevEco Studio | BLOCKED | No `devstudio`/`devecostudio` command or known local installation was found. |
| HarmonyOS SDK | BLOCKED | No HarmonyOS SDK path was detected in the local environment. |
| `hdc` | BLOCKED | Not found on `PATH`; no HarmonyOS target was available. |
| Signing profile / certificate / keystore | BLOCKED | No local signing profile was available; no signing material was inspected or added. |
| HarmonyOS NEXT simulator or physical device | BLOCKED | No target was available for installation or runtime verification. |

## Not run / must remain unverified

- DevEco/Hvigor HAP assembly, signing, installation, and launch.
- ArkWeb cold start, SPA/lazy-route/font loading, safe-area, theme, and tablet layout checks.
- Real HTTPS API, SSE, WSS connectivity, login/JWT, and reconnect behavior on HarmonyOS.
- IndexedDB/`jeep-sqlite` initialization, offline draft persistence, process-restart recovery, outbox drain, and offline-to-online synchronization on HarmonyOS.
- Camera, notification, status bar, biometric, encrypted local DB, lifecycle, back-button, and app-settings native bridges. All remain disabled or on the existing Web fallback until each has a bridge and device evidence.
- HAP distribution or application-market release verification.

The invalid example endpoints used by local bundle checks prove only build-time embedding and validation. They do not prove DNS, TLS, HTTP, SSE, WebSocket, device, HAP, signing, or production readiness. The HarmonyOS status must therefore remain `implemented (unverified)` in `docs/governance/STATUS-MATRIX.md` and the corresponding evidence level in `docs/governance/EVIDENCE-LEDGER.md` must not be upgraded.
