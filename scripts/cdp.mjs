#!/usr/bin/env node
/**
 * 真机 WebView CDP 客户端（审计用）。
 *
 * debug APK 的 WebView 暴露 webview_devtools_remote_<pid>，转发到本地后即可
 * 用 Runtime.evaluate 直接读 DOM / localStorage / 计算样式，不必靠截图猜像素。
 *
 * 用法：
 *   node scripts/cdp.mjs eval "document.title"
 *   node scripts/cdp.mjs eval-file logs/audit/q.js
 *   node scripts/cdp.mjs targets
 *   node scripts/cdp.mjs shot-dom <out.png>       # 抓当前 WebView 可视区（走 screencap，保留状态栏）
 */

import { execFileSync } from 'node:child_process'
import { readFileSync, existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
// ★ 2026-10-07 跨平台化：原来这里无条件写死某个 Windows 用户的 adb.exe
// （`C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe`），
// 没有任何平台分支也没有环境变量覆盖 ⇒ 在 macOS / Linux 上必然找不到，
// 而报错形态与「设备没连上」完全同形。
//
// 现在按 ① POCKET_ADB_BIN ② 本机常见位置（macOS / Linux / Windows 各一）
// ③ PATH 上的 adb 依次找；找不到就响亮退出并说明怎么配，不静默失败。
const ADB = (process.env.POCKET_ADB_BIN
  || [
      join(process.env.ANDROID_HOME || '', 'platform-tools', 'adb'),
      join(process.env.ANDROID_SDK_ROOT || '', 'platform-tools', 'adb'),
      join(homedir(), 'Library/Android/sdk/platform-tools/adb'),
      join(homedir(), 'Android/Sdk/platform-tools/adb'),
      '/usr/local/share/android-sdk/platform-tools/adb',
      join(process.env.LOCALAPPDATA || '', 'Android/platform-tools/adb.exe'),
    ].find((p) => p && existsSync(p))
  || 'adb')
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9222'

function adb(args, timeout = 60000) {
  return execFileSync(ADB, args, { encoding: 'utf8', timeout, maxBuffer: 32 * 1024 * 1024 })
}

function listDevices() {
  return adb(['devices'])
    .split(/\r?\n/).slice(1)
    .map((l) => l.trim().split(/\s+/))
    .filter((p) => p.length >= 2 && p[1] === 'device')
    .map((p) => p[0])
}

function sleep(ms) { Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms) }

/**
 * 等设备上线。
 *
 * 注意：这里**不能**重启 adb server——本模块常被 device.mjs 的 seq 作为子进程调用，
 * 父进程正持有 adb 连接，kill-server 会把父进程一起打断。掉线恢复交给父进程，
 * 子进程只负责轮询等待。
 */
function ensure() {
  if (listDevices().includes(SERIAL)) return
  for (let i = 0; i < 10; i++) {
    sleep(3000)
    if (listDevices().includes(SERIAL)) return
  }
  throw new Error(`device ${SERIAL} unreachable; online: ${listDevices().join(',') || '<none>'}`)
}

function devicePid() {
  ensure()
  const out = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim()
  return out.split(/\s+/).filter(Boolean)[0]
}

function forward() {
  const pid = devicePid()
  if (!pid) throw new Error(`app not running (pidof ${PKG} empty)`)
  try { adb(['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`]) } catch { /* 本来就没有 */ }
  adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:webview_devtools_remote_${pid}`])
  return pid
}

async function targets() {
  forward()
  const res = await fetch(`http://127.0.0.1:${PORT}/json/list`)
  return res.json()
}

function pickPage(list) {
  const pages = list.filter((t) => t.type === 'page' && t.webSocketDebuggerUrl)
  if (!pages.length) throw new Error('no debuggable page target')
  // 优先挑 OpenPocket 自己的页面
  return pages.find((t) => /localhost|openpocket|pocket/i.test(t.url)) || pages[0]
}

let msgId = 0
async function evaluate(wsUrl, expression, { awaitPromise = true, returnByValue = true } = {}) {
  const ws = new WebSocket(wsUrl)
  const pending = new Map()
  ws.addEventListener('message', (ev) => {
    const msg = JSON.parse(ev.data)
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id)
      pending.delete(msg.id)
      if (msg.error) reject(new Error(msg.error.message))
      else resolve(msg.result)
    }
  })
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true })
    ws.addEventListener('error', () => reject(new Error('ws error')), { once: true })
    setTimeout(() => reject(new Error('ws open timeout')), 15000)
  })
  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const id = ++msgId
      pending.set(id, { resolve, reject })
      ws.send(JSON.stringify({ id, method, params }))
      setTimeout(() => {
        if (pending.has(id)) { pending.delete(id); reject(new Error(`${method} timeout`)) }
      }, 45000)
    })

  const res = await send('Runtime.evaluate', {
    expression,
    awaitPromise,
    returnByValue,
    allowUnsafeEvalBlocklistedAPI: true,
  })
  ws.close()
  if (res.exceptionDetails) {
    throw new Error(res.exceptionDetails.exception?.description || res.exceptionDetails.text)
  }
  return res.result?.value
}

const cmd = process.argv[2]
const arg = process.argv[3]

try {
  if (cmd === 'targets') {
    const list = await targets()
    console.log(JSON.stringify(list.map((t) => ({ type: t.type, title: t.title, url: t.url })), null, 2))
  } else if (cmd === 'eval' || cmd === 'eval-file') {
    let expr = cmd === 'eval' ? arg : readFileSync(arg, 'utf8')
    // 占位符注入：脚本在浏览器上下文执行，拿不到 process.env。
    // 把 __ENV_<NAME> 替换成对应环境变量的 JSON 字面量，凭据无需落盘到脚本文件。
    expr = expr.replace(/__ENV_([A-Z0-9_]+)__/g, (_m, name) => {
      const v = process.env[name]
      if (v === undefined) throw new Error(`env ${name} not set`)
      return JSON.stringify(v)
    })
    const list = await targets()
    const page = pickPage(list)
    const val = await evaluate(page.webSocketDebuggerUrl, expr)
    console.log(typeof val === 'string' ? val : JSON.stringify(val, null, 2))
  } else {
    console.error('usage: cdp.mjs targets | eval <expr> | eval-file <path>')
    process.exit(2)
  }
} catch (err) {
  console.error(`ERROR: ${err.message}`)
  process.exit(1)
}
