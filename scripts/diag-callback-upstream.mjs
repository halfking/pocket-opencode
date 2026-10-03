#!/usr/bin/env node
// diag-callback-upstream.mjs — 飞书/企业微信事件回调的**上游归属**诊断。
//
// ## 它回答什么
//
// 「`https://m.kxpms.cn/callback/feishu` 能不能收到飞书的应答」这个问题，
// 用 HTTP 状态码**答不了**。错配的 upstream 一样可能返回 200/401/404，
// 状态码只反映「那个服务怎么对待一个没带签名的空请求」。
//
// 真正能分辨的是**应答格式族** —— 每种服务对空 POST 的应答形状是固定的：
//
//   pocketd/飞书   {"code":0,"msg":"ok"}              （飞书 challenge 应答）
//   pocketd/企微   success                             （企业微信明文应答）
//   AI 网关        {"error":{"code":"missing_key",…}}   （OpenAI/Anthropic 风格）
//   nginx 兜底     <html>…                             （前端 SPA）
//   不可达         502 / ECONNREFUSED
//
// 本地 pocketd 与公网域名打**同一条路径**，比对格式族：不同 ⇒ 公网那条
// 指到了别的服务。
//
// ## 2026-10-04 的实测（本脚本的由来）
//
//	本地  127.0.0.1:18099 /callback/feishu   → 200 {"code":0,"msg":"ok"}
//	公网  m.kxpms.cn      /callback/feishu   → 401 {"error":{"code":"missing_key"}}
//	本地  127.0.0.1:18099 /callback/weixin   → 400 success
//	公网  m.kxpms.cn      /callback/weixin   → 401 {"error":{"code":"missing_key"}}
//
// 同一域名下 `/healthz` 返回的是真 pocketd（`{"git_sha":…,"ready":true}`），
// `/api/healthz` 返回的是 pocketd 的 404，但 `/callback/*` 的应答族与之**都不同**。
// 响应头也对得上：公网 `/callback/*` 的 CORS 允许头里含 `X-Gw-Project-Id`
// （Gw = Gateway），且缺 pocketd 特有的 `Permissions-Policy` / `Referrer-Policy`。
//
// ⇒ 后端协议实现正确，是**边缘 upstream 指错**。这类故障在仓内没有任何
// 配置能看出来：`deploy/edge/` 下 10 个 conf 里没有任何一个的 server_name
// 是 `m.kxpms.cn`（它们是 openpocket-api/web.kxpms.cn、openpocket.kxpms.cn、
// pocket.kxpms.cn、*.itestu.cn），说明这个 vhost 是边缘上手工配的、没进 SSOT。
//
// ## 用法
//
//	node scripts/diag-callback-upstream.mjs
//	node scripts/diag-callback-upstream.mjs --public https://m.kxpms.cn --local http://127.0.0.1:18099
//	node scripts/diag-callback-upstream.mjs --self-test
//
// ## 它**不做**什么
//
// 只发空 POST（无签名、无事件体），不携带任何凭据，也不会触发任何副作用。
// 签名校验通过后的完整事件流不在本脚本范围内。

const args = process.argv.slice(2);
const opt = (name, dflt) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 && args[i + 1] ? args[i + 1] : dflt;
};
const has = (name) => args.includes(`--${name}`);

const PUBLIC_BASE = opt('public', 'https://m.kxpms.cn');
const LOCAL_BASE = opt('local', 'http://127.0.0.1:18099');

