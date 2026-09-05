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
//
// API base guard: a Capacitor app has no same-origin backend — the WebView only
// serves local assets, so an empty VITE_API_BASE makes every /api call return
// index.html (200, text/html) and the UI fails with cryptic "Unexpected token"
// JSON errors (real-device incident 2026-09-05). The script therefore resolves
// the effective VITE_API_BASE (process.env > .env.<mode>.local > .env.<mode> >
// .env.local > .env, same precedence as Vite) and FAILS the build when it is
// empty. Override only in exceptional cases with MOBILE_ALLOW_EMPTY_API_BASE=1.

import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
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

// ---- API base guard (fail fast, before spending a vite build) ----
// Resolve the effective VITE_API_BASE with Vite's precedence: shell env wins,
// then .env.<mode>.local > .env.<mode> > .env.local > .env.
// Returns undefined when the file does not define the key, so the first
// DEFINITION in the precedence chain wins even if its value is empty
// (matching vite: an empty value in a higher-precedence file shadows lower ones).
function readEnvFileAPIBase(file) {
  if (!existsSync(file)) return undefined;
  const content = readFileSync(file, "utf8");
  for (const line of content.split(/\r?\n/)) {
    // 对齐 dotenv LINE 语义：支持 `export ` 前缀；未加引号的值在 # 处截断
    // （行尾注释）；带对称引号的值取引号内原文。
    // 已知限制：不做 ${VAR} 展开（vite loadEnv 会展开）。含 ${} 的值会让本
    // 守卫取到字面量，与实际注入值不一致——这种配置会被构建后的 bundle
    // 正向校验拦下（失败方向安全，不会打出错误包）。
    const m = line.match(
      /^\s*(?:export\s+)?VITE_API_BASE\s*=\s*(['"]?)([^'\r\n#]*)\1\s*(?:#.*)?$/
    );
    if (!m) continue;
    return m[2].trim();
  }
  return undefined;
}

const effectiveAPIBase =
  process.env.VITE_API_BASE ??
  readEnvFileAPIBase(path.join(frontendRoot, `.env.${mode}.local`)) ??
  readEnvFileAPIBase(envFile) ??
  readEnvFileAPIBase(path.join(frontendRoot, ".env.local")) ??
  readEnvFileAPIBase(path.join(frontendRoot, ".env")) ??
  "";

if (!effectiveAPIBase && process.env.MOBILE_ALLOW_EMPTY_API_BASE !== "1") {
  console.error(`[build-mobile] refusing to build ${platform}/${env} (mode=${mode}): VITE_API_BASE is empty`);
  console.error("[build-mobile] a Capacitor app has no same-origin backend — with an empty API base every");
  console.error("[build-mobile] /api call returns index.html and the UI fails with 'Unexpected token' JSON errors.");
  console.error("[build-mobile] fix: set VITE_API_BASE=http://<host>:<port> in the shell or .env." + mode + ",");
  console.error("[build-mobile] or export MOBILE_ALLOW_EMPTY_API_BASE=1 to override (not recommended).");
  process.exit(1);
}
if (!effectiveAPIBase) {
  console.warn("[build-mobile] WARNING: building with empty VITE_API_BASE (MOBILE_ALLOW_EMPTY_API_BASE=1) — the app will not reach any backend");
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
// a build pointing at the wrong server. Any failure here is FATAL — a
// silently-skipped check is exactly how the 2026-09-05 empty-base APK shipped.
{
  const distIndex = path.join(frontendRoot, "dist", "index.html");
  const distAssets = path.join(frontendRoot, "dist", "assets");
  if (!existsSync(distIndex) || !existsSync(distAssets)) {
    console.error("[build-mobile] sanity check failed: dist/index.html or dist/assets missing — vite build produced unexpected output");
    process.exit(1);
  }
  if (effectiveAPIBase) {
    // -F 固定字符串匹配：URL 中的 + ? | 等不再有正则转义/alternation 风险；
    // grep 退出码 1（无匹配）与 2（出错）都必须让构建失败，绝不降级跳过。
    try {
      execFileSync("grep", ["-rlF", effectiveAPIBase, distAssets], { encoding: "utf8" });
    } catch (e) {
      console.error(`[build-mobile] sanity check failed: expected API base ${effectiveAPIBase} not found in dist/assets`);
      console.error("[build-mobile] verify that VITE_API_BASE is exported into the build environment");
      process.exit(1);
    }
    console.log(`[build-mobile] sanity check passed: ${effectiveAPIBase} present in bundle`);
  }
}

console.log(`[build-mobile] OK — ${platform}/${env} (mode=${mode})`);