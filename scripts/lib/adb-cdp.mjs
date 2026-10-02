// lib/adb-cdp.mjs —— 真机 CDP 通道的唯一入口。
//
// 为什么要有这个（2026-10-02）：
//
// 1) **固定端口是共享可变状态。**
//    `scripts/**/*.mjs` 里有 154 个 `forward tcp:…` 绑定点（2026-10-02 实测），
//    硬绑写死端口的有 140 处（`node scripts/check-fixed-cdp-ports.mjs` 的计数），
//    9402-9600 之间的 40 来个 diag/verify/sweep 脚本各占一个。
//    端口是**同机所有会话共享**的：本机同时有别的会话在驱同一台设备，
//    而且 adb server 重连、WiFi 抖动、App 重启都不会替谁清理 forward。
//    撞上时报 `cannot bind listener ... 10048`，而那句报错指向的是装置，
//    根本看不出「真问题是上次没清干净」。BUG-V9 就是这么撞上的；
//    实测它落在 assertFetchIntact 上时**只会降级成一句「未能判定，不阻断」，
//    然后照样 exit=0** —— 守卫没跑成，绿灯照出。
//    ⇒ 改成 `forward tcp:0`，让 adb 自己挑一个当前空闲的端口并打印出来。
//       碰撞从「概率事件」变成「不可能」。不是「多随机几次」——那只推低概率。
//
// 2) **socket 必须按当前进程 pid 选，不是取最后一个。**
//    设备 `/proc/net/unix` 里会留着**死进程**的
//    `webview_devtools_remote_<pid>`。取「最后一个」会连到不响应的旧 socket，
//    表现是「CDP 探测超时」——看着像 CDP 坏了，其实是自己选错了。
//    这个坑我在一次性探针里连踩两次，固化在这里。
//
// 3) **必须能清理。** 154 个绑定点里只有 27 处做了 `--remove`（同一实测）。
//    close() 是必经路径，请放在 finally 里。
//    ⚠️ 注意 `process.exit()` **不会**跑 finally——要退出就设 process.exitCode，
//       让它自然落到块外，否则你以为清干净了其实没清。
//
// 用法：
//   import { openCdp } from './lib/adb-cdp.mjs'
//   const cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
//   try {
//     const v = await cdp.ev('location.hash')
//     // 要抓网络/控制台就得开事件（`ev` 只能求值）：
//     await cdp.send('Network.enable')
//     cdp.on('Network.requestWillBeSent', (p) => { /* … */ })
//   } finally {
//     await cdp.close()
//   }
//
// 逃生口：POCKET_CDP_PORT 显式给了就照用它（调试时想固定端口用）。
// 没给就一律 tcp:0。

import { execFileSync } from 'node:child_process'

const ADB = process.env.POCKET_ADB
  || 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'