// ---------------------------------------------------------------------------
// classify：把一次应答归到「格式族」。
//
// 刻意**不**看状态码作为主判据：错配的 upstream 一样可能 200。
// 状态码只作为附注打印，帮助人区分「服务拒收」与「服务不是它」。
// ---------------------------------------------------------------------------
export function classify(body, status) {
  const t = typeof body === 'string' ? body.trim() : '';
  if (t === '') return { family: 'empty', detail: `HTTP ${status} 空应答` };
  if (/^<\s*(!doctype|html)/i.test(t)) {
    return { family: 'spa-html', detail: '返回了 HTML —— 请求落到了前端而不是 API' };
  }
  // 先试 JSON：飞书/企微/网关三种都是 JSON。
  let j = null;
  try {
    j = JSON.parse(t);
  } catch {
    /* 非 JSON，走下面的纯文本判定 */
  }
  if (j) {
    // AI 网关：OpenAI/Anthropic 风格的 error 包装
    if (j.error && typeof j.error === 'object' && typeof j.error.code === 'string') {
      return {
        family: 'gateway-authz',
        detail: `AI 网关鉴权错误 error.code=${j.error.code}（HTTP ${status}）`,
      };
    }
    // 飞书：challenge 应答固定是 {"code":<int>,"msg"/"message":<str>}
    if (typeof j.code === 'number' && (j.msg !== undefined || j.message !== undefined)) {
      return { family: 'feishu-handler', detail: `飞书应答 code=${j.code}（HTTP ${status}）` };
    }
    // 企业微信：明文 success，或 JSON 带 errcode/echostr
    if (typeof j.errcode === 'number' || typeof j.echostr === 'string') {
      return { family: 'wecom-handler', detail: `企业微信应答 errcode=${j.errcode ?? '-'}（HTTP ${status}）` };
    }
    return { family: 'json-other', detail: `JSON 但不属于任何已知族：${t.slice(0, 120)}` };
  }
  if (t === 'success') {
    return { family: 'wecom-handler', detail: `企业微信明文 success（HTTP ${status}）` };
  }
  return { family: 'text-other', detail: `非 JSON 且非 success：${t.slice(0, 120)}` };
}

// pocketd 自身身份的判据：/healthz 的应答形状。
//
// ⚠️ 必须认**两种**形态。实测同一仓库的两种构建：
//
//	本地开发/精简构建  → 200 text/plain "ok"        （Content-Length: 2）
//	生产构建            → 200 JSON {"version":…,"git_sha":…,"ready":true}
//
// 第一版只认 JSON，于是本地那台被判成「不是 pocketd」，进而把整份报告
// 标成「本机后端没起来，下面所有对比都无意义」—— 而后端明明是活的，
// /callback/feishu 也正确返回了飞书应答。这是**判据对一种真实形态失明**，
// 且失明方向是「制造一个假的阻断理由」。
export function classifyHealth(body) {
  const t = typeof body === 'string' ? body.trim() : '';
  if (t === 'ok') {
    return { isPocketd: true, variant: 'minimal', detail: 'pocketd（精简构建：/healthz = "ok"）' };
  }
  let j = null;
  try {
    j = JSON.parse(t);
  } catch {
    return { isPocketd: false, variant: 'none', detail: `非 JSON：${t.slice(0, 80) || '(空)'}` };
  }
  const isPocketd = !!j && (j.ready !== undefined || j.git_sha !== undefined);
  return {
    isPocketd,
    variant: isPocketd ? 'full' : 'none',
    detail: isPocketd
      ? `pocketd ${j.version ?? '?'} git_sha=${j.git_sha ?? '?'} ready=${j.ready}`
      : `不是 pocketd：${t.slice(0, 120)}`,
  };
}

// ---------------------------------------------------------------------------
// 探测
// ---------------------------------------------------------------------------
const PATHS = [
  { path: '/healthz', kind: 'health', label: '身份探测' },
  { path: '/callback/feishu', kind: 'feishu', label: '飞书回调' },
  { path: '/callback/weixin', kind: 'wecom', label: '企业微信回调' },
];

