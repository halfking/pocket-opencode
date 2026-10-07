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
// Build the coexisting STT debug package (android only):
//   node scripts/build-mobile.mjs android dev --sttdev
//   -> vite build + cap sync + `gradlew assembleDebug -PsttDevApp`
//
//   Why this exists: `-PsttDevApp` flips applicationIdSuffix to ".sttdev", giving a
//   package that can sit on the phone next to the real one with separate data
//   (app/build.gradle). That is how STT getsmessed with on-device recording without
//   touching the user's installed app. But the flag lived only in gradle, so the
//   sanctioned build path stopped at `cap sync` and the gradle step was a
//   hand-typed incantation that nothing verified — a mistyped property silently
//   yields a **main-package** APK that would overwrite the real app on install.
//
//   So this flag does not just pass the property; it asserts the produced
//   artifact really is the coexisting one (see verifySttdevArtifact).
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
import { existsSync, readFileSync, readdirSync } from "node:fs";
import net from "node:net";
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

function tcpReachable(host, port, timeoutMs = 1500) {
  return new Promise((resolve) => {
    const socket = new net.Socket();
    let settled = false;
    const done = (ok, why) => {
      if (settled) return;
      settled = true;
      try { socket.destroy(); } catch { /* 已关闭 */ }
      resolve({ ok, why });
    };
    socket.setTimeout(timeoutMs);
    socket.once("connect", () => done(true, "connected"));
    socket.once("timeout", () => done(false, `connect timed out after ${timeoutMs}ms`));
    // ECONNREFUSED / EHOSTUNREACH / ENOTFOUND 都落到这里
    socket.once("error", (e) => done(false, `${e.code || e.message}`));
    try { socket.connect(port, host); } catch (e) { done(false, e.code || e.message); }
  });
}

// ---- 自检（不构建任何东西） --------------------------------------------
// 守卫自己静默失效就是负债：可达性探针一旦被改坏，它会安静地放行所有死地址，
// 而症状要等到真机上「连不上后端」才暴露。`--selftest` 在本地造出**结论相反**
// 的两种输入（自己开的活端口 / 确定没人听的死端口），不碰 vite、不碰 gradle、
// 不需要设备。
if (process.argv.includes('--selftest')) {
  const { spawnSync } = await import('node:child_process')
  const cases = []
  // 1) 活端口：自己 listen 一个，抓到端口号再探 —— 必须通
  const srv = net.createServer((s) => s.end())
  await new Promise((res) => srv.listen(0, '127.0.0.1', res))
  const livePort = srv.address().port
  // 2) 死端口：先 listen 拿到端口号，随即 close —— 此刻确定没人听
  const dead = net.createServer((s) => s.end())
  await new Promise((res) => dead.listen(0, '127.0.0.1', res))
  const deadPort = dead.address().port
  await new Promise((res) => dead.close(res))

  const r1 = await tcpReachable('127.0.0.1', livePort, 2000)
  cases.push({ name: '活端口必须可达', got: r1.ok, want: true, why: r1.why })
  const r2 = await tcpReachable('127.0.0.1', deadPort, 2000)
  cases.push({ name: '死端口必须不可达', got: r2.ok, want: false, why: r2.why })
  // ⚠️ 第 3 条**刻意不写成断言**。2026-10-04 在本机实测：
  //   192.0.2.1:9（RFC 5737 黑洞地址）      → connected
  //   no-such-host.invalid:80（永不可解析） → connected
  //   192.168.31.37:8090（LAN 上没人听）    → ECONNREFUSED ✅
  // ⇒ 本机存在拦截**非 LAN** 出站 TCP 的透明代理/端口转发。
  // 也就是说：探针对它设计针对的场景（LAN 机器下线）有效，但在有代理的环境里
  // **负向用例根本证不出来**。把它写成断言会得到一条「永远红」的用例，
  // 那不是判据，是噪音。所以这里只**如实报告**，不断言。
  const r3 = await tcpReachable('no-such-host.invalid', 80, 2000)
  const intercepts = r3.ok === true
  console.log(`  ${intercepts ? '⚠️' : '  '} 观测（不断言）：不可解析主机名 → ${r3.ok ? 'connected' : r3.why}` +
    (intercepts
      ? '　⇒ 本环境有透明代理/端口转发，**负向用例在此环境不可证**；守卫只对 LAN 侧有效'
      : '　⇒ 本环境无拦截，负向用例可证'))

  srv.close()
  let bad = 0
  for (const c of cases) {
    const ok = c.got === c.want
    if (!ok) bad++
    console.log(`  ${ok ? '🟢' : '🔴'} ${c.name}：got=${c.got} want=${c.want}${c.why ? '（' + c.why + '）' : ''}`)
  }
  console.log(`\n[build-mobile] 自检 ${cases.length - bad}/${cases.length} 通过`)
  process.exit(bad ? 1 : 0)
}