const adb = (args, t = 30000) =>
  execFileSync(ADB, ['-s', SERIAL, ...args], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (args, t = 8000) => { try { return adb(args, t) } catch { return '' } }

/**
 * 打开到 App WebView 的 CDP 通道。
 * @returns {Promise<{ws:WebSocket, port:number, pid:string, socket:string, ev:(expr:string,ms?:number)=>Promise<any>, close:()=>Promise<void>}>}
 */
export async function openCdp(opts = {}) {
  const pkg = opts.pkg || 'com.kaixuan.opencode.pocket'

  const pid = adbSoft(['shell', `pidof ${pkg}`]).trim().split(/\s+/)[0]
  if (!pid) {
    throw new Error(`APP_NOT_RUNNING（${pkg} 没在跑；先跑 node scripts/maestro-run.mjs 任意 flow 拉起它）`)
  }

  // 按当前 pid 精确匹配；匹配不到才退回最后一个，并**大声说出来**——
  // 静默退回会让「选错 socket」变成一个看不见的错误来源。
  const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  let socket = socks.find((s) => s.endsWith(`_${pid}`))
  if (!socket) {
    const dead = socks.filter((s) => !s.endsWith(`_${pid}`))
    if (!socks.length) throw new Error('NO_DEVTOOLS_SOCKET（设备上没有任何 webview_devtools_remote_*）')
    socket = socks[socks.length - 1]
    console.error(`[cdp] ⚠️ 没有 pid=${pid} 的 socket，退回 ${socket}。设备上另有 ${dead.length} 个死进程的陈旧 socket。`)
  }

  // 端口：显式给了就用，没给就让 adb 分配空闲端口。
  const fixed = process.env.POCKET_CDP_PORT
  const out = adb(['forward', 'tcp:0', `localabstract:${socket}`]).trim()
  const port = fixed ? Number(fixed) : Number(out)
  if (!Number.isInteger(port) || port <= 0) {
    throw new Error(`CDP_FORWARD_NO_PORT: adb forward tcp:0 没返回可用端口号，输出=${JSON.stringify(out)}`)
  }

  const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`, { signal: AbortSignal.timeout(10000) })).json()
  const page = pages.find((t) => t.type === 'page')
  if (!page) throw new Error('NO_PAGE_TARGET')

  const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${port}/`))
  let id = 0
  const pending = new Map()
  const listeners = new Map()   // CDP 事件名 -> Set<handler>
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data)
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); return }
    const hs = listeners.get(m.method)
    if (hs) for (const h of [...hs]) { try { h(m.params) } catch { /* 处理器自己的错不该打断通道 */ } }
  })
  const opened = await Promise.race([
    new Promise((r) => ws.addEventListener('open', () => r(true))),
    new Promise((r) => setTimeout(() => r(false), 10000)),
  ])
  if (!opened) throw new Error('CDP_OPEN_TIMEOUT')

  /**
   * 页内求值。**注意**：CDP 上 `Runtime.evaluate` 失败时信息在
   * `exceptionDetails.exception.description`，`exceptionDetails.text` 恒为 "Uncaught"。
   * 这里直接把 description 抛出去，别让调用方拿到一个没信息量的字符串。
   */
  /**
   * 通用 CDP 命令。`ev` 只够用 Runtime.evaluate；
   * 要开 `Network.enable`、订阅 `Network.requestWillBeSent` 这类**事件**，
   * 就必须走这里 —— 否则每个要抓网络/控制台的脚本都得自己再搭一遍 WebSocket。
   * 返回**整个 result 消息**（不是 result.result），因为
   * `Network.getResponseBody` 的载荷在 `result.body`，
   * 只回传 `m.result` 会把它整个丢掉。
   */
  const send = async (method, params = {}, ms = 20000) => {
    const i = ++id
    const v = await new Promise((r) => {
      const t = setTimeout(() => { pending.delete(i); r({ __timeout: 1 }) }, ms)
      pending.set(i, (y) => { clearTimeout(t); r(y) })
      ws.send(JSON.stringify({ id: i, method, params }))
    })
    if (v?.__timeout) throw new Error(`CDP_SEND_TIMEOUT（${ms}ms）: ${method}`)
    if (v?.error) throw new Error(`CDP_ERROR ${method}: ${v.error.message || JSON.stringify(v.error)}`)
    return v?.result
  }

  /** 订阅 CDP 事件；返回退订函数。close() 时全部失效。 */
  const on = (method, handler) => {
    if (!listeners.has(method)) listeners.set(method, new Set())
    listeners.get(method).add(handler)
    return () => listeners.get(method)?.delete(handler)
  }

  const ev = async (expr, ms = 20000) => {
    const v = await send('Runtime.evaluate', {
      expression: expr, returnByValue: true, awaitPromise: true,
    }, ms)
    if (v?.exceptionDetails) {
      const d = v.exceptionDetails?.exception?.description || v.exceptionDetails?.text || '(无描述)'
      throw new Error(`CDP_EVAL_EXCEPTION: ${String(d).slice(0, 300)}`)
    }
    return v?.result?.value
  }

  const close = async () => {
    try { listeners.clear() } catch { /* ignore */ }
    try { ws.close() } catch { /* 已经关了 */ }
    adbSoft(['forward', '--remove', `tcp:${port}`])
  }

  return { ws, port, pid, socket, ev, send, on, close }
}
