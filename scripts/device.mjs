#!/usr/bin/env node
/**
 * 真机/模拟器 adb 操作助手（审计用）。
 *
 * 解决三个反复踩到的坑：
 *  1. 4c308e2e 会从 adb 列表里掉线 —— ensure() 自动重启 adb server 找回设备。
 *  2. PowerShell 重定向会破坏 PNG 二进制 —— 截图一律走 screencap + pull，并校验 PNG magic。
 *  3. 每条命令都是全新 shell —— 路径/serial 收敛到一处。
 *
 * 用法：
 *   node scripts/device.mjs ensure
 *   node scripts/device.mjs shot real-01            # 截图到 logs/audit/real-01.png
 *   node scripts/device.mjs tap 360 640
 *   node scripts/device.mjs text "http://127.0.0.1:8088"
 *   node scripts/device.mjs key 4
 *   node scripts/device.mjs swipe 360 1200 360 400 300
 *   node scripts/device.mjs sh "getprop ro.product.model"
 *   node scripts/device.mjs reverse tcp:8088
 */

import { execFileSync, spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, statSync, readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SHOT_DIR = join(ROOT, 'logs', 'audit')
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'

function adb(args, opts = {}) {
  return execFileSync(ADB, args, {
    encoding: opts.encoding ?? 'utf8',
    timeout: opts.timeout ?? 120000,
    maxBuffer: 64 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'pipe'],
    ...opts,
  })
}

function tryAdb(args, opts = {}) {
  const r = spawnSync(ADB, args, { encoding: 'utf8', timeout: opts.timeout ?? 120000, maxBuffer: 64 * 1024 * 1024 })
  if (r.error || r.status !== 0) return null
  return r.stdout
}

/** 列出在线设备 serial。 */
function listDevices() {
  const out = adb(['devices'])
  return out
    .split(/\r?\n/)
    .slice(1)
    .map((l) => l.trim().split(/\s+/))
    .filter((p) => p.length >= 2 && p[1] === 'device')
    .map((p) => p[0])
}

/** 确保目标设备在线；掉线时重启 adb server 再试。 */
function ensure(serial = SERIAL) {
  if (listDevices().includes(serial)) return serial
  tryAdb(['kill-server'])
  sleep(2000)
  tryAdb(['start-server'])
  sleep(4000)
  for (let i = 0; i < 6; i++) {
    if (listDevices().includes(serial)) return serial
    sleep(3000)
  }
  throw new Error(`device ${serial} unreachable; online: ${listDevices().join(',') || '<none>'}`)
}

function sleep(ms) {
  const end = Date.now() + ms
  while (Date.now() < end) {
    spawnSync(process.execPath, ['-e', 'setTimeout(()=>{},50)'])
  }
}

const isReal = () => SERIAL === '4c308e2e'

/** 截图并校验结果确实是一张 PNG。 */
function shot(name) {
  if (!existsSync(SHOT_DIR)) mkdirSync(SHOT_DIR, { recursive: true })
  const local = join(SHOT_DIR, `${name}.png`)
  let lastErr = ''
  // 设备会随机掉线，整段重试而不是只重试 pull
  for (let round = 0; round < 5; round++) {
    try {
      ensure()
      const remote = `/sdcard/_s_${round}.png`
      adb(['-s', SERIAL, 'shell', 'screencap', '-p', remote])
      adb(['-s', SERIAL, 'pull', remote, local])
      adb(['-s', SERIAL, 'shell', 'rm', '-f', remote])
      if (existsSync(local)) {
        const buf = readFileSync(local)
        if (buf.length > 1000 && buf[0] === 0x89 && buf[1] === 0x50 && buf[2] === 0x4e && buf[3] === 0x47) {
          // PNG IHDR: 宽高在字节 16..24 大端
          console.log(`${local}  ${buf.length}B  ${buf.readUInt32BE(16)}x${buf.readUInt32BE(20)}`)
          return local
        }
        lastErr = `not a png (${buf.length}B)`
      } else {
        lastErr = 'pull produced no file'
      }
    } catch (err) {
      lastErr = err.message
    }
    sleep(1500)
  }
  throw new Error(`screenshot ${name} failed after 5 rounds: ${lastErr}`)
}

const cmd = process.argv[2]
const a = process.argv.slice(3)

