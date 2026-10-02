#!/usr/bin/env node
/**
 * verify-email-writepath.mjs — **真机**验证邮箱模块的 UI 写路径。
 *
 * ## 为什么先探 API 再点 UI
 *
 * `EmailAccountAddView` 的 saveAndVerify 是 `addAccount()` → `syncNow()` → `testSmtp()`
 * 串起来的。IMAP 连不上（没有真实 IMAP 服务）时后两步必然失败，但**第一步已经写库了**。
 * 所以「界面显示失败」≠「没写成功」，必须把两者分开判：
 *   - 写路径判据 = **直接查 PG** 有没有新行（唯一可信来源）
 *   - 反馈判据   = 界面是否说「已保存…但连接未全部通过」（而不是笼统报错）
 *
 * ## 判据设计
 *
 * 1. **前置**：直接查 PG 记下 email_accounts 行数
 * 2. **向导可达**：#/email/accounts/new 能进到 step2（说明 CTA 不是死路）
 * 3. **表单校验有反馈**：空提交被拦（防止「无脑点也能过」）
 * 4. **真能写**：填完提交 → **直接查 PG** 行数 +1 且 email_address 匹配
 * 5. **反馈正确**：界面区分「已保存」与「连接未全部通过」
 * 6. **对照组**：API 直接建的那条（PROBE-*）仍在，证明不是 UI 幻觉
 *
 * ## 不做的判断
 *
 * IMAP/SMTP **真的能不能连通**不在本脚本范围内 —— 没有真实 IMAP 服务，
 * 硬要判「连通」只会得到一个恒失败的结果。脚本只判「写路径通不通 + 反馈准不准」。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/verify-email-writepath.mjs
 */
import { execFileSync } from 'node:child_process';
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9253'
const MASTER = process.env.POCKET_MASTER || ''

function resolvePsql() {
  const cands = [process.env.POCKET_PSQL, 'logs/pg/dist2/pgsql/bin/psql.exe', 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'].filter(Boolean)
  for (const c of cands) { try { execFileSync(c, ['--version'], { stdio: 'ignore' }); return c } catch { /* next */ } }
  console.error('找不到 psql.exe，请设置 POCKET_PSQL')
  process.exit(4)
}
const PSQL = resolvePsql()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const psql = (sql) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim()

// 兜底串必须纯 ASCII：中文经系统 ANSI 码页传给 psql 会报 invalid byte sequence
const acctCount = () => Number(psql('select count(*) from opencode_pocket.email_accounts;').match(/-?\d+/)?.[0] ?? NaN)
const acctAddresses = () => psql("select coalesce(string_agg(email_address,'|' order by created_at),'(none)') from opencode_pocket.email_accounts;")

// ---------- 连 CDP ----------
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const errors = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.exceptionThrown') errors.push(m.params?.exceptionDetails?.exception?.description || m.params?.exceptionDetails?.text || '')
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await send('Network.enable')
// 记下 /api/ 的往返（含方法与状态码）——「PG 没变」说不清是「没发请求」
// 还是「被拒了」，把状态码摆出来才能一眼定性。
const apiLog = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  if (m.method === 'Network.requestWillBeSent' && String(p.request?.url || '').includes('/api/')) {
    apiLog.push({ url: p.request.url.replace(/https?:\/\/[^/]+/, ''), method: p.request.method, status: null })
  }
  if (m.method === 'Network.responseReceived' && String(p.response?.url || '').includes('/api/')) {
    const url = p.response.url.replace(/https?:\/\/[^/]+/, '')
    const hit = apiLog.find((r) => r.url === url && r.status === null)
    if (hit) hit.status = p.response.status
  }
  if (m.method === 'Runtime.exceptionThrown') errors.push(m.params?.exceptionDetails?.exception?.description || m.params?.exceptionDetails?.text || '')
})

const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

let origin = null
const readyDl = Date.now() + 20000
while (Date.now() < readyDl) { origin = await ev('location.origin'); if (origin && origin !== 'null') break; await sleep(500) }
console.log('origin =', origin, '（必须是 http://localhost）')
if (origin !== 'http://localhost') { console.log('非 dev 包或 WebView 未就绪，中止。'); process.exit(5) }

