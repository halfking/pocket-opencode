#!/usr/bin/env node
/**
 * verify-marketplace-install.mjs — 真机验证「技能市场」的 UI 写路径。
 *
 * ## 补的是哪个缺口
 *
 * 六个模块（密码箱 / 市场 / 邮箱 / 网关 / 实例 / 费用配额）的 UI 写路径此前
 * **一条都没在真机上点过**。市场是其中**后端端点已全部验证可达**的一个
 * （`probe-marketplace-auth.mjs`：11 个端点 0 个 404/405），所以最适合先打通。
 *
 * ## 为什么要先播种
 *
 * `SkillMarketView` 的写操作是「安装」→ `POST /api/marketplace/install`，
 * 而 install 需要一个**已发布的 release**。dev 库里 packages/releases 都是空的，
 * UI 上根本没有可安装对象。所以流程是：
 *   1. **API 播种**（顺带再验 3 个写端点：submit / review / publish）
 *   2. **UI 点安装**（真正的验收对象）
 *   3. **查 PG** 确认安装记录落库
 *
 * 判据必须能区分通/不通：
 *   - 「按钮点了」不算证据
 *   - 「DOM 里有安装后的提示」也不算 —— 可能只是乐观 UI
 *   - **唯一可信判据是直接查 `marketplace_installations` 表**
 *
 * ## 对照组
 *
 * 装完之后再点一次「安装」应当**不新增行**（`uniq_marketplace_inst_ws_rel` 唯一索引
 * 会在后端挡下）。如果第二次反而多了一行，说明重复安装没被约束住。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/verify-marketplace-install.mjs
 */
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
import http from 'node:http'
// 设备上装的是**生产 https 包**（实测 origin=https://localhost），
// 而这一关原本写死开发包 http://localhost ⇒ 在当前设备上会在走到任何
// 真正要验的判据之前就 exit 5。生产 https 回归用 POCKET_EXPECT_ORIGIN 放宽。
const EXPECT_ORIGIN = process.env.POCKET_EXPECT_ORIGIN || 'http://localhost';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const CDP_PORT = process.env.POCKET_CDP_PORT || '9250'
const MASTER = process.env.POCKET_MASTER || ''
const API_HOST = '127.0.0.1'
// API 端口：跟随后端。写死 8088 时，脚本会对着一个**可能根本没人监听**的端口
// 打 API，而判据照样往下跑 —— 拿到一堆看似「接口不通」的假失败。
// 与 §4.89（API base 改 env）同一类，这里是端口。
const API_PORT = Number(process.env.POCKET_API_PORT || 8088);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