const [, , platform, env, ...rest] = process.argv;
if (!platform || !PLATFORMS.has(platform)) usage();
if (!env || !ENVS.has(env)) usage();

// --sttdev is a build VARIANT, orthogonal to platform/env. Reject it on iOS
// instead of ignoring it: silently dropping the flag would hand back a
// main-package build while the caller believes they got the coexisting one.
const sttdev = rest.includes("--sttdev");
const unknownFlags = rest.filter((a) => a !== "--sttdev");
if (unknownFlags.length) {
  console.error(`[build-mobile] unknown argument(s): ${unknownFlags.join(" ")}`);
  usage();
}
if (sttdev && platform !== "android") {
  console.error("[build-mobile] --sttdev is android-only (it drives app/build.gradle)");
  process.exit(1);
}

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

// ---- production host guard ----
// 生产包只允许公网入口（pocket.itestu.cn 等）。占位符（pocket.example.com）与
// 环回/LAN 网段（127.0.0.1 / localhost / ::1 / 10.0.2.2 / 192.168.* / 172.16-31.*）
// 一律拒绝——真机上这些地址要么打到 WebView 自身、要么打到别人的局域网。
// 与 build-harmony.mjs 的 loopback 拒绝同向，比它更严（含占位符与 LAN 段）。
if (mode === "production" && effectiveAPIBase) {
  let host = "";
  try {
    host = new URL(effectiveAPIBase).hostname;
  } catch {
    console.error(`[build-mobile] refusing production build: VITE_API_BASE is not an absolute URL: ${effectiveAPIBase}`);
    process.exit(1);
  }
  const isLoopbackOrLAN =
    ["localhost", "127.0.0.1", "::1", "10.0.2.2"].includes(host) ||
    /^192\.168\.\d+\.\d+$/.test(host) ||
    /^172\.(1[6-9]|2\d|3[01])\.\d+\.\d+$/.test(host);
  const isPlaceholder = /^(pocket|staging-pocket)\.example\.com$/.test(host);
  if (isLoopbackOrLAN || isPlaceholder) {
    console.error(`[build-mobile] refusing production build: VITE_API_BASE host '${host}' is loopback/LAN/placeholder`);
    console.error("[build-mobile] production apps must reach the public ingress (https://pocket.itestu.cn);");
    console.error("[build-mobile] dev/emulator targets belong to 'dev' builds (.env.android-dev), not 'prod'.");
    process.exit(1);
  }
}

// ---- reachability guard（字符串对了 ≠ 那台机器还在） ----
// 2026-10-04 实吃：`.env.android-dev` 写死 `VITE_API_BASE=http://192.168.31.37:8090`，
// 而本机 LAN IP 早已变成 .34，.37 那台也不再监听（curl 全 000）。但下面那段
// 「sanity check」只验**字符串在 bundle 里**，于是这个死地址一路绿灯打进 APK——
// 它自己的注释写着「fail loudly instead of silently shipping a build pointing at
// the wrong server」，而它能防的只有「值写错」，防不住「值没变、机器没了」。
// 真机上这类包的表现是 UI 报 JSON 解析错，追起来要多绕三个仓。
//
// 放在 vite build **之前**：死地址不该先花掉一次完整构建再报。
// 默认对 dev 与 production 都判红（两者的后果都是「打出去的包连不上任何后端」），
// 确需离线出包时用 MOBILE_SKIP_REACHABILITY=1 显式放行——与既有的
// MOBILE_ALLOW_EMPTY_API_BASE 同一套「逃生舱要显式、要留痕」的约定。