async function probe(base, spec) {
  const url = base.replace(/\/+$/, '') + spec.path;
  const started = Date.now();
  try {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
      signal: AbortSignal.timeout(15000),
    });
    const body = await res.text();
    const ms = Date.now() - started;
    const verdict =
      spec.kind === 'health' ? classifyHealth(body) : classify(body, res.status);
    const hdr = res.headers;
    return {
      ok: true,
      status: res.status,
      ms,
      verdict,
      // 区分「指到了 pocketd」与「指到了别的 pocketd 实例/网关」的辅助证据
      hints: {
        permissionsPolicy: hdr.get('permissions-policy') ? '有' : '无',
        strictTransport: hdr.get('strict-transport-security') ? '有' : '无',
        // CORS 允许头里出现 X-Gw-Project-Id 说明前面是 AI 网关
        allowHeaders: hdr.get('access-control-allow-headers') ?? '',
        server: hdr.get('server') ?? '',
      },
    };
  } catch (e) {
    return {
      ok: false,
      status: 0,
      ms: Date.now() - started,
      verdict: { family: 'unreachable', detail: String(e.message ?? e) },
      hints: {},
    };
  }
}

async function selfTest() {
  // 自证：分类器必须能区分每一族。负控 = 删掉任一 case 后本函数必须红。
  const cases = [
    ['{"code":0,"msg":"ok"}', 200, 'feishu-handler', 'pocketd 对空 POST 的飞书 challenge 应答'],
    ['success', 400, 'wecom-handler', 'pocketd 对缺签名企微请求的明文拒绝'],
    ['{"errcode":40001,"errmsg":"bad"}', 400, 'wecom-handler', '企微 JSON 形态'],
    ['{"error":{"code":"missing_key","type":"authentication_error"}}', 401, 'gateway-authz', 'AI 网关鉴权'],
    ['<!DOCTYPE html><html><body>x</body></html>', 200, 'spa-html', '请求落进前端'],
    ['{"hello":"world"}', 200, 'json-other', '不属任何族'],
  ];
  let bad = 0;
  for (const [body, status, want, why] of cases) {
    const got = classify(body, status).family;
    const ok = got === want;
    if (!ok) bad++;
    console.log(`${ok ? '✅' : '❌'} classify(${JSON.stringify(body.slice(0, 42))}) = ${got}` +
      (ok ? '' : `（期望 ${want}）`) + `  ← ${why}`);
  }
  const h = classifyHealth('{"ready":true,"git_sha":"ed827caa","version":"2.5.8"}');
  if (!h.isPocketd) {
    bad++;
    console.log('❌ classifyHealth 对真 pocketd（结构化 JSON）返回 isPocketd=false');
  } else {
    console.log(`✅ classifyHealth 认出结构化 pocketd：${h.detail}`);
  }
  // 精简构建的 /healthz 形态：纯文本 "ok"。第一版漏了它，于是本地后端被判成
  // 「没起来」—— 判据对一种**真实存在的**形态失明，且失明方向是假阻断。
  const h1 = classifyHealth('ok');
  if (!h1.isPocketd) {
    bad++;
    console.log('❌ classifyHealth 对精简构建的 /healthz="ok" 返回 isPocketd=false');
  } else {
    console.log(`✅ classifyHealth 认出精简构建 pocketd：${h1.detail}`);
  }
  const h2 = classifyHealth('{"error":"nope"}');
  if (h2.isPocketd) {
    bad++;
    console.log('❌ classifyHealth 对非 pocketd 返回 isPocketd=true（这会让每个域名都「通过」）');
  } else {
    console.log('✅ classifyHealth 正确拒绝非 pocketd 应答');
  }
  const h3 = classifyHealth('');
  if (h3.isPocketd) {
    bad++;
    console.log('❌ classifyHealth 对空应答返回 isPocketd=true');
  } else {
    console.log('✅ classifyHealth 正确拒绝空应答');
  }
  console.log(bad === 0 ? '\n自证通过：分类器在每一族上都成立。' : `\n自证失败：${bad} 项。`);
  process.exit(bad === 0 ? 0 : 1);
}

