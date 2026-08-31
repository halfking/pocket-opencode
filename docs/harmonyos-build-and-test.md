# HarmonyOS Phase A Build And Test

## Scope

`frontend/harmony/` is an ArkTS Stage Model shell that loads the existing Vue/Vite bundle from `rawfile/`. It is independent of Capacitor: Android and iOS continue to use `npx cap sync`, while HarmonyOS never enters a Capacitor platform command.

Phase A is intentionally a Web fallback release. The ArkTS shell advertises no verified native capability. Camera, notifications, status bar, biometric authentication, encrypted local database, lifecycle, back button, and app-settings bridges remain disabled until each has device evidence.

## Build the ArkWeb assets

```bash
cd frontend
cp harmony.env.example .env.harmony-dev
# Set VITE_API_BASE to a device-reachable HTTPS/WSS or LAN endpoint.
MOBILE_FAST=1 node scripts/build-harmony.mjs dev
```

The build script removes stale hashes and copies `dist/` into `harmony/entry/src/main/resources/rawfile/`. The directory is intentionally ignored by Git. Do not manually copy files, commit `rawfile/`, or run `npx cap sync harmony`.

## DevEco Studio prerequisites

1. Install a current DevEco Studio and the HarmonyOS SDK compatible with `frontend/harmony/build-profile.json5`.
2. Open `frontend/harmony/` as a Stage Model project.
3. Configure a local signing profile. Do not commit certificates, provisioning files, keystores, `.hvigor/`, `oh_modules/`, `.hap`, `.app`, `.hsp`, or `.har` artifacts.
4. Run the DevEco/Hvigor debug build and install it on a HarmonyOS NEXT target device.

## Required device verification

- Cold start opens `rawfile/index.html`, including lazy routes, fonts, and SPA navigation.
- API HTTP, SSE, and WebSocket use the configured device-reachable `VITE_API_BASE`; no request derives a backend host from `rawfile` origin.
- IndexedDB/`jeep-sqlite` initializes, stores an offline draft, and restores it after process restart.
- Login, route navigation, and offline-to-online sync work without Capacitor "not implemented" errors.
- Camera, notifications, biometric auth, encrypted local DB, status bar, lifecycle, back button, and app settings are either absent or use a safe Web fallback. Do not claim support before a corresponding ArkTS bridge and test evidence exist.
- Verify safe-area behavior and theme colors on each target device.

## Android and iOS non-regression

This work must not modify `frontend/capacitor.config.ts`, `frontend/android/**`, `frontend/ios/**`, or Capacitor dependencies. Run the existing commands from `frontend/` after every Phase A change:

```bash
node scripts/build-mobile.mjs android dev
cd android && ./gradlew --no-daemon assembleDebug

cd ..
node scripts/build-mobile.mjs ios dev
xcodebuild -project ios/App/App.xcodeproj -scheme App \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath ios/build build
```

The current local environment has no DevEco/Hvigor command, so HAP build and HarmonyOS device verification are Phase B blockers, not passing checks. Android `assembleDebug` and iOS simulator `xcodebuild` were exercised successfully during the Phase A regression gate.