if (effectiveAPIBase && process.env.MOBILE_SKIP_REACHABILITY !== "1") {
  let target;
  try {
    const u = new URL(effectiveAPIBase);
    const scheme = u.protocol.replace(":", "");
    if (!["http", "https", "ws", "wss"].includes(scheme)) {
      target = { unsupportedScheme: scheme };
    } else {
      target = { host: u.hostname, port: Number(u.port) || (scheme === "https" || scheme === "wss" ? 443 : 80) };
    }
  } catch {
    target = { parseError: true };
  }

  if (target.parseError) {
    console.error(`[build-mobile] refusing to build ${platform}/${env}: VITE_API_BASE is not an absolute URL: ${effectiveAPIBase}`);
    process.exit(1);
  }
  if (target.unsupportedScheme) {
    console.warn(`[build-mobile] WARNING: VITE_API_BASE scheme '${target.unsupportedScheme}://' is not tcp-probeable — skipping reachability check`);
  } else {
    const probe = await tcpReachable(target.host, target.port);
    if (!probe.ok) {
      console.error(`[build-mobile] refusing to build ${platform}/${env}: API base is UNREACHABLE`);
      console.error(`[build-mobile]   VITE_API_BASE = ${effectiveAPIBase}`);
      console.error(`[build-mobile]   tcp ${target.host}:${target.port} → ${probe.why}`);
      console.error("[build-mobile] 字符串正确不等于那台机器还在——.env.<mode> 里写死的 LAN 地址会随网络变化而失效。");
      console.error("[build-mobile] 修法：先起后端，或用 VITE_API_BASE=http://<当前可达的 host:port> 覆盖；");
      console.error("[build-mobile] 确需离线出包：MOBILE_SKIP_REACHABILITY=1（会在构建日志留痕）。");
      process.exit(1);
    }
    console.log(`[build-mobile] reachability OK: ${target.host}:${target.port} 可连接（${effectiveAPIBase}）`);
    // ⚠️ 诚实标注这条守卫**防得住什么、防不住什么**：
    //   防得住 —— 最常见的一类：LAN IP 变了、写死的机器下线了、端口没人听。
    //   防不住 —— 「连得上但那不是我们的服务」。TCP 连通 ≠ 服务可用：本机实测
    //   192.0.2.1:9（RFC 5737 黑洞地址）都返回 connected，说明存在透明代理或
    //   端口转发在应答。所以它**不是**健康检查，只是「这台机器上这个口有没有人听」。
    console.log("[build-mobile] 注意：这只证明「有人监听该端口」，不证明「那是 pocketd」。");  
    console.log("[build-mobile] 透明代理/端口转发会造成假绿；真机联调前请另跑 curl -sS " + effectiveAPIBase + "/healthz。");
  }
} else if (effectiveAPIBase) {
  console.warn("[build-mobile] WARNING: MOBILE_SKIP_REACHABILITY=1 —— 跳过后端可达性检查，产物可能指向一台不存在的机器");
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

// 产物级门禁：确认「读原生构建身份」的修复**真的进了 dist/**。
//
// 为什么要这一层：判据与 vue-tsc 都跑在**源码**上，两者都绿并不代表修复进了
// APK —— vite 会 tree-shake，改名/拆包也能让代码「还在仓库里、但不在产物里」。
// 这正是 §4.74.2 的形态：仓库里是对的、设备上跑的不是，而没有任何一步会红。
//
// 放在 cap sync **之前**：门禁失败时 dist 还没被拷进原生工程，不会留下一个
// 「已经 cap sync 过、但 bundle 是旧的」的平台目录。
console.log(`[build-mobile] verify build identity in artifact`);
const identity = spawnSync("node", ["scripts/verify-build-identity.mjs"], {
  cwd: frontendRoot,
  env: envVars,
  stdio: "inherit",
  shell: true,
});
if (identity.status !== 0) {
  console.error(
    `[build-mobile] build-identity gate failed (exit=${identity.status}) — ` +
      `产物里缺少「读原生版本」相关内容，APK 会继续显示硬编码常量（§4.74.2）。` +
      `已停止，未执行 cap sync。`
  );
  process.exit(identity.status ?? 1);
}

console.log(`[build-mobile] cap sync ${platform}`);
// BUG-V3 (2026-10-01): this spawnSync must pass shell:true on Windows.
// `npx` ships as npx.cmd, and spawnSync without a shell cannot execute a
// .cmd — it returns status:null with error ENOENT, which the check below
// then reports as "cap sync failed (exit=null)". Measured on this machine:
//
//   spawnSync("npx", ["--version"])              -> status=null  error=ENOENT
//   spawnSync("npx", ["--version"], {shell:true}) -> status=0     "10.9.8"
//
// Effect before the fix: the sanctioned Android build path could never finish
// on Windows even though `vite build` had already succeeded — the artifact on
// disk was left half-updated and the script exited nonzero. Matches the above
// vite-build call, which already passes shell:true.
const sync = spawnSync("npx", ["cap", "sync", platform], {
  cwd: frontendRoot,
  env: envVars,
  stdio: "inherit",
  shell: true,
});
if (sync.status !== 0) {
  if (sync.error) console.error(`[build-mobile] cap sync could not start: ${sync.error.code || sync.error.message}`);
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
    // BUG-V4 (2026-10-01): this used to shell out to `grep -rlF`. grep does not
    // exist on Windows, so execFileSync threw ENOENT and the catch below
    // reported it as "expected API base ... not found in dist/assets" — a
    // message that sends you hunting for a VITE_API_BASE problem that does not
    // exist. Verified locally: the base was in dist/assets the whole time.
    // Reading the bundle from Node removes the platform dependency, and lets a
    // genuine read error be reported as a read error instead of a false miss.
    // (The original intent is preserved: a missing base must still be FATAL —
    // a silently-skipped check is exactly how the 2026-09-05 empty-base APK shipped.)
    let hit = null;
    let readError = null;
    try {
      const walk = (dir) => {
        for (const entry of readdirSync(dir, { withFileTypes: true })) {
          const p = path.join(dir, entry.name);
          if (entry.isDirectory()) walk(p);
          else if (/\.(js|mjs|css|html|json)$/.test(entry.name) && readFileSync(p, "utf8").includes(effectiveAPIBase)) {
            hit = p;
            return;
          }
        }
      };
      walk(distAssets);
      if (!hit && readFileSync(distIndex, "utf8").includes(effectiveAPIBase)) hit = distIndex;
    } catch (e) {
      readError = e;
    }
    if (readError) {
      console.error(`[build-mobile] sanity check could not read the bundle: ${readError.code || readError.message}`);
      console.error("[build-mobile] this is a READ failure, not a missing API base");
      process.exit(1);
    }
    if (!hit) {
      console.error(`[build-mobile] sanity check failed: expected API base ${effectiveAPIBase} not found in dist/assets`);
      console.error("[build-mobile] verify that VITE_API_BASE is exported into the build environment");
      process.exit(1);
    }
    console.log(`[build-mobile] sanity check passed: ${effectiveAPIBase} present in ${path.relative(frontendRoot, hit)}`);
  }
}