// ---------- 登录 ----------
await ev(`location.hash = '#/login'`); await sleep(2600)
if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(1700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b)b.click();return 1})()`)
  await sleep(4200)
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const devPass = requireDevPass()
  const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`
  await ev(fillBy('input[placeholder*="用户名"]', 'admin'))
  await ev(fillBy('input[type="password"]', devPass)); await sleep(900)
  // 精确匹配「登录」：页面上还有「密码登录」tab，用 indexOf 会点错
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});if(b)b.click();return b?1:0})()`)
  await sleep(6500)
}

const checks = []
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`) }
const STAMP = Date.now().toString().slice(-6)
const TEST_ADDR = `uidemo${STAMP}@example.com`

// FoldAwareLayout 故意同时渲染两个 slot，必须限定可见 pane
const PANE = `(function(){var ps=document.querySelectorAll('.inner-pane, .outer-pane');for(var i=0;i<ps.length;i++){if(ps[i].offsetParent!==null)return ps[i];}return document.body;})()`

// ---------- 1. 前置 ----------
const before = acctCount()
console.log(`前置：PG email_accounts = ${before}，地址 = ${acctAddresses()}`)
check('前置：直接查 PG 拿到基线行数', Number.isFinite(before), `count=${before}`)

// ---------- 2. 进新增向导 ----------
await ev(`location.hash = '#/email/accounts/new'`)
const dl = Date.now() + 15000
while (Date.now() < dl && (await ev('location.hash')) !== '#/email/accounts/new') await sleep(300)
await sleep(2200)

// step1：选服务商。选「其他」以免 applyProvider 预填真实 IMAP 主机
const picked = await ev(`(function(){
  var p=(${PANE});
  var bs=p.querySelectorAll('button.prov');
  for(var i=0;i<bs.length;i++){ if((bs[i].textContent||'').indexOf('其他')>=0){ bs[i].click(); return 'other'; } }
  if(bs.length){ bs[bs.length-1].click(); return 'last-fallback'; }
  return 'none';
})()`)
await sleep(1200)
const step2 = await ev(`(function(){return (document.body.innerText||'').indexOf('邮箱地址')>=0})()`)
check('新增向导可达：从 step1 选服务商后进入 step2（CTA 不是死路）', picked !== 'none' && step2 === true, `picked=${picked} step2=${step2}`)

// ---------- 3. 空提交应被拦（对照组：证明不是无脑点也能过）----------
const emptyGuard = await ev(`(function(){
  var p=(${PANE});
  var b=Array.prototype.slice.call(p.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('保存并测试收发')>=0});
  if(!b) return 'NO_BTN';
  b.click();
  return 'clicked';
})()`)
await sleep(1200)
const errShown = await ev(`(function(){var e=document.querySelector('.err');return e?(e.textContent||'').trim():''})()`)
check('对照组：空表单提交被校验拦下（证明提交路径有真实校验）', emptyGuard === 'clicked' && !!errShown, `err="${errShown}"`)

// ---------- 4. 真填真提交 ----------
// 按**字段标签文字**定位 input，不要写死 placeholder：
// 「其他」服务商的显示名 placeholder 是「其他 IMAP」，写死 `placeholder="其他"`
// 会 NF（踩过一次）。标签文案是稳定契约，placeholder 随服务商变。
const fillByLabel = (labelText, val) => `(function(){
  var root=(${PANE});
  var fs=root.querySelectorAll('label.field');
  for(var i=0;i<fs.length;i++){
    var sp=fs[i].querySelector('span');
    if(sp && (sp.textContent||'').indexOf(${JSON.stringify(labelText)})>=0){
      var el=fs[i].querySelector('input');
      if(!el) return 'NO_INPUT';
      var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
      s.call(el,${JSON.stringify(val)});
      el.dispatchEvent(new Event('input',{bubbles:true}));
      el.dispatchEvent(new Event('change',{bubbles:true}));
      return 'ok';
    }
  }
  return 'NO_LABEL';
})()`
const fillOne = (sel, val) => `(function(){
  var el=(${PANE}).querySelector(${JSON.stringify(sel)});
  if(!el) return 'NF';
  var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
  s.call(el,${JSON.stringify(val)});
  el.dispatchEvent(new Event('input',{bubbles:true}));
  el.dispatchEvent(new Event('change',{bubbles:true}));
  return 'ok';
})()`
const fills = {
  email: await ev(fillByLabel('邮箱地址', TEST_ADDR)),
  display: await ev(fillByLabel('显示名', `UI演示${STAMP}`)),
  pwd: await ev(fillByLabel('IMAP 密码', 'dummy-not-a-real-password')),
  imap: await ev(fillByLabel('IMAP 主机', 'imap.invalid.test')),
}
check('四个字段都能填入（表单不是只读的假界面）', Object.values(fills).every((v) => v === 'ok'), JSON.stringify(fills))
await sleep(800)

