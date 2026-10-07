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
  /** 「这次没得出结论」的判别：探针**超时**或子进程**压根没起来**。
   *  这两种与「判据判定为假」是两件事 —— 混在一起报，会让下一次重跑去改一个没坏的东西。
   *  退出码沿用本函数内已有的 2（见下面 MIN_SELFTEST_CASES 那道），
   *  与 audit-doc-encoding.mjs 的「2=量具坏 / 1=文档坏」同约定。
   *  ⚠ 声明必须在**首次使用之前**——放在 GUARD_TIMEOUT_MS 旁边会踩 const 的 TDZ
   *  （node --check 是语法检查，查不出这种运行时错误）。 */
  const NO_VERDICT = (why) => /timed out/i.test(String(why || ''))
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
  cases.push({ subject: 'TCP 可达性', name: '活端口必须可达', got: r1.ok, want: true, why: r1.why, inconclusive: NO_VERDICT(r1.why) })
  const r2 = await tcpReachable('127.0.0.1', deadPort, 2000)
  cases.push({ subject: 'TCP 可达性', name: '死端口必须不可达', got: r2.ok, want: false, why: r2.why, inconclusive: NO_VERDICT(r2.why) })

  // 3) ★ 事故守卫本身（2026-10-07 新增，见本文件 §116 的实测）。
  //    上面两条只测**辅助函数** tcpReachable。而 2026-09-05 那次真机事故
  //    （空 API base ⇒ 每个 /api 请求拿到 index.html）是由**守卫的决定**挡住的，
  //    那个决定**一条用例都没有**：
  //      实测把整个 `if (!effectiveAPIBase && …)` 换成 `if (false)` ⇒
  //      `npm run gates` **37/37 全绿**，包括本自检本身。
  //    ⇒ 守卫可以在完全无声的情况下被摘掉，而事故防护归零。
  //
  //    为什么能用子进程测：这些守卫在任何构建动作**之前**就 exit 1（实测各 <0.1s），
  //    所以这些用例不碰 vite、不碰 gradle、不需要设备。
  //    ⚠ 判据必须同时要求 **退出码是 1** 且 **输出里有该守卫自己的那句话**：
  //      只看「非 0」的话，参数写错等别的 exit 1 也能把它顶成绿；
  //      而守卫被摘掉时子进程会一路走去跑 vite build ⇒ 必须给 timeout，
  //      超时返回 status===null，同样判红（否则「跑太久」会被误当成「守卫拦住了」）。
  const GUARD_TIMEOUT_MS = 15000
  /** 跑一次真脚本，问「这道守卫拦住没有」。saw 是守卫自己的报错文案片段。 */
  const runGuard = (label, args, envPatch, saw) => {
    const t0 = Date.now()
    const r = spawnSync(process.execPath, [fileURLToPath(import.meta.url), ...args], {
      // 逃生舱一律置空：本机若恰好开着它，用例就会测成假的。
      // 置空而不是 delete —— 空串足以让 `!== "1"` 为真，且不依赖外层环境。
      env: { ...process.env, MOBILE_ALLOW_EMPTY_API_BASE: '', MOBILE_SKIP_REACHABILITY: '', ...envPatch },
      encoding: 'utf8',
      timeout: GUARD_TIMEOUT_MS,
    })
    const elapsedMs = Date.now() - t0
    const said = `${r.stderr || ''}${r.stdout || ''}`.includes(saw)
    // ⚠ **超时 ≠ 守卫失效**。原来这两件事被并成同一句话：
    //   超时只说明这台机器那一刻慢，守卫完全可能是好的（实测子进程正常只要 0.1–0.4s，
    //   而这个上限是 15s ⇒ 裕度 38–118×）；ENOENT 则是**子进程压根没起来**，守卫根本没被检验。
    //   `got` **一个字不改** ⇒ 通过/不通过的口径零变化；这里只把失败消息修成
    //   「这次失败真正测到的那件事」（对照 audit-doc-encoding.mjs 的 2=量具坏 / 1=文档坏）。
    const timedOut = !!(r.error && (r.error.code === 'ETIMEDOUT' || r.signal === 'SIGTERM'))
    const spawnFail = !!r.error && !timedOut
    cases.push({
      subject: 'runGuard 守卫',
      name: label,
      got: r.status === 1 && said,
      want: true,
      inconclusive: timedOut || spawnFail,
      why: timedOut
        ? `子进程在 ${GUARD_TIMEOUT_MS / 1000}s 内没结束（${r.error.code || r.signal}）`
          + ` · **本例实测 ${elapsedMs}ms vs 上限 ${GUARD_TIMEOUT_MS}ms**`
          + ` ⇒ 机器慢/环境问题，**不是守卫失效**；本例未得出结论`
        : spawnFail
          ? `子进程没能启动（${r.error.code || r.error.message}）`
            + ` ⇒ 守卫**根本没被检验**；本例未得出结论`
          : `exit=${r.status}${said ? '' : ` · 缺少守卫自己的报错文案「${saw}」`}`,
    })
  }

  // 3) 空 API base —— 2026-09-05 真机事故本体。
  //    显式置空：?? 只对 null/undefined 回退，空串正好钉住「空值」这条路径。
  runGuard('空 VITE_API_BASE 必须拒绝构建', ['android', 'dev'], { VITE_API_BASE: '' }, 'VITE_API_BASE is empty')

  // 4) 同一族的三个兄弟守卫。它们防的是**同一个失效模式**：
  //    打出一个 /api 拿不到 JSON 的包（2026-09-05 事故的形态），
  //    只是入口不同——空值 / 非绝对 URL / prod 指向 LAN。
  //    实测摘掉其中任何一道，gates 都不会红（与 §116 的 M9 同一形状）。
  //    ★ prod 那两条尤其硬：它们拦住的是**发给真实用户**的包指向 192.168.x.x。
  runGuard(
    '任意构建 + 非绝对 URL 必须拒绝',
    ['android', 'dev'],
    { VITE_API_BASE: 'not-a-url' },
    'VITE_API_BASE is not an absolute URL',
  )
  runGuard(
    'prod + 非绝对 URL 必须拒绝',
    ['android', 'prod'],
    { VITE_API_BASE: 'not-a-url' },
    'refusing production build: VITE_API_BASE is not an absolute URL',
  )
  runGuard(
    'prod + LAN/loopback 主机必须拒绝',
    ['android', 'prod'],
    { VITE_API_BASE: 'http://192.168.31.37:8090' },
    'is loopback/LAN/placeholder',
  )
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

  // ★ 下限闸：`cases` 只由**真实调用之后**的 push 填充，所以把两条 push 删掉，
  //   `cases.length` 就是 0、`bad` 也是 0 ⇒ 打印「自检 0/0 通过」并 exit 0。
  //   实测（2026-10-07）：删掉两条 push ⇒ `自检 0/0 通过`、EXIT=0，全绿。
  //   这与 docs/design §91.7、§92 是**同一个形状**（本仓第三次出现）：
  //   自检的成功判据由「声明量」而不是「实际执行量」驱动。
  //   下面是第三道（§87 修过 §92 那道，§91.7 修过 vm-gaps 那道）。
  //   需要放宽只能手工改这个常量，不接受命令行参数。
  //   ⚠ 2026-10-08 提高 2 -> 6（docs/design §204.5 / §230）：原来 6 例配下限 2，
  //     而**下限恰好等于两条不承重夹具的条数** ⇒ 删掉全部 4 条 runGuard 用例后
  //     cases.length 仍是 2、仍然 ≥ 2、门照样全绿。
  const MIN_SELFTEST_CASES = 6;
  if (cases.length < MIN_SELFTEST_CASES) {
    console.error(
      `[build-mobile] 自检只跑了 ${cases.length}/${MIN_SELFTEST_CASES} 例 —— 夹具循环或 push 被改过。`,
    );
    console.error('   「0/0 通过」不是通过：那是判据失明时的读数，和真通过长得一模一样。');
    process.exit(2);
  }

  // ★★★ 分组下限（§206 待拍板第 4 条）。总条数下限按上面那条已经提到 6，
  //   但「总数够」与「每一类都还在」不是同一件事 —— 这是 §230 那道算术题在
  //   本文件的形状：删掉一整组、别的组不动，总数照样够。
  //
  //   runGuard 这一组尤其要单列：它防的是 2026-09-05 真机事故本体
  //   （空/非绝对/LAN API base ⇒ 每个 /api 请求拿到 index.html），
  //   而实测**摘掉其中任何一道，gates 都不会红**（§116 的 M9 同一形状）。
  const REQUIRED_COVERAGE = [
    ['TCP 可达性', 2],    // 实测 2 —— 只测辅助函数 tcpReachable
    ['runGuard 守卫', 4],  // 实测 4 —— 空 base / 非绝对 URL / prod 非绝对 / prod LAN
  ]
  for (const [subject, min] of REQUIRED_COVERAGE) {
    const n = cases.filter((c) => c.subject === subject).length
    if (n < min) {
      console.error(`[build-mobile] 被测面「${subject}」只剩 ${n} 条用例（下限 ${min}）—— 这一组被删光或腰斩了。`)
      process.exit(2);
    }
    console.log(`  覆盖 ${subject}: ${n} 条（下限 ${min}）`)
  }

  let bad = 0
  let noVerdict = 0
  for (const c of cases) {
    const ok = c.got === c.want
    // ⚠ 优先于 🔴：没得出结论时，`got` 的真假不可信，不能当成「判定为假」记进 bad
    if (c.inconclusive) { noVerdict++; console.log(`  ⚠️  ${c.name}：**未得出结论**${c.why ? '（' + c.why + '）' : ''}`); continue }
    if (!ok) bad++
    console.log(`  ${ok ? '🟢' : '🔴'} ${c.name}：got=${c.got} want=${c.want}${c.why ? '（' + c.why + '）' : ''}`)
  }
  const judged = cases.length - noVerdict
  console.log(
    `\n[build-mobile] 自检 实跑 ${cases.length} 例，通过 ${judged - bad} 例` +
    (noVerdict ? `，另有 ${noVerdict} 例**未得出结论**` : ''),
  )
  if (noVerdict) {
    console.error('[build-mobile] ⚠️ 上面这些例子**没得出结论**（探针超时或子进程没起来），不是判据判定为假。')
    console.error('   量具侧的问题（机器慢 / 环境 / 起不来），请重跑；判据本身是否有牙要等它们真出结论才算数。')
    console.error('   退出码 2 = 量具失效（与下面 MIN_SELFTEST_CASES、audit-doc-encoding.mjs 同一约定），不是断言失败。')
    process.exit(2)
  }
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

