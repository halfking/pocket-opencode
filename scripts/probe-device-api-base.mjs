// probe-device-api-base.mjs —— **只读**查设备上 App 实际打向哪个后端。
//
// 为什么需要：页面 origin 是 `https://localhost`（生产壳），而 API base 若走
// `http://…` 就是混合内容。本脚本先把「现在到底打向哪」读出来，
// 再判断把隔离后端（192.168.31.20:18101）指过去**在技术��行不行**。
//
// 严格只读：只 openCdp + Runtime.evaluate 读 localStorage / 试 fetch，不导航、
// 不点击、**不写 localStorage**、不碰 adb reverse。
import { openCdp } from './lib/adb-cdp.mjs'

const HOST_LAN = process.env.POCKET_HOST_LAN || '192.168.31.20'
const PORT = process.env.POCKET_VERIFY_PORT || '18101'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let cdp
try {
  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
  console.log(`CDP：pid=${cdp.pid} port=${cdp.port}`)

  const origin = await cdp.ev('location.origin')
  const state = await cdp.ev(`(function(){
    var out = {};
    try { out.apiBase = localStorage.getItem('pocket_api_base'); } catch(e) { out.apiBase = 'ERR:'+e.message }
    try { out.token = (localStorage.getItem('pocket_token')||'').length; } catch(e) { out.token = 'ERR' }
    try { out.keys = Object.keys(localStorage).filter(function(k){return /api|base|server/i.test(k)}); } catch(e) { out.keys = [] }
    return out;
  })()`)
  console.log(`\norigin            = ${origin}`)
  console.log(`pocket_api_base   = ${JSON.stringify(state.apiBase)}   (null = 未覆盖，走构建默认值/生产入口)`)
  console.log(`localStorage 里相关的键 = ${JSON.stringify(state.keys)}`)
  console.log(`pocket_token 长度 = ${state.token}`)

  // 构建期烘进去的默认值：读不到 import.meta.env，但可以看实际请求打向哪。
  // 用 Performance 资源表反推 —— 只读。
  const recent = await cdp.ev(`(function(){
    try { return performance.getEntriesByType('resource')
      .map(function(e){return e.name})
      .filter(function(u){return /\\/api\\//.test(u)})
      .slice(-8);
    } catch(e){ return ['ERR:'+e.message]; }
  })()`)
  console.log(`\n最近的 /api/ 请求（从 Performance 资源表反推实际 base）：`)
  for (const u of recent) console.log('   ' + u)

  // 混合内容可行性：https 页面能不能 fetch 到 http 的隔离后端。
  // 这是**只发 HEAD/GET 探测**，不写任何数据。
  const probe = await cdp.ev(`(async function(){
    var url = 'http://${HOST_LAN}:${PORT}/healthz';
    try {
      var ctl = new AbortController(); setTimeout(function(){ctl.abort()}, 8000);
      var r = await fetch(url, {signal: ctl.signal, cache:'no-store'});
      var t = (await r.text()).trim();
      return {ok:true, status:r.status, body:t.slice(0,40)};
    } catch(e) { return {ok:false, error:String(e && e.message || e)}; }
  })()`)
  console.log(`\n从页面 fetch http://${HOST_LAN}:${PORT}/healthz →`)
  console.log('   ' + JSON.stringify(probe))
  if (probe.ok) {
    console.log('   ⇒ 页面能直连隔离后端（混合内容**没有**被拦）')
  } else {
    console.log('   ⇒ 页面**不能**直连。典型原因：https 页面 fetch http:// 被当混合内容拦掉。')
    console.log('     那就只能继续用 adb reverse（设备 localhost:<port> → 主机），而那是共享状态。')
  }
} catch (e) {
  console.error(`探测失败：${e.message}`)
  process.exitCode = 3
} finally {
  if (cdp) await cdp.close()
}
