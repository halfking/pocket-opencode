#!/usr/bin/env node
// 把目标里指定的 5 个真实邮箱账户配置部署到 pocketd（admin 名下，SSOT），
// 使其可被真机客户端按需求 8 的 LWW 机制同步到本地数据库。
//
// 特性：
//  - 幂等：同 emailAddress 已存在则 PUT 刷新，否则 POST 创建；
//  - 同时写入 IMAP 与 SMTP（目标里两个都给了参数，缺 SMTP 会让 test-smtp 恒 400）；
//  - 凭证只从环境变量读，不落仓库、不打日志；
//  - 清理不属于目标清单的夹具/审计账户（--prune 才执行，默认只报告）。
//
// 用法：
//   KAIXUAN_PW=... QQ_PW=... N163_FK_PW=... N163_FK1_PW=... N163_KH_PW=... \
//   node scripts/deploy-email-accounts-to-device.mjs [--base http://127.0.0.1:8088] [--prune]
import { readFileSync } from 'node:fs';

const argv = process.argv.slice(2);
const argOf = (name, dflt) => {
  const i = argv.indexOf(`--${name}`);
  return i >= 0 && argv[i + 1] ? argv[i + 1] : dflt;
};
const BASE = argOf('base', process.env.POCKET_API_BASE || 'http://127.0.0.1:8088');
const PRUNE = argv.includes('--prune');
const USER = process.env.POCKET_ADMIN_USER || 'admin';

// 目标清单：displayName / address / imap / smtp / 密码环境变量名
const TARGETS = [
  {
    displayName: '凯轩企业邮',
    emailAddress: 'huangxutao@kxpms.cn',
    imapHost: 'imap.exmail.qq.com', imapPort: 993,
    smtpHost: 'smtp.exmail.qq.com', smtpPort: 465,
    pwEnv: 'KAIXUAN_PW',
  },
  {
    displayName: 'QQ 私人',
    emailAddress: '56551681@qq.com',
    imapHost: 'imap.qq.com', imapPort: 993,
    smtpHost: 'smtp.qq.com', smtpPort: 465,
    pwEnv: 'QQ_PW',
  },
  {
    displayName: '163 / feikemanager',
    emailAddress: 'feikemanager@163.com',
    imapHost: 'imap.163.com', imapPort: 993,
    smtpHost: 'smtp.163.com', smtpPort: 465,
    pwEnv: 'N163_FK_PW',
  },
  {
    displayName: '163 / feikemanager1',
    emailAddress: 'feikemanager1@163.com',
    imapHost: 'imap.163.com', imapPort: 993,
    smtpHost: 'smtp.163.com', smtpPort: 465,
    pwEnv: 'N163_FK1_PW',
  },
  {
    displayName: '163 / kimmy.huang',
    emailAddress: 'kimmy.huang@163.com',
    imapHost: 'imap.163.com', imapPort: 993,
    smtpHost: 'smtp.163.com', smtpPort: 465,
    pwEnv: 'N163_KH_PW',
  },
];

// dev 实例的口令兜底取自源码，与 scripts/probe-email-account-api.mjs 一致。
function devPassFromSource() {
  try {
    const s = readFileSync('backend/internal/server/server_assistant.go', 'utf8');
    return (s.match(/devPass = "([^"]+)"/) || [])[1] || '';
  } catch {
    return '';
  }
}
const PASS = process.env.POCKET_ADMIN_PASS || process.env.POCKET_AUTH_PASS || devPassFromSource();