async function main() {
  if (has('self-test')) return selfTest();

  console.log(`公网：${PUBLIC_BASE}`);
  console.log(`本地：${LOCAL_BASE}`);
  console.log('（全部为空 POST，不带任何签名或凭据；只探路由归属，无副作用）\n');

  const rows = [];
  for (const spec of PATHS) {
    const local = await probe(LOCAL_BASE, spec);
    const pub = await probe(PUBLIC_BASE, spec);
    rows.push({ spec, local, pub });
    console.log(`── ${spec.path}（${spec.label}）`);
    console.log(`   本地  HTTP ${local.status} ${local.ms}ms  ${local.verdict.detail}`);
    console.log(`   公网  HTTP ${pub.status} ${pub.ms}ms  ${pub.verdict.detail}`);
    console.log('');
  }

  console.log('══ 结论 ══');
  const localHealth = rows[0].local.verdict;
  const pubHealth = rows[0].pub.verdict;
  let problems = 0;

  // 身份确认是**参考**不是门禁。真正的结论依据是 /callback/* 的格式族对比 ——
  // 那个判据不依赖 /healthz 认不认得。反过来，「/healthz 不认识 ⇒ 全部无意义」
  // 是个危险的推论：第一版就这么写，结果把一个形态未覆盖的判据变成了
  // 假的阻断理由（本地精简构建 /healthz = "ok" 被判成「不是 pocketd」）。
  if (localHealth.isPocketd) {
    console.log(`ℹ️  本地后端身份：${localHealth.detail}`);
  } else {
    console.log(`⚠️  本地 ${LOCAL_BASE}/healthz 未被认成 pocketd（${localHealth.detail}）。`);
    console.log('    这不影响下面的结论 —— 比对依据是 /callback/* 的应答格式族。');
  }
  if (!rows[1].local.ok) {
    problems++;
    console.log(`❌ 本地 ${LOCAL_BASE}/callback/ 不可达：无法建立基准，请先起后端。`);
  }

  for (const { spec, local, pub } of rows.slice(1)) {
    if (spec.kind !== 'feishu' && spec.kind !== 'wecom') continue;
    if (!local.ok) {
      problems++;
      console.log(`❌ ${spec.path}：本地不可达，无法建立基准。`);
      continue;
    }
    if (pub.family === local.verdict.family) {
      console.log(`✅ ${spec.path}：公网与本地同族（${pub.verdict.family}）—— 上游正确。`);
      continue;
    }
    problems++;
    console.log(`❌ ${spec.path}：上游指错！`);
    console.log(`     本地 = ${local.verdict.family}：${local.verdict.detail}`);
    console.log(`     公网 = ${pub.verdict.family}：${pub.verdict.detail}`);
    const allow = pub.hints.allowHeaders ?? '';
    if (/X-Gw-Project-Id/i.test(allow)) {
      console.log('     证据：公网响应的 CORS 允许头含 X-Gw-Project-Id —— 前面是 AI 网关，不是 pocketd。');
    }
    if (pub.hints.permissionsPolicy === '无' && local.hints.permissionsPolicy === '有') {
      console.log('     证据：公网缺少 pocketd 特有的 Permissions-Policy 响应头。');
    }
    console.log('     修法：把 m.kxpms.cn 的 /callback/ 指到 pocketd（与 /api/、/ws 同一个 upstream），');
    console.log('           不是前端、也不是 AI 网关。仓内 deploy/edge/ 下没有该域名的 vhost 模板，');
    console.log('           说明它是边缘上手工配的 —— 建议补进 SSOT。');
  }

  if (pubHealth.isPocketd) {
    console.log(`ℹ️  公网 ${PUBLIC_BASE}/healthz 本身是 pocketd（${pubHealth.detail}）——`);
    console.log('    所以这不是「整个域名指错」，而是**该域名下 /callback/ 的 location 指错**。');
  }

  process.exit(problems === 0 ? 0 : 1);
}

main();