// ---- absolute-URL guard（非 production 模式；prod 由上面那道更严的守）----
// 2026-10-08 抽出：这个检查原先**寄居在下面的可达性守卫里**
// （`if (target.parseError) { … exit 1 }`）⇒ 它是 `MOBILE_SKIP_REACHABILITY=1`
// 的**条件分支内部**，于是那个逃生舱会把它一起关掉 —— 实测设了逃生舱就能用
// 相对 URL（`not-a-url`）出包。
// 而它判的是「值合不合法」，可达性判的是「机器还在不在」，**两件事必须分开**：
// 逃生舱的用途是「那台机器暂时不在，但值是对的」，它不该顺带把合法性校验也免掉。
//
// 放在 production host guard **之后**：prod 有自己更严的一套（含 LAN/环回/占位符），
// 报错文案也不同，不能被这条抢走。
if (effectiveAPIBase) {
  try {
    new URL(effectiveAPIBase);
  } catch {
    console.error(
      `[build-mobile] refusing to build ${platform}/${env}: VITE_API_BASE is not an absolute URL: ${effectiveAPIBase}`,
    );
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

  // 兜底：URL 合法性已由上面的 absolute-URL guard 判过，正常走不到这里。
  // 保留是为了**失败方向安全** —— 若将来有人把它挪到那道守卫之前（例如挪进
  // 某个更早的分支），这里仍会拒绝而不是静默跳过可达性检查。
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

// ⚠ env 文件存在性检查的位置（2026-10-08 调整）：它**故意排在 API base 守卫之后**。
// 此前排在守卫之前 ⇒ 干净检出里（.env.android-dev 被 .gitignore 的 .env.* 排除、
// 从未入库）`build-mobile.mjs android dev` 一进来就以「missing env file」退出，
// **API base 守卫压根没被执行到** ⇒ check:build-mobile-selftest 的两条 android/dev
// 用例在 CI 上恒红；又因 run-gates --ci 失败即停 ⇒ 后 30 条门禁在 CI 上一次都没跑过。
//
// 守卫的判定不受该文件影响：自检显式传的 VITE_API_BASE 空串经 Vite 优先级遮蔽了
// 文件里的值（见上方 effectiveAPIBase 的 `??` 链），文件在不在都不改变结论。
// 唯一的行为变化：两个条件同时成立时**先报哪一句**——先报更可操作的 base 问题，
// 紧接着仍会报 missing env file，两句都在。

if (!existsSync(envFile) && mode !== "production") {
  console.error(`[build-mobile] missing env file: ${envFile}`);
  console.error(`[build-mobile] expected ${mode} profile for ${platform}/${env}`);
  process.exit(1);
}

// ⚠ env 文件存在性检查的位置（2026-10-08 调整）：它排在**全部前置守卫之后**。
//
// 此前它排在守卫之前 ⇒ 干净检出里（.env.android-dev 被 .gitignore 的 .env.* 排除、
// 从未入库）`build-mobile.mjs android dev` 一进来就以「missing env file」退出，
// **API base 的三道守卫一道都没被执行到** ⇒ check:build-mobile-selftest 的两条
// android/dev 用例在 CI 上恒红；又因 run-gates --ci 失败即停
// ⇒ 后 30 条门禁在 CI 上一次都没跑过。
//
// 为什么放在守卫之后是对的：守卫判的是「这个包安不安全」（base 空/非法/主机不可达），
// 而本段判的是「配置齐不齐」。**安全判断不该被配置完整性挡在前面** ——
// 挡在前面的后果不是「早失败」，是「该跑的守卫没跑」。
//
// 行为变化只有一处：配置缺失**且** base 也不安全时，先报更可操作的安全问题，
// 紧接着仍会报 missing env file，两句都在。

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