const readBack = await ev(`(function(){var el=(${PANE}).querySelector('input[type="email"]');return el?el.value:''})()`)
check('填入值回读一致（不靠「看起来填了」）', readBack === TEST_ADDR, `readBack=${readBack}`)

const beforeSubmit = acctCount()
apiLog.length = 0
await ev(`(function(){
  var p=(${PANE});
  var b=Array.prototype.slice.call(p.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('保存并测试收发')>=0});
  if(b)b.click(); return 1;
})()`)

// 等状态到达期望：按钮从 busy 恢复 或 出现结果面板。**不固定 sleep。**
const waitDl = Date.now() + 30000
let resultText = ''
while (Date.now() < waitDl) {
  resultText = (await ev(`document.body.innerText.replace(/\\s+/g,' ')`)) || ''
  if (/已添加|未完成|已保存/.test(resultText)) break
  await sleep(600)
}
console.log('   结果面板文本 =', JSON.stringify((resultText.match(/.{0,90}(已添加|未完成|已保存).{0,90}/) || [''])[0]))

// ---------- 5. 落库判据：直接查 PG ----------
const after = acctCount()
const addrs = acctAddresses()
check('**直接查 PG** 确认真的写进去了（唯一能排除 UI 假象的判据）', after === beforeSubmit + 1,
  `${beforeSubmit} -> ${after}`)
check('PG 里的 email_address 与 UI 填入值一致', addrs.includes(TEST_ADDR), `PG=${addrs}`)

// ---------- 5b. API 层证据 ----------
// 「PG 没变」本身说不清是「没发请求」还是「被服务器拒了」。抓 Network 层
// 把状态码摆出来，才能一眼定性。BUG-AB 就是靠这一步才看清是 **400**
// 「smtpHost required when smtpPassword is provided」，而不是猜出来的。
const acctReq = apiLog.find((r) => r.url.includes('/api/email/accounts') && r.method !== 'GET')
console.log('   API 往返 =', JSON.stringify(apiLog.filter((r) => r.url.includes('/api/email'))))
check('POST /api/email/accounts 发出且非 4xx/5xx（不是被服务器拒绝）',
  !!acctReq && acctReq.status >= 200 && acctReq.status < 300,
  acctReq ? `status=${acctReq.status}` : '（没有捕获到该请求）')

// ---------- 6. 反馈正确性 ----------
// IMAP 连不上是**预期**的（没有真实 IMAP 服务）。判据不是「能不能连通」——
// 那只会得到一个恒失败的结果 —— 而是**界面有没有诚实**。
//
// BUG-AC 修之前：后端返回 200 且 body 里写着 failed:[…]，前端只读 sync.new，
// 于是显示「已保存并验证」+「IMAP：同步成功」—— 把失败说成成功。
// BUG-AC 修之后：必须显示「已保存…但连接未全部通过」，且**不得**出现「已保存并验证」。
const saysSaved = /已保存/.test(resultText)
const falseSuccess = /已保存并验证|同步成功/.test(resultText)
const admitsFailure = /连接未全部通过|连接失败/.test(resultText)
check('界面承认「已保存」（写确实发生了）', saysSaved, resultText.match(/.{0,40}已保存.{0,40}/)?.[0] || '(未见)')
check('⚠️ 不把连接失败说成成功（BUG-AC 核心判据）', !falseSuccess,
  falseSuccess ? '出现了「已保存并验证 / 同步成功」——把失败说成了成功' : '未出现假成功文案')
check('界面明确指出连接未全部通过', admitsFailure,
  admitsFailure ? '已指出' : `resultText=${resultText.slice(0, 120)}`)

// ---------- 7. 对照组：API 直接建的那条仍在 ----------
check('对照组：API 直建的那条仍在（证明不是 UI 幻觉）', addrs.includes('probe'), addrs.slice(0, 90))

check('无未捕获 JS 异常', errors.length === 0, errors.slice(0, 2).join(' | ') || '0 条')

console.log('\n=== 汇总 ===')
const passed = checks.filter((c) => c.pass).length
console.log(`${passed}/${checks.length} 通过`)
checks.filter((c) => !c.pass).forEach((c) => console.log(`  FAIL: ${c.n}`))
process.exit(passed === checks.length ? 0 : 1)
