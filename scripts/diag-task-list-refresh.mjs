// diag-task-list-refresh.mjs —— 定性：任务删除后列表不刷新，是真缺陷还是缓存？
//
// 事实（§4.94 实测）：`verify-task-writepath.mjs` 删掉任务后，
// PG 里 count=0，但轮询 15 秒卡片始终还在列表里。
//
// 两种可能，必须分开：
//   A) 列表**从不**重新拉取（keep-alive 缓存，进页面不 onMounted）——
//      那「删除后不刷新」是真缺陷，但要手动刷新才看得见。
//   B) 列表会刷新，只是慢 —— 那 15 秒不够，仍是缺陷但性质不同。
//
// 做法：删除后依次看
//   ① 重新进列表页（换 hash 再回来）→ 卡片还在吗
//   ② 点列表页的刷新控件 → 卡片消失吗
// 若 ② 消失而 ① 不消失 ⇒ 确认为 A：数据是对的，是**刷新触发**缺失。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'
import { requireDevPass } from './lib/dev-pass.mjs'

const BASE = process.env.POCKET_VERIFY_BASE || 'http://127.0.0.1:18101'
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket_verify'
const STAMP = Date.now().toString().slice(-6)
const TITLE = `LISTREFRESH-${STAMP}`
const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const psql = (s) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', s], { encoding: 'utf8' }).trim()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: requireDevPass() }), signal: AbortSignal.timeout(15000),
})
const { token } = await login.json()
if (!token) { console.error('登录不通'); process.exit(2) }

let cdp, createdId = null, cleaned = false
async function cleanup() {
  if (!createdId || cleaned) return
  cleaned = true
  const r = await fetch(`${BASE}/api/tasks/${createdId}`, { method: 'DELETE', headers: { authorization: `Bearer ${token}` } })
  console.log(`[cleanup] DELETE ${createdId} -> ${r.status}；PG 残留 = ${psql(`select count(*) from ${SCHEMA}.tasks where id='${createdId}'`)}`)
}
for (const sig of ['unhandledRejection', 'uncaughtException']) {
  process.on(sig, async (e) => { console.error(sig, e); await cleanup(); if (cdp) await cdp.close(); process.exit(1) })
}

try {
  const mk = await fetch(`${BASE}/api/tasks`, {
    method: 'POST', headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
    body: JSON.stringify({ title: TITLE, status: 'active' }), signal: AbortSignal.timeout(15000),
  })
  const j = await mk.json()
  createdId = j && (j.id || (j.task && j.task.id))
  console.log(`播种 ${TITLE} -> ${mk.status} id=${createdId}`)
  if (!createdId) { console.error('播种失败'); process.exit(1) }

  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
  const gotoHash = async (h, ms = 15000) => {
    await cdp.ev(`location.hash=${JSON.stringify(h)}`)
    const d = Date.now() + ms
    while (Date.now() < d && (await cdp.ev('location.hash')) !== h) await sleep(300)
    await sleep(2200)
  }
  const foundExpr = `(function(){var cs=document.querySelectorAll('.task-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0)return true}return false})()`

  await gotoHash('#/ai')
  await cdp.ev(`(function(){var b=document.querySelector('button[aria-label="刷新"]');if(b){b.click();return 'clicked'}return 'no-refresh-btn'})()`)
  await sleep(2000)
  console.log(`\n① 删除前刷新一次，列表里能看到 = ${await cdp.ev(foundExpr)}`)

  const del = await fetch(`${BASE}/api/tasks/${createdId}`, { method: 'DELETE', headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(15000) })
  console.log(`② 服务端 DELETE -> ${del.status}；PG = ${psql(`select count(*) from ${SCHEMA}.tasks where id='${createdId}'`)}`)

  console.log(`③ 删除后（不刷新）仍能看到 = ${await cdp.ev(foundExpr)}`)

  // 离开再回来：验证是不是 keep-alive 缓存导致不重新拉取
  await gotoHash('#/more')
  await gotoHash('#/ai')
  await sleep(2000)
  const afterReenter = await cdp.ev(foundExpr)
  console.log(`④ 离开再回来（没点刷新）仍能看到 = ${afterReenter}`)

  // 点刷新
  const rf = await cdp.ev(`(function(){var b=document.querySelector('button[aria-label="刷新"]');if(b){b.click();return 'clicked'}return 'no-refresh-btn'})()`)
  await sleep(2500)
  const afterRefresh = await cdp.ev(foundExpr)
  console.log(`⑤ 点刷新（${rf}）后仍能看到 = ${afterRefresh}`)

  console.log('')
  if (afterReenter && !afterRefresh) {
    console.log('判定：**A —— 数据是对的，缺的是「触发刷新」**。');
    console.log('      离开再回来不会重新拉取；点刷新才更新。⇒ 删除后列表不反映服务端状态，是真缺陷（UI 层）。');
  } else if (!afterReenter) {
    console.log('判定：重新进页面就更新 ⇒ 15 秒那条是**等待不足**，不是缺陷。');
  } else {
    console.log('判定：点刷新也没消失 ⇒ 需要另查（可能删的不是同一个 id，或列表读了别的作用域）。');
  }
} catch (e) {
  console.error(`失败：${e.message}`)
  process.exitCode = 3
} finally {
  await cleanup()
  if (cdp) await cdp.close()
}
