// diag-task-refresh-observability.mjs —— 定位 BUG-V20 到具体环节。
//
// 静态面已排除的：
//   - `useListScene('tasks', handleRefresh)` **只注册一处** ⇒ 不是多处抢脏标记
//   - markListDirty 在 push 之前调用（源码注释专门说明顺序）
//   - loadTasks 是整体替换，无 mergeById 式合并
//   - 接口返回里已无被删那条
//
// 只剩一个能一刀切开的观测：**删除后返回列表，`GET /api/tasks` 有没有发出去？**
//   没发 ⇒ onActivated / consumeListDirty 这段没生效
//   发了 ⇒ 接口已无该条（已证），卡片还在就是渲染 / 计算属性层
//
// 顺带把页面上「脏标记」的取值也读出来：list-scene-store 是模块级单例，
// 可以在页内 import 不到，但可以用 Performance/时间线旁证 —— 所以这里只做网络观测，
// 不去猜内部状态。
import { openCdp } from './lib/adb-cdp.mjs'
import { requireDevPass } from './lib/dev-pass.mjs'
import { execFileSync } from 'node:child_process'

const BASE = process.env.POCKET_VERIFY_BASE || 'http://127.0.0.1:18101'
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket_verify'
const TITLE = `OBS-${Date.now().toString().slice(-6)}`
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

  // 列表端点的请求（**不带 id**）：loadTasks() 打的 GET /api/tasks
  const listGets = []
  cdp.on('Network.requestWillBeSent', (p) => {
    const u = String(p.request.url || '').replace(/https?:\/\/[^/]+/, '')
    if (/^\/api\/tasks(\?|$)/.test(u) && p.request.method === 'GET') listGets.push({ t: Date.now(), u })
  })

  const gotoHash = async (h, ms = 15000) => {
    await cdp.ev(`location.hash=${JSON.stringify(h)}`)
    const d = Date.now() + ms
    while (Date.now() < d && (await cdp.ev('location.hash')) !== h) await sleep(300)
    await sleep(2500)
  }

  await gotoHash('#/ai')
  await gotoHash(`#/tasks/${encodeURIComponent(createdId)}`)
  await cdp.ev(`(function(){var b=document.querySelector('.action-btn.delete');if(b)b.click();return 1})()`)
  await sleep(1200)
  const hit = await cdp.ev(`(function(){
    var f=document.querySelector('.dialog .dialog-footer');
    if(!f) return 'NO_FOOTER';
    var bs=f.querySelectorAll('button'); if(!bs.length) return 'NO_BUTTONS';
    var t=(bs[bs.length-1].textContent||'').replace(/\\s+/g,' ').trim();
    bs[bs.length-1].click(); return 'clicked:'+t;
  })()`)
  console.log(`点确认 = ${hit}`)

  // 标记点：确认点击的这一刻
  const tMark = Date.now()
  await sleep(6000)

  const after = listGets.filter((g) => g.t > tMark)
  console.log(`\n确认点击之后的 GET /api/tasks 次数 = ${after.length}`)
  for (const g of after) console.log(`   +${g.t - tMark}ms  ${g.u}`)

  const hash = await cdp.ev('location.hash')
  const dom = await cdp.ev(`(function(){
    var cs=document.querySelectorAll('.task-card'); var hitT=null;
    for(var i=0;i<cs.length;i++){ if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0) hitT=(cs[i].textContent||'').replace(/\\s+/g,' ').slice(0,50) }
    return {cards:cs.length, hitText:hitT};
  })()`)
  console.log(`当前 hash = ${hash}`)
  console.log(`DOM = ${JSON.stringify(dom)}`)
  console.log(`PG = ${psql(`select count(*) from ${SCHEMA}.tasks where id='${createdId}'`)}`)

  console.log('')
  if (after.length === 0) {
    console.log('判定：**返回列表后没有重新拉 /api/tasks** ⇒ onActivated / consumeListDirty 没生效。')
    console.log('      下一步：查 onActivated 是否触发（KeepAlive 命中时它一定触发），')
    console.log('            以及 handleRefresh 是否在 push 后被同步调用。')
  } else {
    console.log('判定：**列表确实重新拉了**，而接口返回里已无该条 ⇒ 卡片还在是**渲染 / 计算属性**层的问题。')
    console.log('      下一步：查 activeTasks / groupedActiveTasks 是否引用了 tasks 之外的数据源。')
  }
  if (dom.hitText) console.log('      （卡片确实还在：' + dom.hitText + '）')
} catch (e) {
  console.error(`失败：${e.message}`)
  process.exitCode = 3
} finally {
  await cleanup()
  if (cdp) await cdp.close()
}