// ---------- psql 解析（logs/ 是 gitignored，worktree 里没有） ----------
function resolvePsql() {
  for (const c of [process.env.POCKET_PSQL, 'logs/pg/dist2/pgsql/bin/psql.exe', 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'].filter(Boolean)) {
// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);
    try { execFileSync(c, ['--version'], { stdio: 'ignore' }); return c } catch { /* next */ }
  }
  console.error('找不到 psql.exe，请设置 POCKET_PSQL')
  process.exit(4)
}
const PSQL = resolvePsql()
function sql(q) {
  const out = execFileSync(PSQL, ['-h', API_HOST, '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', q], { encoding: 'utf8' }).trim()
  const m = out.match(/-?\d+/)
  return { raw: out, n: m ? Number(m[0]) : NaN }
}

// ---------- 带鉴权 API ----------
function api(path, token, method = 'GET', body = '') {
  return new Promise((res) => {
    const h = {}
    if (token) h.Authorization = 'Bearer ' + token
    if (body) { h['Content-Type'] = 'application/json'; h['Content-Length'] = Buffer.byteLength(body) }
    const r = http.request({ host: API_HOST, port: API_PORT, path, method, headers: h }, (resp) => {
      let b = ''; resp.on('data', (c) => (b += c)); resp.on('end', () => res({ status: resp.statusCode, body: b }))
    })
    r.on('error', (e) => res({ status: 'ERR', body: e.message }))
    if (body) r.write(body); r.end()
  })
}

const checks = []
const check = (name, pass, detail) => {
  checks.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}

// ---------- 播种 ----------
const devPass = requireDevPass()
const login = await api('/api/auth/login', '', 'POST', JSON.stringify({ username: 'admin', password: devPass }))
let token = ''
try { token = JSON.parse(login.body).token || '' } catch { /* 下面报告 */ }
if (!token) { console.log('LOGIN_FAILED ' + login.status); process.exit(1) }

const STAMP = Date.now()
const PKG_ID = `e2e-skill-${STAMP}`
// ⚠️ **后端忽略客户端传的 package_id**，自己从 workspace + name + version 推导
// （实测 submit 返回的 package_id 是 `ws_user-admin/E2E 技能`，不是我传的
// `e2e-skill-<时间戳>`）。所以唯一性由**名字**决定 —— 名字必须每次不同，
// 否则第二次 submit 撞 `marketplace_versions_pkey` 唯一约束。
// （顺带发现：那个冲突被返回成 **500** 而不是 409/400，见 handoff §4.26）
const PKG_NAME = `E2E 技能 ${STAMP}`
const submit = await api('/api/marketplace/submit', token, 'POST', JSON.stringify({
  package_id: PKG_ID, name: PKG_NAME, kind: 'skill', version: '1.0.0', digest: `sha256:${STAMP}`,
}))
check('API 播种 submit', submit.status >= 200 && submit.status < 300, `status=${submit.status} ${submit.body.slice(0, 110)}`)
let versionId = ''
try { versionId = JSON.parse(submit.body).version_id || '' } catch { /* 下面报告 */ }
if (!versionId) { console.log('拿不到 version_id，终止'); process.exit(1) }

const review = await api('/api/marketplace/review', token, 'POST', JSON.stringify({ version_id: versionId, approved: true }))
check('API 播种 review', review.status === 200, `status=${review.status} ${review.body.slice(0, 110)}`)
const publish = await api('/api/marketplace/publish', token, 'POST', JSON.stringify({ version_id: versionId, channel: 'stable' }))
// ⚠️ 第一版把判据写死成 `status === 200`，实测 publish 返回 **201**（创建语义），
// 于是报出一条假 FAIL。判据要按语义放宽到 2xx，而不是照抄一个数字。
check('API 播种 publish', publish.status >= 200 && publish.status < 300, `status=${publish.status} ${publish.body.slice(0, 110)}`)

// ---------- 先核对 workspace：两边不在同一个数据孤岛里，后面才有意义 ----------
const apiWs = (JSON.parse(Buffer.from(token.split('.')[1], 'base64').toString()).workspace_id) || '(无)'

const before = sql(`select count(*) from ${SCHEMA}.marketplace_installations;`).n
console.log(`播种前 installations 行数 = ${before}（package=${PKG_ID}）`)

// ---------- 连 CDP ----------
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${CDP_PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${CDP_PORT}/`))
let id = 0
const pending = new Map()
const errors = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.exceptionThrown') errors.push(m.params?.exceptionDetails?.exception?.description || '')
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

let origin = null
const readyDl = Date.now() + 20000
while (Date.now() < readyDl) {
  origin = await ev('location.origin')
  if (origin && origin !== 'null') break
  await sleep(500)
}
console.log('origin =', origin)
if (origin !== EXPECT_ORIGIN) { console.log('非 http 调试包，断言无意义，终止'); process.exit(5) }

// ---------- 会话恢复 ----------
// ⚠️ 必须先清掉旧 token 强制重新登录。
// 设备上那个 token 签发于 `ws_<user>` 约定生效**之前**，workspace_id 还是 `default`；
// 而 API 侧现在稳定给 `ws_user-admin`。市场按 workspace 隔离，于是：
//   **后端明明返回了刚播种的包，UI 却显示「暂无技能包」。**
// 第一版就踩了这个坑，还差点把它当成前端缺陷报出去。
// 判据见下面 workspace 核对 —— 它的存在就是为了让这类错误当场暴露。
if (MASTER) {
  await ev(`(function(){try{localStorage.removeItem('pocket_token');localStorage.removeItem('pocket_workspace_id')}catch(e){};return 1})()`)
  await send('Page.reload', { ignoreCache: false })
  await sleep(6000)

  // ⚠️ 必须**精确匹配**。这个页面上有「密码登录」「验证码登录」「登录」三个按钮，
  // 文本都**包含**「登录」。用 indexOf >= 0 会点到第一个「密码登录」那个 tab 上 ——
  // 点击执行了、没报错，但登录从未发生。症状是「hash 停在 #/login、没有 token」，
  // 看起来像登录失败。独立诊断脚本用 === 精确匹配，一次就通。
  const clickExact = (label) => ev(`(function(){
    var b = Array.prototype.slice.call(document.querySelectorAll('button'))
      .find(function(x){ return (x.textContent||'').trim() === ${JSON.stringify(label)}; });
    if (b) { b.click(); return 'clicked:' + ${JSON.stringify(label)}; }
    return 'NOT_FOUND:' + Array.prototype.map.call(document.querySelectorAll('button'),function(x){return (x.textContent||'').trim();}).join('|');
  })()`)
  const clickByText = clickExact

  // 轮询：登录页可能先要求解锁主密码，也可能直接是账号密码。
  // 固定 sleep 短了会错过表单、长了白等 —— 判据是「状态达到期望」。
  let unlocked = false
  const sDl = Date.now() + 45000
  while (Date.now() < sDl) {
    const hasUnlock = await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)
    const hasLogin = await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)
    if (hasUnlock) {
      await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
      await sleep(1800)
      await clickByText('解锁')
      await sleep(4500)
      unlocked = true
    }
    if (hasLogin) {
      const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`
      await ev(fillBy('input[placeholder*="用户名"]', 'admin'))
      await ev(fillBy('input[type="password"]', devPass))
      // 「登录」按钮的 disabled 是**计算属性**，填完字段要等 Vue 重渲染才变可用。
      // 固定 sleep（本脚本原来写 1200ms）偏短时按钮仍是 disabled，click() 静默无效 ——
      // 表现是「会话没恢复、hash 停在 #/login、localStorage 没有 token」，
      // 看起来像登录失败，其实是**根本没点上**。判据必须是「等按钮 enabled」。
      let btnReady = false
      const bDl = Date.now() + 10000
      while (Date.now() < bDl) {
        await sleep(300)
        const dis = await ev(`(function(){
          var b = Array.prototype.slice.call(document.querySelectorAll('button'))
            .find(function(x){ return (x.textContent||'').trim() === '登录' });
          return b ? b.disabled : null;
        })()`)
        if (dis === false) { btnReady = true; break }
      }
      if (!btnReady) { console.log('  登录按钮始终 disabled —— 检查字段是否真的填进去了'); break }
      await clickByText('登录')
      // 同样是「等状态」而不是「等够时间」：登录后要跳转 + 写 localStorage，
      // 固定 sleep 偏短会在 token 落盘前就往下走，后面全部判据读的是「未登录」状态。
      let loggedIn = false
      const lDl = Date.now() + 25000
      while (Date.now() < lDl) {
        if ((await ev(`!!localStorage.getItem('pocket_token')`)) === true) { loggedIn = true; break }
        await sleep(500)
      }
      console.log('  登录后是否拿到 token =', loggedIn)
      break
    }
    if (unlocked) await sleep(800)
    else await sleep(600)
  }
  console.log('会话恢复后 hash =', await ev('location.hash'))
}

