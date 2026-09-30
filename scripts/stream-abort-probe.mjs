#!/usr/bin/env node
/**
 * 真机验证：AI 对话「停止生成」是否真能中止请求（与 LLM 网关是否就绪无关）。
 *
 * 为什么要拦截：正常情况下 /api/llm/stream 打到 pocketd 再转发网关，
 * 而网关当前 ready=false，流会在百来毫秒内失败 —— 停止按钮根本没有可观测窗口，
 * 无法证明「可强行终止」。用 CDP 的 Fetch 域把该请求挂起，就能在真机上
 * 稳定复现「流进行中」的状态，从而观察真实的 UI 与真实的 abort 行为。
 *
 * 判据（全部来自真实链路，不看代码猜）：
 *   1. 页面确实发出了 /api/llm/stream 请求   —— Fetch.requestPaused 事件
 *   2. 停止按钮在流进行中真实出现             —— .send-btn.stop 在 DOM 里
 *   3. 点击后请求被真实取消                   —— fulfillRequest 报 canceled，
 *                                              即页面侧的 AbortController 真的生效
 *
 * Run: node scripts/stream-abort-probe.mjs
 */
import { execFileSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9223'
/** 挂起多久后补发 SSE。必须长于「点停止」的时机，才能观察到取消。 */
const HOLD_MS = 12_000

const adb = (args, timeout = 60000) =>
  execFileSync(ADB, args, { encoding: 'utf8', timeout, maxBuffer: 32 * 1024 * 1024 })

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function forward() {
  const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
  if (!pid) throw new Error('app not running')
  try { adb(['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`]) } catch { /* 本来就没有 */ }
  adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:webview_devtools_remote_${pid}`])
}

forward()
const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
  .catch(() => { throw new Error('devtools list failed') })
const page = list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl)
if (!page) throw new Error('no page target')

const ws = new WebSocket(page.webSocketDebuggerUrl)
const pending = new Map()
const events = []
let msgId = 0

ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) {
    const { resolve, reject } = pending.get(m.id)
    pending.delete(m.id)
    m.error ? reject(new Error(m.error.message)) : resolve(m.result)
  } else if (m.method) {
    events.push(m)
  }
})
await new Promise((res, rej) => {
  ws.addEventListener('open', res, { once: true })
  ws.addEventListener('error', () => rej(new Error('ws error')), { once: true })
  setTimeout(() => rej(new Error('ws open timeout')), 15000)
})
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++msgId
  pending.set(id, { resolve, reject })
  ws.send(JSON.stringify({ id, method, params }))
  setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error(`${method} timeout`)) } }, 45000)
})

const evaluate = async (expression) => {
  const r = await send('Runtime.evaluate', {
    expression, awaitPromise: true, returnByValue: true, allowUnsafeEvalBlocklistedAPI: true,
  })
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text)
  return r.result?.value
}

// ---- 1. 打开拦截：只挂起 LLM 流式请求，其他一律放行 ----
await send('Fetch.enable', { patterns: [{ urlPattern: '*llm/stream*', requestStage: 'Request' }] })
console.log('已开启 Fetch 拦截（仅 */llm/stream*）')

// ---- 2. 页面侧：登录态 + 进入对话页 + 发一条消息 ----
await evaluate(`
  (async () => {
    localStorage.setItem('pocket_api_base', 'http://127.0.0.1:8088');
    location.hash = '#/ai-chat';
    return true;
  })()
`)
// 必须确认真的落在对话页再填词：固定 sleep 会撞上路由切换中的旧页面，
// Enter 会被别的页面的输入框接走（实测曾误触发笔记详情的「AI 总结」）。
let ready = null
for (let i = 0; i < 60; i++) {
  await sleep(300)
  ready = await evaluate(`
    (() => {
      const el = document.querySelector('.uc-input');
      return { hash: location.hash, hasInput: !!el, title: (document.querySelector('.title')?.textContent||'').trim() };
    })()
  `)
  if (ready && ready.hasInput && ready.hash === '#/ai-chat') break
}
if (!ready || !ready.hasInput || ready.hash !== '#/ai-chat') {
  console.log('未能进入对话页:', JSON.stringify(ready))
  process.exit(2)
}
console.log('对话页就绪:', JSON.stringify(ready))
await evaluate(`
  (async () => {
    const el = document.querySelector('.uc-input') || document.querySelector('textarea');
    if (!el) return 'no-input';
    const proto = HTMLTextAreaElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, '用一句话解释什么是大模型');
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return 'filled';
  })()
`)
await sleep(500)
const submitResult = await evaluate(`
  (() => {
    const el = document.querySelector('.uc-input') || document.querySelector('textarea');
    if (!el) return 'no-input';
    el.focus();
    el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true, cancelable: true }));
    return 'enter-sent';
  })()
`)
console.log('页面提交动作:', submitResult)

// ---- 3. 观察：请求是否被挂起 + 停止按钮是否出现 ----
const t0 = Date.now()
let paused = null
let stopBtnSeenAt = null
let stopBtnInfo = null
let canceled = false
let fulfillError = ''
let pageClickedStop = false

// 后台：处理挂起的请求
;(async () => {
  for (;;) {
    await sleep(120)
    const ev = events.find((e) => e.method === 'Fetch.requestPaused')
    if (!ev || paused) continue
    paused = { id: ev.params.requestId, url: ev.params.request.url, at: Date.now() - t0 }
    console.log(`\n>> 捕获请求 ${paused.url}\n   挂起于 ${paused.at}ms`)
    await sleep(HOLD_MS)
    try {
      await send('Fetch.fulfillRequest', {
        requestId: paused.id,
        responseCode: 200,
        responseHeaders: [
          { name: 'Content-Type', value: 'text/event-stream' },
          { name: 'Cache-Control', value: 'no-cache' },
        ],
        body: Buffer.from(
          'data: {"content":"（补发的测试内容）"}\n\ndata: [DONE]\n\n',
        ).toString('base64'),
      })
      console.log('   补发 SSE 成功（未被取消）')
    } catch (e) {
      canceled = /canceled|Failed to fulfill|Invalid InterceptionId/i.test(e.message)
      fulfillError = e.message
      console.log(`   补发失败: ${e.message}  → 判定为已取消=${canceled}`)
    }
  }
})()

// 前台：等停止按钮出现并点击
for (let i = 0; i < 100; i++) {
  await sleep(150)
  const info = await evaluate(`
    (() => {
      const b = document.querySelector('.send-btn.stop');
      if (!b) return null;
      const r = b.getBoundingClientRect();
      return { w: Math.round(r.width), h: Math.round(r.height), left: Math.round(r.left), top: Math.round(r.top),
               label: b.getAttribute('aria-label'), bg: getComputedStyle(b).backgroundColor };
    })()
  `)
  if (info && !stopBtnSeenAt) {
    stopBtnSeenAt = Date.now() - t0
    stopBtnInfo = info
    console.log(`\n>> 停止按钮出现于 ${stopBtnSeenAt}ms:`, JSON.stringify(info))
    // 真机点按（用 CDP 派发真实鼠标事件到按钮中心）
    const x = info.left + info.w / 2
    const y = info.top + info.h / 2
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
    pageClickedStop = true
    console.log(`   已在 (${x}, ${y}) 点按停止按钮`)
  }
  if (canceled) break
  if (paused && Date.now() - t0 > HOLD_MS + 3000) break
}

await sleep(500)
const after = await evaluate(`
  (() => {
    const b = document.querySelector('.send-btn.stop');
    return { stillStopBtn: !!b, hash: location.hash,
             text: (document.body.innerText||'').replace(/\\s+/g,' ').slice(-180) };
  })()
`)

await send('Fetch.disable').catch(() => {})
ws.close()

console.log('\n================ 判定 ================')
console.log('1. 页面发出了 /api/llm/stream 请求 :', paused ? '✅ ' + paused.url : '❌ 未捕获')
console.log('2. 停止按钮在流进行中出现         :', stopBtnSeenAt !== null ? `✅ ${stopBtnSeenAt}ms` : '❌ 未出现')
console.log('3. 点击后请求被真实取消           :', canceled ? `✅ ${fulfillError}` : (paused ? '❌ 未被取消' : '—'))
console.log('4. 点击后按钮复位（停止态已解除） :', after && !after.stillStopBtn ? '✅' : '❌')
console.log('页面尾部文本:', after && after.text)
process.exitCode = paused && stopBtnSeenAt !== null && canceled ? 0 : 1
