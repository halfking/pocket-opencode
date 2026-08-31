# HarmonyOS Phase A Audit

**Date:** 2026-09-01
**Scope:** ArkTS WebView shell, Vite-to-rawfile synchronization, frontend runtime detection, and Android/iOS non-regression boundaries.

## Findings And Corrections

### Corrected: Production builds require a device-reachable API

The audit found that the first implementation allowed the production command to build without `VITE_API_BASE`, which would make ArkWeb use the rawfile origin for relative API calls. `build-harmony.mjs` now requires an absolute, non-loopback HTTP(S) API base for every profile and verifies that the resolved value is embedded in the copied JavaScript assets.

### Corrected: No Capacitor platform emulation

The original research implementation sketch suggested extending Capacitor configuration and a generic Capacitor-like bridge. That would cause Android/iOS-only plugins to appear callable on HarmonyOS. Phase A instead uses `frontend/src/native/runtime-platform.ts` with a private versioned protocol and leaves `Capacitor.isNativePlatform()` unchanged.

### Corrected: HarmonyOS must not enter native SQLite/SQLCipher

`frontend/src/native/local-db.ts` treated every non-Web Capacitor runtime as encrypted native SQLite. `isWebFallbackRuntime()` now forces HarmonyOS Phase A through the existing Web SQLite/IndexedDB branch, avoiding `setEncryptionSecret` and native Capacitor SQLite calls.

### Corrected: APK delivery is Android-only

The update endpoint previously always sent `platform: "android"`, and the UI opened an APK for every platform. `runtimePlatform()` now identifies the request platform. `UpdateChecker.vue` presents the APK delivery dialog only for Android; iOS continues to use App Store distribution and HarmonyOS has no Phase A HAP distribution path.

### Corrected: Runtime marker cannot be bypassed by build metadata

The audit found that `VITE_TARGET_PLATFORM=harmony` alone could identify any Web build as Harmony if the ArkTS marker injection failed. The compile-time flag is no longer used for runtime identity; only a validated post-load private marker identifies Harmony, while missing/invalid markers stay on the Web fallback.

### Corrected: Research documentation described nonexistent paths

The research document referred to `frontend-harmony/`, `harmony-bridge.ts`, Capacitor `platforms.harmony`, and a shell copy command. It now cites the actual `frontend/harmony/`, `runtime-platform.ts`, and `build-harmony.mjs` implementation, and explicitly forbids adding HarmonyOS to Capacitor.

## Security Review

- The Harmony bridge is versioned and fail-closed. It accepts only a plain object with `version: 1`, `host: "arkts-webview"`, explicit `true` capabilities, and an invoke function.
- Missing or malformed bridges keep startup on the established Web fallback; only a validated marker identifies the post-load Harmony runtime. Throwing bridge calls return `null` rather than becoming native capability success.
- Phase A exposes no ArkTS native capability. No permission beyond `ohos.permission.INTERNET` is requested.
- `rawfile/`, DevEco caches, signing material, and HAP outputs are ignored.
- No secrets or endpoint credentials are committed; `frontend/harmony.env.example` uses an invalid example endpoint.

## Verification Status

Passed locally:

- `npm run typecheck`
- `node --test src/native/__tests__/runtime-platform.test.mjs src/native/__tests__/capabilities-harmony.test.mjs`
- `MOBILE_FAST=1 VITE_API_BASE=https://harmony-test.example.invalid node scripts/build-harmony.mjs dev`
- rawfile output existence and stale-output cleanup are verified by the build script.
- Existing Android flow: `cap sync android` completed with 6 plugins and `./gradlew --no-daemon assembleDebug` passed.
- Existing iOS flow: `cap sync ios` completed and `xcodebuild ... build` reported `BUILD SUCCEEDED`.

Not executed / blockers:

- DevEco Studio/Hvigor build: no toolchain detected.
- Signing, HAP assembly and installation: no signing profile.
- ArkWeb network, IndexedDB and safe-area behavior: no HarmonyOS device or emulator.
- Native bridges: intentionally unimplemented in Phase A.

## Android/iOS Regression Boundary

No files under `frontend/android/` or `frontend/ios/` were changed. `frontend/capacitor.config.ts` and Capacitor dependency versions were not changed. Android and iOS build commands remain the pre-existing `build-mobile.mjs` plus Gradle/Xcode flows.
