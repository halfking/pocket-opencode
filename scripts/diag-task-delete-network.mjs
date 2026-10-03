// diag-task-delete-network.mjs —— **定案 BUG-V20**：删除请求到底发没发出去。
//
// §4.116 留下的两条互斥可能：
//   (a) 产品缺陷：删除成功后列表不刷新
//   (b) 探针缺陷：`verify-task-writepath` 用**全页面文本匹配**找「删除/确认删除」按钮，
//       而确认弹层是 `Dialog`（不是 BottomSheet），footer 里 cancel 在前、confirm 在后；
//       文本匹配很可能点空了 —— 后面看到的「PG 无该行」也许是上一条删除的结果。
//
// 唯一能一刀切开的方法：**开 Network 域，看 `DELETE /api/tasks/{id}` 有没有真的发出去。**
//   没发出去  ⇒ (b)，探针的锅
//   发出且 200 ⇒ (a)，产品缺陷，继续查 consumeListDirty
//
// 顺带把确认按钮改成**按选择器点**（`.dialog .dialog-footer` 里的 confirm 按钮），
// 不再用全页面文本匹配 —— 这一条本身就是要验的东西。
import { openCdp } from './lib/adb-cdp.mjs'
import { requireDevPass } from './lib/dev-pass.mjs'
import { execFileSync } from 'node:child_process'

const BASE = process.env.POCKET_VERIFY_BASE || 'http://127.0.0.1:18101'
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket_verify'
const TITLE = `DELNET-${Date.now().toString().slice(-6)}`
const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const psql = (s) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', s], { encoding: 'utf8' }).trim()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: requireDevPass() }), signal: AbortSignal.timeout(15000),
})
const { token } = await login.json()
if (!token) { console.error('登录不通'); process.exit(2) }

let createdId = null, cleaned = false
async function cleanup() {
  if (!createdId || cleaned) return
  cleaned = true
  const r = await fetch(`${BASE}/api/tasks/${createdId}`, { method: 'DELETE', headers: { authorization: `Bearer ${token}` } })
  console.log(`[cleanup] DELETE -> ${r.status}；PG 残留 = ${psql(`select count(*) from ${SCHEMA}.tasks where id='${createdId}'`)}`)
}
for (const sig of ['unhandledRejection', 'uncaughtException']) {
  process.on(sig, async (e) => { console.error(sig, e); await cleanup(); if (cdp) await cdp.close(); process.exit(1) })
}

let cdp
try {
  const mk = await fetch(`${BASE}/api/tasks`, {
    method: 'POST', headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
    body: JSON.stringify({ title: TITLE, status: 'active' }), signal: AbortSignal.timeout(15000),
  })
  const j = await mk.json()
  createdId = j && (j.id || (j.task && j.task.id))
  console.log(`播种 ${TITLE} -> ${mk.status} id=${createdId}`)
  if (!createdId) process.exit(1)

  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
  await cdp.send('Network.enable')

  // 抓与该任务 id 相关的所有请求
  const seen = []
  cdp.on('Network.requestWillBeSent', (p) => {
    if (String(p.request.url || '').includes(createdId)) seen.push({ kind: 'req', method: p.request.method, url: p.request.url.replace(/https?:\/\/[^/]+/, '') })
  })
  cdp.on('Network.responseReceived', (p) => {
    if (String(p.response.url || '').includes(createdId)) seen.push({ kind: 'res', status: p.response.status, url: p.response.url.replace(/https?:\/\/[^/]+/, '') })
  })

  const gotoHash = async (h, ms = 15000) => {
    await cdp.ev(`location.hash=${JSON.stringify(h)}`)
    const d = Date.now() + ms
    while (Date.now() < d && (await cdp.ev('location.hash')) !== h) await sleep(300)
    await sleep(2500)
  }
  await gotoHash('#/ai')
  await gotoHash(`#/tasks/${encodeURIComponent(createdId)}`)

  const delBtn = await cdp.ev(`(function(){var b=document.querySelector('.action-btn.delete');if(!b)return 'NO_BTN';b.click();return 'clicked'})()`)
  console.log(`点 .action-btn.delete = ${delBtn}`)
  await sleep(1200)

  const dlg = await cdp.ev(`(function(){
    var d=document.querySelector('.dialog');
    if(!d) return {dialog:false};
    var f=d.querySelector('.dialog-footer');
    var btns=f?Array.prototype.slice.call(f.querySelectorAll('button')):[];
    return {dialog:true, title:(d.querySelector('.dialog-title')||{}).textContent||'',
            footerButtons: btns.map(function(b){return (b.textContent||'').replace(/\\s+/g,' ').trim()})};
  })()`)
  console.log(`确认弹层 = ${JSON.stringify(dlg)}`)

  // 按**选择器**点最后一个（confirm）按钮，而不是全页面文本匹配
  const confirmClick = await cdp.ev(`(function(){
    var f=document.querySelector('.dialog .dialog-footer');
    if(!f) return 'NO_FOOTER';
    var bs=f.querySelectorAll('button');
    if(!bs.length) return 'NO_BUTTONS';
    bs[bs.length-1].click(); return 'clicked:'+(bs[bs.length-1].textContent||'').trim();
  })()`)
  console.log(`点确认按钮 = ${confirmClick}`)
  await sleep(3500)

  const pgNow = psql(`select count(*) from ${SCHEMA}.tasks where id='${createdId}'`)
  console.log(`\nPG = ${pgNow}`)
  console.log(`与该 id 相关的网络事件：`)
  if (!seen.length) console.log('   （一条都没有）')
  for (const s of seen) console.log(`   ${s.kind}  ${s.status || s.method}  ${s.url}`)

  const delSent = seen.some((s) => s.kind === 'req' && s.method === 'DELETE')
  console.log('')
  if (!delSent) {
    console.log('判定：**DELETE 请求根本没发出去** ⇒ (b) 探针缺陷，不是产品缺陷。')
    console.log('      verify-task-writepath 的全页面文本匹配点空了（弹层是 Dialog，footer 里 cancel 在前）。')
  } else {
    const res = seen.find((s) => s.kind === 'res')
    console.log(`判定：DELETE 已发出（响应 ${res ? res.status : '无'}）⇒ (a) 产品缺陷。`)
    console.log('      下一步查 consumeListDirty(\'tasks\') 是否被调用。')
  }
  if (pgNow === '0' && !delSent) {
    console.log('⚠️ 注意：PG 已归零但没有 DELETE 事件 —— 说明是**别的**删除把它删掉了，')
    console.log('   这正是 §4.116 怀疑的「看到的是上一条删除的结果」。')
  }
} catch (e) {
  console.error(`失败：${e.message}`)
  process.exitCode = 3
} finally {
  await cleanup()
  if (cdp) await cdp.close()
}
