# HarmonyOS Phase A Local Verification

**Worktree:** `feat/harmonyos-phase-a` from `origin/main` at `ab2e71b`
**Run date:** 2026-09-01

## Passed

| Command | Result | Evidence |
|---|---|---|
| `npm run typecheck` | PASS | Vue/TypeScript check exited 0 after Phase A changes. |
| `node --test src/native/__tests__/runtime-platform.test.mjs src/native/__tests__/capabilities-harmony.test.mjs` | PASS | 5 tests: private protocol validation, fail-closed invoke, explicit capability opt-in, Harmony native capability clamp. |
| `MOBILE_FAST=1 VITE_API_BASE=https://harmony-test.example.invalid node scripts/build-harmony.mjs dev` | PASS | Vite produced `dist/`; script copied a complete bundle to ignored `harmony/entry/src/main/resources/rawfile/`. |
| `git check-ignore -v frontend/harmony/entry/src/main/resources/rawfile/index.html` | PASS | `frontend/harmony/.gitignore` excludes generated rawfile assets. |
| `env -u VITE_API_BASE npm run build:harmony` | PASS (expected failure) | Production command rejects missing API configuration instead of producing a rawfile-origin bundle. |
| `node scripts/build-mobile.mjs android dev && ./gradlew --no-daemon assembleDebug` | PASS | Existing Android Capacitor sync found 6 plugins and Gradle assembled the debug APK. |
| `node scripts/build-mobile.mjs ios dev && xcodebuild ... build` | PASS | Existing iOS Capacitor sync completed and simulator build reported `BUILD SUCCEEDED`. |

## Warnings Observed

Vite emitted existing dynamic-import/static-import chunking warnings and a chunk-size warning. The build still exited 0. No new bundle failure was introduced.

## Not Run

- `hvigorw assembleHap`: Hvigor/DevEco toolchain is not available in the local environment.
- HarmonyOS simulator/physical device: unavailable.
- HAP signing and install: no signing profile.
- HarmonyOS device verification remains unavailable; Android Gradle and iOS Xcode regressions passed after temporary ignored local SDK/API config was created and removed.

Do not interpret this record as a HarmonyOS device or application-market release verification.