async function api(path, { token, method = 'GET', body } = {}) {
  const res = await fetch(BASE + path, {
    method,
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* keep text */ }
  return { status: res.status, text, json };
}

const results = [];
const record = (n, pass, detail = '') => {
  results.push({ n, pass });
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${detail ? '  — ' + detail : ''}`);
};

if (!PASS) {
  console.error('[deploy] 缺少管理员口令：设置 POCKET_ADMIN_PASS 或 POCKET_AUTH_PASS');
  process.exit(1);
}

const login = await api('/api/auth/login', { method: 'POST', body: { username: USER, password: PASS } });
const token = login.json?.token;
if (!token) {
  console.error(`[deploy] 登录失败 ${login.status} ${login.text.slice(0, 200)}`);
  process.exit(1);
}
console.log(`[deploy] 已登录 ${USER} @ ${BASE}\n`);

const before = await api('/api/email/accounts', { token });
const existing = before.json?.accounts || [];
const byAddr = new Map(existing.map((a) => [a.emailAddress, a]));
const wantAddrs = new Set(TARGETS.map((t) => t.emailAddress));

// 1) 缺失凭证的账户先报出来，不静默用空密码覆盖。
const missing = TARGETS.filter((t) => !process.env[t.pwEnv]);
if (missing.length) {
  record(
    '凭证齐备（5 个账户都有密码环境变量）',
    false,
    '缺: ' + missing.map((t) => `${t.emailAddress}[${t.pwEnv}]`).join(', '),
  );
} else {
  record('凭证齐备（5 个账户都有密码环境变量）', true);
}

// 2) 逐账户 upsert，IMAP + SMTP 一起写。
for (const t of TARGETS) {
  const pw = process.env[t.pwEnv];
  if (!pw) {
    record(`upsert ${t.emailAddress}`, false, '无凭证，跳过（未改动该账户）');
    continue;
  }
  const payload = {
    displayName: t.displayName,
    imapHost: t.imapHost,
    imapPort: t.imapPort,
    authType: 'password',
    syncIntervalMin: 15,
    enabled: true,
    smtpHost: t.smtpHost,
    smtpPort: t.smtpPort,
    password: pw,
    smtpPassword: pw,
  };
  const prev = byAddr.get(t.emailAddress);
  // PUT 是 patch 语义；带上服务端当前 updatedAt 会触发 LWW 守卫（baseUpdatedAt>0
  // 且 updated_at <= base 才写），这里要的是无条件刷新，所以不传 updatedAt。
  const r = prev
    ? await api(`/api/email/accounts/${prev.id}`, { token, method: 'PUT', body: payload })
    : await api('/api/email/accounts', { token, method: 'POST', body: { ...payload, emailAddress: t.emailAddress } });
  record(`upsert ${t.emailAddress} (${prev ? 'PUT' : 'POST'})`, r.status === 200 || r.status === 201, `status=${r.status} ${r.status >= 400 ? r.text.slice(0, 160) : ''}`);
}

// 3) 夹具/审计账户：不在目标清单里，会随同步进真机列表。只报告，--prune 才删。
const extras = existing.filter((a) => !wantAddrs.has(a.emailAddress));
if (extras.length === 0) {
  record('无非目标账户残留', true);
} else if (!PRUNE) {
  record('非目标账户残留（未删除，加 --prune 才删）', false, extras.map((a) => `${a.id} ${a.emailAddress}`).join(' | '));
} else {
  for (const a of extras) {
    const r = await api(`/api/email/accounts/${a.id}`, { token, method: 'DELETE' });
    record(`prune ${a.emailAddress} (${a.id})`, r.status === 200, `status=${r.status}`);
  }
}

// 4) 回读校验：IMAP 与 SMTP 都必须落库（smtp 不在列表契约里，用 test-smtp 探）。
const after = await api('/api/email/accounts', { token });
const afterAcc = after.json?.accounts || [];
for (const t of TARGETS) {
  const a = afterAcc.find((x) => x.emailAddress === t.emailAddress);
  if (!a) {
    record(`回读 ${t.emailAddress}`, false, '账户不存在');
    continue;
  }
  const imapOk = a.imapHost === t.imapHost && a.imapPort === t.imapPort;
  const smtp = await api(`/api/email/accounts/${a.id}/test-smtp`, { token, method: 'POST', body: {} });
  // 400 + "smtp not configured" = 没落库；其它状态说明已配置（探测结果本身可能是网络失败）。
  const smtpConfigured = smtp.status !== 400 || !/smtp not configured/i.test(smtp.text);
  record(`回读 ${t.emailAddress}`, imapOk && smtpConfigured, `imap=${imapOk ? 'ok' : `${a.imapHost}:${a.imapPort}`} smtp=${smtpConfigured ? 'configured' : `未配置(${smtp.status})`}`);
}

const failed = results.filter((r) => !r.pass).length;
console.log(`\n[deploy] ${results.length - failed} PASS / ${failed} FAIL  base=${BASE}`);
process.exit(failed ? 1 : 0);
