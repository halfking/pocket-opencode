// probe-device-idle.mjs —— **只读**判断设备上的 App 此刻是否正在被人使用。
//
// 用途：把 API base 指向隔离后端是**设备上的共享状态**（同一个 App、
// 同一份 localStorage）。动手前必须先确认对方没在跑，否则会把人家的请求
// 一起改道。判据：连续采样 /api/ 请求条数，**在增长就是有人在用**。
//
// ⚠️ 采样窗口短（默认 12s）只覆盖「此刻」。窗口内没增长不等于之后没人用，
//    所以这个探针只用来把「现在明显有人在用」和「现在看不出」分开，
//    后者仍要靠可逆性兜底：改动前记录原值，finally 里原样写回。
import { openCdp } from './lib/adb-cdp.mjs'

const SAMPLES = Number(process.env.POCKET_IDLE_SAMPLES || 6)
const GAP_MS = Number(process.env.POCKET_IDLE_GAP_MS || 2000)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const COUNT = `(function(){
  try { return performance.getEntriesByType('resource')
    .filter(function(e){return /\\/api\\//.test(e.name)}).length; } catch(e){ return -1; }
})()`

let cdp
try {
  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
  const first = await cdp.ev(COUNT)
  console.log(`起始 /api/ 请求条数 = ${first}`)
  console.log(`采样 ${SAMPLES} 次，每次间隔 ${GAP_MS}ms：\n`)
  let prev = first
  let grew = 0
  const series = [first]
  for (let i = 1; i <= SAMPLES; i++) {
    await sleep(GAP_MS)
    const n = await cdp.ev(COUNT)
    const d = n - prev
    if (d > 0) grew++
    series.push(n)
    console.log(`  t${i}  ${n}  (${d >= 0 ? '+' : ''}${d})`)
    prev = n
  }
  const total = series[series.length - 1] - series[0]
  console.log(`\n窗口内共新增 ${total} 条 /api/ 请求，其中 ${grew}/${SAMPLES} 次采样有增长`)
  if (total > 0) {
    console.log('\n判定：**有人在用**。不要动 localStorage。')
    process.exitCode = 1
  } else {
    console.log('\n判定：窗口内无人使用（看不出有人在用）。')
    console.log('      ⚠️ 这只覆盖这个窗口，不保证之后也没人用 —— 改动必须可逆。')
  }
} catch (e) {
  console.error(`探测失败：${e.message}`)
  process.exitCode = 3
} finally {
  if (cdp) await cdp.close()
}