// ---- sttdev variant: gradle assembleDebug + assert the artifact is the
// coexisting package, not the main one -------------------------------------
//
// The assertion is the point. `assembleDebug -PsttDevApp` is only a *request*:
// if app/build.gradle ever drops the `if (project.hasProperty('sttDevApp'))`
// branch, gradle still exits 0 and emits an APK — a **main-package** APK that
// `adb install -r` would happily push over the user's real app. Nothing in the
// gradle output distinguishes the two, and the damage only shows up later, on
// the user's data. So we read what was actually produced.
// 期望的 applicationId 后缀。默认 `.sttdev`（行为与之前完全一致）；
// 设 MOBILE_APP_ID_SUFFIX=.matrix 可出**第三个**并存包（设备上已有的 .sttdev
// 由别的机器签名时，install -r 会 INSTALL_FAILED_UPDATE_INCOMPATIBLE，
// 换 applicationId 就不必卸载任何东西）。见 04 §4.1d。
const APP_ID_SUFFIX = process.env.MOBILE_APP_ID_SUFFIX || ".sttdev";

function verifySttdevArtifact() {
  const meta = path.join(
    frontendRoot, "android", "app", "build", "outputs", "apk", "debug", "output-metadata.json"
  );
  if (!existsSync(meta)) {
    console.error(`[build-mobile] sttdev verification failed: ${path.relative(frontendRoot, meta)} not found`);
    console.error("[build-mobile] gradle reported success but produced no APK metadata — treating as FATAL");
    console.error("[build-mobile] (a main-package APK here would overwrite the user's installed app on install)");
    process.exit(1);
  }
  let parsed;
  try {
    parsed = JSON.parse(readFileSync(meta, "utf8"));
  } catch (e) {
    // A read/parse failure must NOT be reported as "wrong applicationId":
    // the distinction matters, because the first means "the build is broken" and
    // the second means "the build is fine but you built the wrong thing".
    console.error(`[build-mobile] sttdev verification could not parse metadata: ${e.code || e.message}`);
    console.error("[build-mobile] this is a READ failure, not a wrong-applicationId failure");
    process.exit(1);
  }
  const appId = parsed?.applicationId;
  // ⚠️ 这里必须拿 **实际请求的后缀** 比，而不是写死 '.sttdev'。写死的话，
  // 请求 .matrix 却产出 .sttdev（或反过来）都会「验证通过」——断言在说谎。
  if (typeof appId !== "string" || !appId.endsWith(APP_ID_SUFFIX)) {
    console.error(`[build-mobile] applicationId verification failed: applicationId=${JSON.stringify(appId)}`);
    console.error(`[build-mobile] expected it to end with '${APP_ID_SUFFIX}'（MOBILE_APP_ID_SUFFIX=${process.env.MOBILE_APP_ID_SUFFIX || "(未设)"}）`);
    console.error("[build-mobile] 若产出的是 MAIN package，安装会覆盖用户手机上的正式包。不要安装它。");
    process.exit(1);
  }
  const attrs = parsed?.elements?.[0]?.attributes;
  const versionName = Array.isArray(attrs) ? attrs.find((a) => a?.name === "versionName")?.value : undefined;
  console.log(
    `[build-mobile] sttdev artifact verified: applicationId=${appId}` +
    (versionName ? ` versionName=${versionName}` : "")
  );
}

if (sttdev) {
  const androidDir = path.join(frontendRoot, "android");
  const gradlew = process.platform === "win32" ? "gradlew.bat" : "./gradlew";
  // 默认仍传 -PsttDevApp：保持既有 gradle 分支不变（只换后缀会走另一条分支，
  // 那属于没必要的变量）。非默认后缀才用 -PappIdSuffix。
  const isDefaultSuffix = APP_ID_SUFFIX === ".sttdev";
  const idFlag = isDefaultSuffix ? "-PsttDevApp" : `-PappIdSuffix=${APP_ID_SUFFIX}`;
  console.log(`[build-mobile] gradle assembleDebug ${idFlag} (coexisting package)`);
  const g = spawnSync(gradlew, ["--no-daemon", "assembleDebug", idFlag], {
    cwd: androidDir,
    env: envVars,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (g.status !== 0) {
    if (g.error) console.error(`[build-mobile] gradle could not start: ${g.error.code || g.error.message}`);
    console.error(`[build-mobile] gradle failed (exit=${g.status})`);
    process.exit(g.status ?? 1);
  }
  verifySttdevArtifact();
}

console.log(`[build-mobile] OK — ${platform}/${env} (mode=${mode}${sttdev ? ", variant=sttdev" : ""})`);