try {
  switch (cmd) {
    case 'ensure': {
      ensure()
      console.log(`online: ${listDevices().join(', ')} | target: ${SERIAL}`)
      break
    }
    case 'serial': {
      console.log(SERIAL)
      break
    }
    case 'devices': {
      console.log(listDevices().join('\n'))
      break
    }
    case 'size': {
      ensure()
      console.log(adb(['-s', SERIAL, 'shell', 'wm', 'size']).trim())
      console.log(adb(['-s', SERIAL, 'shell', 'wm', 'density']).trim())
      break
    }
    case 'shot': {
      shot(a[0])
      break
    }
    case 'tap': {
      ensure()
      adb(['-s', SERIAL, 'shell', 'input', 'tap', String(Math.round(+a[0])), String(Math.round(+a[1]))])
      console.log(`tap ${a[0]} ${a[1]}`)
      break
    }
    case 'longpress': {
      ensure()
      adb(['-s', SERIAL, 'shell', 'input', 'swipe', String(+a[0]), String(+a[1]), String(+a[0]), String(+a[1]), String(a[2] || 900)])
      console.log(`longpress ${a[0]} ${a[1]}`)
      break
    }
    case 'swipe': {
      ensure()
      adb(['-s', SERIAL, 'shell', 'input', 'swipe', ...a.map((v, i) => String(i === 4 ? +v : Math.round(+v)))])
      console.log(`swipe ${a.join(' ')}`)
      break
    }
    case 'text': {
      ensure()
      // input text 不可靠地支持 : / ? & = 等符号，用 %s 表示空格并整体 URL 编码规避 shell 解析
      const raw = a.join(' ')
      const encoded = raw.replace(/ /g, '%s').replace(/"/g, '\\"')
      adb(['-s', SERIAL, 'shell', `input text "${encoded}"`])
      console.log(`text ${raw}`)
      break
    }
    case 'key': {
      ensure()
      adb(['-s', SERIAL, 'shell', 'input', 'keyevent', String(a[0])])
      console.log(`key ${a[0]}`)
      break
    }
    case 'sh': {
      ensure()
      process.stdout.write(adb(['-s', SERIAL, 'shell', a.join(' ')]))
      break
    }
    case 'reverse': {
      ensure()
      const list = adb(['-s', SERIAL, 'reverse', '--list'])
      adb(['-s', SERIAL, 'reverse', a[0], a[1]])
      console.log(`reverse ${a[0]} ${a[1]}\n${list}---\n${adb(['-s', SERIAL, 'reverse', '--list'])}`)
      break
    }
    case 'forward': {
      ensure()
      console.log(adb(['-s', SERIAL, 'forward', a[0], a[1]]).trim() || 'ok')
      break
    }
    case 'install': {
      ensure()
      const apk = a[0]
      console.log(adb(['-s', SERIAL, 'install', '-r', '-d', apk], { timeout: 300000 }).trim())
      break
    }
    case 'launch': {
      ensure()
      const pkg = a[0] || 'com.kaixuan.opencode.pocket'
      adb(['-s', SERIAL, 'shell', 'am', 'force-stop', pkg])
      adb(['-s', SERIAL, 'shell', 'monkey', '-p', pkg, '-c', 'android.intent.category.LAUNCHER', '1'])
      sleep(+(a[1] || 6000))
      console.log('launched')
      break
    }
    case 'restart': {
      ensure()
      adb(['-s', SERIAL, 'shell', 'am', 'force-stop', a[0] || 'com.kaixuan.opencode.pocket'])
      adb(['-s', SERIAL, 'shell', 'monkey', '-p', a[0] || 'com.kaixuan.opencode.pocket', '-c', 'android.intent.category.LAUNCHER', '1'])
      sleep(6000)
      console.log('restarted')
      break
    }
    case 'webview': {
      ensure()
      // 当前前台 activity + WebView 调试端口
      console.log(adb(['-s', SERIAL, 'shell', 'dumpsys', 'activity', 'activities']).split(/\r?\n/).filter((l) => /mResumedActivity|topResumedActivity/.test(l)).join('\n'))
      break
    }
    case 'logcat-clear': {
      adb(['-s', SERIAL, 'logcat', '-c'])
      console.log('cleared')
      break
    }
    case 'logcat': {
      const out = adb(['-s', SERIAL, 'logcat', '-d', '-v', 'time'], { timeout: 60000 })
      const lines = out.split(/\r?\n/)
      const filt = a[0] ? lines.filter((l) => new RegExp(a[0], 'i').test(l)) : lines
      console.log(filt.slice(-(+(a[1] || 120))).join('\n'))
      break
    }
    case 'curl': {
      ensure()
      console.log(adb(['-s', SERIAL, 'shell', `curl -s -m ${a[1] || 6} ${JSON.stringify(a[0])}`], { timeout: 40000 }))
      break
    }
    case 'seq': {
      // 一次设备稳定窗口内批量执行，避免每步都重连：
      //   node scripts/device.mjs seq '[{"shot":"a"},{"wait":800},{"tap":360,640},{"shot":"b"}]'
      const ops = JSON.parse(readFileSync(a[0], 'utf8'))
      for (const [i, op] of ops.entries()) {
        const [k, v] = Object.entries(op)[0]
        const label = `${i + 1}/${ops.length} ${k}`
        try {
          if (k === 'shot') console.log(`> ${label} ${v}\n  ${shot(v)}`)
          else if (k === 'wait') { sleep(+v); console.log(`> ${label} ${v}ms`) }
          else if (k === 'tap') { ensure(); adb(['-s', SERIAL, 'shell', 'input', 'tap', String(Math.round(+v[0])), String(Math.round(+v[1]))]); console.log(`> ${label} ${v.join(',')}`) }
          else if (k === 'swipe') { ensure(); adb(['-s', SERIAL, 'shell', 'input', 'swipe', ...v.map(String)]); console.log(`> ${label} ${v.join(',')}`) }
          else if (k === 'text') { ensure(); adb(['-s', SERIAL, 'shell', `input text "${String(v).replace(/ /g, '%s')}"`]); console.log(`> ${label} ${v}`) }
          else if (k === 'key') { ensure(); adb(['-s', SERIAL, 'shell', 'input', 'keyevent', String(v)]); console.log(`> ${label} ${v}`) }
          else if (k === 'sh') { ensure(); console.log(`> ${label}\n${adb(['-s', SERIAL, 'shell', v])}`) }
          else if (k === 'eval') {
            // 在同一次设备稳定窗口内读 WebView：写表达式到临时文件再交给 cdp.mjs
            // v 以 .js/.mjs 结尾时视为脚本文件路径，否则视为内联表达式
            const tmp = join(SHOT_DIR, '__eval.js')
            const isFile = typeof v === 'string' && /\.m?js$/i.test(v)
            writeFileSync(tmp, isFile ? readFileSync(v, 'utf8') : (typeof v === 'string' ? v : JSON.stringify(v)))
            const out = spawnSync(process.execPath, [join(ROOT, 'scripts', 'cdp.mjs'), 'eval-file', tmp], { encoding: 'utf8', timeout: 90000 })
            console.log(`> ${label}\n${(out.stdout || '').trim() || (out.stderr || '').trim()}`)
          } else if (k === 'tapSel' || k === 'rectSel') {
            // 用 CDP 取元素真实 rect 再按 dpr 换算成物理像素点击，避免手工估坐标
            // v 形如 [selector, nth?, textMustContain?]
            const [sel, nth = 0, textFilter] = Array.isArray(v) ? v : [v, 0]
            const tmp = join(SHOT_DIR, '__rect.js')
            writeFileSync(tmp, `(() => {
              let els = [...document.querySelectorAll(${JSON.stringify(sel)})];
              const want = ${JSON.stringify(textFilter ?? null)};
              if (want) els = els.filter(e => (e.textContent||'').replace(/\\s+/g,' ').includes(want));
              const el = els[${Number(nth)}];
              if (!el) return JSON.stringify({ found: false, total: els.length });
              el.scrollIntoView({ block: 'center', behavior: 'instant' });
              const r = el.getBoundingClientRect();
              const dpr = window.devicePixelRatio;
              return JSON.stringify({ found: true, text: (el.textContent||'').replace(/\\s+/g,' ').trim().slice(0,40),
                cx: Math.round((r.x + r.width/2) * dpr), cy: Math.round((r.y + r.height/2) * dpr), dpr });
            })()`)
            const out = spawnSync(process.execPath, [join(ROOT, 'scripts', 'cdp.mjs'), 'eval-file', tmp], { encoding: 'utf8', timeout: 90000 })
            const raw = (out.stdout || '').trim()
            let info
            try { info = JSON.parse(raw) } catch { info = { found: false, error: raw } }
            if (!info.found) { console.log(`> ${label} NOT FOUND: ${sel} :: ${raw}`); if (!op.soft) break; continue }
            if (k === 'rectSel') { console.log(`> ${label} ${raw}`); continue }
            sleep(400)
            ensure()
            adb(['-s', SERIAL, 'shell', 'input', 'tap', String(info.cx), String(info.cy)])
            console.log(`> ${label} ${sel}[${nth}] "${info.text}" @ ${info.cx},${info.cy} (dpr ${info.dpr})`)
          }
          else if (k === 'curl') { ensure(); console.log(`> ${label}\n${adb(['-s', SERIAL, 'shell', `curl -s -m ${v[1] || 6} ${JSON.stringify(v[0])}`])}`) }
          else { console.log(`> ${label} UNKNOWN OP`); break }
        } catch (err) {
          console.log(`> ${label} FAILED: ${err.message}`)
          if (op.soft !== true) break
        }
      }
      break
    }
    default:
      console.error(`unknown command: ${cmd}`)
      process.exit(2)
  }
} catch (err) {
  console.error(`ERROR: ${err.message}`)
  process.exit(1)
}