// ---------- workspace 核对：两个数据孤岛会让后面所有断言失去意义 ----------
const appWs = await ev(`(function(){
  try {
    var t = localStorage.getItem('pocket_token') || '';
    var m = t.match(/eyJ[A-Za-z0-9_\\-]+\\.[A-Za-z0-9_\\-]+/);
    if (!m) return '(no jwt)';
    var p = JSON.parse(atob(m[0].split('.')[1].replace(/-/g,'+').replace(/_/g,'/')));
    return p.workspace_id || '(none)';
  } catch(e){ return 'ERR ' + String(e); }
})()`)
check('App 与 API 在同一 workspace（否则是两个数据孤岛）', appWs === apiWs, `App=${appWs} API=${apiWs}`)
if (appWs !== apiWs) {
  console.log('\nworkspace 不一致，后续 UI 断言无意义（UI 看不到 API 刚建的数据）。中止。')
  process.exit(6)
}

// ---------- 导航到技能市场 ----------
await ev(`location.hash = '#/marketplace/skills'`)
const dl = Date.now() + 12000
while (Date.now() < dl && (await ev('location.hash')) !== '#/marketplace/skills') await sleep(300)
await sleep(3000)

const view = JSON.parse((await ev(`(function(){
  var pane = null;
  var panes = document.querySelectorAll('.inner-pane, .outer-pane');
  for (var i=0;i<panes.length;i++){ if (panes[i].offsetParent !== null) { pane = panes[i]; break; } }
  var root = pane || document.body;
  var cards = root.querySelectorAll('article');
  var btns = [];
  for (var i=0;i<cards.length;i++) {
    var b = Array.prototype.slice.call(cards[i].querySelectorAll('button'));
    for (var j=0;j<b.length;j++) btns.push((b[j].textContent||'').trim());
  }
  return JSON.stringify({
    articleCount: cards.length,
    bodyText: (document.body.innerText||'').replace(/\\s+/g,' ').trim().slice(0,220),
    buttonLabels: btns.slice(0, 12),
    hasOurPackage: (document.body.innerText||'').indexOf(${JSON.stringify(PKG_NAME)}) >= 0
  });
})()`)) || '{}')
check('技能市场渲染出包卡片', view.articleCount >= 1, `articles=${view.articleCount}`)
check('刚播种的包出现在列表里', view.hasOurPackage === true, `text=${view.bodyText}`)

