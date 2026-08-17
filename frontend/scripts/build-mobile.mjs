#!/usr/bin/env node
// scripts/build-mobile.mjs — per-platform, per-environment Vite + Capacitor build.
//
// Usage:
//   node scripts/build-mobile.mjs ios     dev       # iOS dev (LAN IP default)
//   node scripts/build-mobile.mjs ios     staging   # iOS staging
//   node scripts/build-mobile.mjs android dev       # Android emulator
//   node scripts/build-mobile.mjs android prod      # Android prod
//
// Override the API base URL:
//   VITE_API_BASE=http://192.168.1.42:8088 \
//     node scripts/build-mobile.mjs ios dev
//
// Skip vite typecheck (fast path):
//   MOBILE_FAST=1 node scripts/build-mobile.mjs ios dev
//
// Behaviour:
//   - Validates args (platform ∈ {ios, android}; env ∈ {dev, staging, prod}).
//   - Picks a vite --mode profile: ios-dev | android-dev | staging | production.
//   - Vite loads .env.<mode> automatically when --mode is set. A mode of
//     "production" loads .env.production; we use the literal mode name
//     "production" so that file applies.
//   - Runs `vite build`, then `npx cap sync <platform>` so the bundle lands
//     in the native project's webDir (dist).
//
// Why this exists: the legacy build only had `.env.development` (Android emulator)
// and `.env` (empty). iOS real-device users had to override VITE_API_BASE by hand
// every time, and there was no staging/prod profile at all. This script makes the
// per-platform / per-env matrix reproducible.

import { execSync, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(__dirname, "..");

const PLATFORMS = new Set(["ios", "android"]);
const ENVS = new Set(["dev", "staging", "prod"]);

function usage(exitCode = 1) {
  console.error("Usage: node scripts/build-mobile.mjs <ios|android> <dev|staging|prod>");
  console.error("Override API base: VITE_API_BASE=http://host:port node scripts/build-mobile.mjs ...");
  process.exit(exitCode);
}

const [, , platform, env] = process.argv;
if (!platform || !PLATFORMS.has(platform)) usage();
if (!env || !ENVS.has(env)) usage();

function modeFor(platform, env) {
  if (env === "dev") return `${platform}-dev`;
  if (env === "staging") return "staging";
  // env === 'prod'
  return "production";
}

const mode = modeFor(platform, env);
const envFile = path.join(frontendRoot, `.env.${mode}`);
if (!existsSync(envFile) && mode !== "production") {
  console.error(`[build-mobile] missing env file: ${envFile}`);
  console.error(`[build-mobile] expected ${mode} profile for ${platform}/${env}`);
  process.exit(1);
}

const fast = process.env.MOBILE_FAST === "1";
const envVars = { ...process.env, FORCE_COLOR: "1" };

const buildCmd = fast ? "npm:build:fast" : "npm:build";
console.log(`[build-mobile] vite build --mode ${mode} (${fast ? "fast, no typecheck" : "with typecheck"})`);
const build = spawnSync("npm", ["run", fast ? "build:fast" : "build", "--", "--mode", mode], {
  cwd: frontendRoot,
  env: envVars,
  stdio: "inherit",
  shell: true,
});
if (build.status !== 0) {
  console.error(`[build-mobile] vite build failed (exit=${build.status})`);
  process.exit(build.status ?? 1);
}

console.log(`[build-mobile] cap sync ${platform}`);
const sync = spawnSync("npx", ["cap", "sync", platform], {
  cwd: frontendRoot,
  env: envVars,
  stdio: "inherit",
});
if (sync.status !== 0) {
  console.error(`[build-mobile] cap sync failed (exit=${sync.status})`);
  process.exit(sync.status ?? 1);
}

// Sanity check: assert that the bundled JS contains the API base we expect,
// so a wrong VITE_API_BASE override fails loudly instead of silently shipping
// a build pointing at the wrong server.
try {
  const distIndex = path.join(frontendRoot, "dist", "index.html");
  const distAssets = path.join(frontendRoot, "dist", "assets");
  if (!existsSync(distIndex)) {
    console.warn("[build-mobile] dist/index.html missing — skipping asset sanity check");
  } else {
    const expectedBase = process.env.VITE_API_BASE;
    if (expectedBase) {
      const grep = execSync(
        `grep -rl --include='*.js' '${expectedBase.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}' ${distAssets} || true`,
        { cwd: frontendRoot, encoding: "utf8" }
      ).trim();
      if (!grep) {
        console.error(`[build-mobile] sanity check failed: expected API base ${expectedBase} not found in dist/assets/*.js`);
        console.error("[build-mobile] verify that VITE_API_BASE is exported into the build environment");
        process.exit(1);
      }
      console.log(`[build-mobile] sanity check passed: ${expectedBase} present in bundle`);
    }
  }
} catch (e) {
  console.warn(`[build-mobile] sanity check skipped: ${e.message}`);
}

console.log(`[build-mobile] OK — ${platform}/${env} (mode=${mode})`);