// probe-device-request-cadence.mjs —— **只读**区分「人在操作」与「App 后台轮询」。
//
// 上一轮 60 秒窗口里 /api/ 请求 +2，于是「有人在用」判红。但那两个可能是
// App 自带的轮询器（scheduled-tasks?since=…、redclaw/health 都很像定时任务），
// 而**轮询增长不等于有人在驱设备** —— 不分清就会为一个空判断放弃整条路线，
// 或者反过来为一个假判断去改共享状态。
//
// 做法：取每条 /api/ 请求的 URL 与 startTime，按时间排序打印。
// 人操作 ⇒ 出现**页面级**端点（/api/finance、/api/tasks、/api/notes…）且成簇；
// 轮询   ⇒ 只有 health / scheduled-tasks 这类，且间隔均匀。
import { openCdp } from './lib/adb-cdp.mjs'

const WAIT_MS = Number(process.env.POCKET_CADENCE_WAIT_MS || 45000)

let cdp
try {
  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
  const snap = `(function(){
    try {
      return performance.getEntriesByType('resource')
        .filter(function(e){return /\\/api\\//.test(e.name)})
        .map(function(e){ return { url: e.name.replace(/^https?:\\/\\/[^/]+/, ''), t: Math.round(e.startTime) }; });
    } catch(e){ return []; }
  })()`

  const before = await cdp.ev(snap)
  console.log(`起点：${before.length} 条`)
  console.log(`等待 ${WAIT_MS}ms …\n`)
  await new Promise((r) => setTimeout(r, WAIT_MS))
  const after = await cdp.ev(snap)

  const added = after.slice(before.length)
  console.log(`窗口内新增 ${added.length} 条：\n`)
  if (!added.length) { console.log('（无新增 —— 窗口内完全静止）') }
  for (const r of added) console.log(`   t+${String(r.t).padStart(7)}ms  ${r.url}`)

  // 轮询特征：路径落在已知的心跳/定时任务集合里
  const POLLER = /\/(health|scheduled-tasks|redclaw\/health|notifications|poll)(\?|$)/
  const pollerish = added.filter((r) => POLLER.test(r.url))
  const humanish = added.filter((r) => !POLLER.test(r.url))

  console.log(`\n其中疑似轮询器：${pollerish.length} 条；疑似人工/页面级：${humanish.length} 条`)
  console.log('')
  if (humanish.length) {
    console.log('判定：**有页面级请求** ⇒ 有人在用设备，不该动 localStorage。')
    humanish.forEach((r) => console.log('   ' + r.url))
    process.exitCode = 1
  } else if (pollerish.length) {
    console.log('判定：新增全是心跳/定时任务端点 ⇒ 更像 App 自带轮询，**不能据此断定有人在驱设备**。')
    console.log('      仍不能当作「无人使用」的证明 —— 窗口有限。')
  } else {
    console.log('判定：窗口内完全静止。')
  }
  console.log('\n--- 全部最近的 /api/ 端点（看这个 App 平时会打什么）---')
  const uniq = [...new Set(after.map((r) => r.url.split('?')[0]))]
  uniq.forEach((u) => console.log('   ' + u))
} catch (e) {
  console.error(`探测失败：${e.message}`)
  process.exitCode = 3
} finally {
  if (cdp) await cdp.close()
}