const installBtns = (view.buttonLabels || []).filter((l) => l === '安装' || l.includes('安装'))
check('存在「安装」按钮', installBtns.length >= 1, `按钮=${JSON.stringify(view.buttonLabels)}`)

// ---------- 点「安装」→ 确认弹窗 ----------
await ev(`(function(){
  var pane=null; var panes=document.querySelectorAll('.inner-pane, .outer-pane');
  for(var i=0;i<panes.length;i++){ if(panes[i].offsetParent!==null){pane=panes[i];break;} }
  var root=pane||document.body;
  var cards=root.querySelectorAll('article');
  for(var i=0;i<cards.length;i++){
    var b=Array.prototype.slice.call(cards[i].querySelectorAll('button'));
    for(var j=0;j<b.length;j++){ if((b[j].textContent||'').trim()==='安装'){ b[j].click(); return 'clicked'; } }
  }
  return 'NOT_FOUND';
})()`)
await sleep(1500)
const dlg = await ev(`(function(){
  var d=document.querySelector('[role=dialog]');
  return d ? JSON.stringify({ open:true, text:(d.innerText||'').replace(/\\s+/g,' ').slice(0,120),
    buttons: Array.prototype.map.call(d.querySelectorAll('button'), function(b){return (b.textContent||'').trim();}) })
           : JSON.stringify({ open:false });
})()`)
const d = JSON.parse(dlg || '{}')
check('安装确认弹窗出现', d.open === true, `text=${d.text} buttons=${JSON.stringify(d.buttons)}`)

// ---------- 点「确认安装」 ----------
await ev(`(function(){
  var d=document.querySelector('[role=dialog]');
  if(!d) return 'NO_DIALOG';
  var b=Array.prototype.slice.call(d.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='确认安装'});
  if(b){b.click();return 'clicked';}
  return 'NO_CONFIRM_BTN';
})()`)
await sleep(3500)

const after = sql(`select count(*) from ${SCHEMA}.marketplace_installations;`).n
check('UI 点击后 PG 落库（不信 DOM，不信接口返回）', after === before + 1, `安装前=${before} 安装后=${after}`)

// 按 **name** 关联而不是 package_id —— 后端会自行推导 package_id（见上面的说明），
// 用我传的 PKG_ID 去 join 永远匹配不到，会得出假的 0。
const ours = sql(`select count(*) from ${SCHEMA}.marketplace_installations i join ${SCHEMA}.marketplace_releases r on r.release_id=i.release_id join ${SCHEMA}.marketplace_versions v on v.version_id=r.version_id where v.package_id like '%${STAMP}%';`).n
check('落库的是刚播种的那个包（关联核对）', ours === 1, `命中=${ours}`)

// ---------- 对照组：再点一次，不应新增 ----------
await ev(`(function(){
  var pane=null; var panes=document.querySelectorAll('.inner-pane, .outer-pane');
  for(var i=0;i<panes.length;i++){ if(panes[i].offsetParent!==null){pane=panes[i];break;} }
  var root=pane||document.body;
  var cards=root.querySelectorAll('article');
  for(var i=0;i<cards.length;i++){
    var b=Array.prototype.slice.call(cards[i].querySelectorAll('button'));
    for(var j=0;j<b.length;j++){ if((b[j].textContent||'').trim()==='安装'){ b[j].click(); return 'clicked'; } }
  }
  return 'NOT_FOUND';
})()`)
await sleep(1200)
await ev(`(function(){
  var d=document.querySelector('[role=dialog]');
  if(!d) return 'NO_DIALOG';
  var b=Array.prototype.slice.call(d.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='确认安装'});
  if(b){b.click();return 'clicked';}
  return 'NO_CONFIRM_BTN';
})()`)
await sleep(3000)
const after2 = sql(`select count(*) from ${SCHEMA}.marketplace_installations;`).n
check('对照组：重复安装不新增行（唯一索引挡住了）', after2 === after, `再点后=${after2}`)

const errs = errors.filter((e) => e && !/favicon/i.test(e))
check('无未捕获 JS 异常', errs.length === 0, errs.slice(0, 2).join(' | ') || '无')

console.log('\n=== 汇总 ===')
const passed = checks.filter((c) => c.pass).length
console.log(`${passed}/${checks.length} 通过`)
checks.filter((c) => !c.pass).forEach((c) => console.log(`  FAIL: ${c.name} — ${c.detail || ''}`))
process.exit(passed === checks.length ? 0 : 1)
