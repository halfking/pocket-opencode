#!/usr/bin/env node
/**
 * 构建守卫：禁止把**明文 http:// 的非本机后端**打进前端构建。
 *
 * 背景（BUG-F）：`CAP_ANDROID_SCHEME=http` 是为了绕开 https 下 XHR 混合内容被拦
 * 而引入的 opt-in 逃生舱。上一轮有人把它和 `VITE_API_BASE=http://<局域网 IP>:8088`
 * 一起提交/装包，release 路径的 APK 就指着明文后端跑，而默认构建仍是 https——
 * 于是「包被换了」这件事在 UI 上完全看不出来（override 静默压过构建默认值，
 * 页面却显示构建默认值），排查成本极高。
 *
 * 这条断言就是让「下一个人」在 `npm run build` 阶段就被拦下，而不是在真机上
 * 花一小时才发现 origin 不对。
 *
 * 逃生舱：本机/模拟器回环地址（localhost / 127.0.0.1 / 10.0.2.2 / *.local）
 * 是正常联调目标，不拦。确实要打明文局域网包时，显式 opt-in：
 *   $env:POCKET_ALLOW_PLAINTEXT_API='1'; npm run build:fast
 */
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')

/** 允许明文 http 的目标主机：本机 / 模拟器 / 局域网 .local 名字。 */
const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '0.0.0.0', '::1', '10.0.2.2', '[::1]'])

export function isLoopbackHost(host) {
  const h = String(host || '').trim().toLowerCase()
  if (LOOPBACK_HOSTS.has(h)) return true
  // 127.0.0.0/8 与 *.local / *.localhost
  if (/^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(h)) return true
  if (h.endsWith('.local') || h.endsWith('.localhost')) return true
  return false
}

/**
 * 判定一个候选后端地址是否会让构建失败。
 * @returns {{ offending: boolean, reason: string }}
 */
export function checkApiBase(raw, { label = 'VITE_API_BASE' } = {}) {
  const value = String(raw ?? '').trim()
  if (!value) return { offending: false, reason: `${label} 未设置（同源相对路径，OK）` }

  let u
  try {
    u = new URL(value)
  } catch {
    return { offending: true, reason: `${label}=${value} 不是合法 URL` }
  }
  if (u.protocol === 'https:') return { offending: false, reason: `${label} 是 https，OK` }
  if (u.protocol !== 'http:') {
    return { offending: true, reason: `${label}=${value} 的 scheme 是 ${u.protocol}，只允许 http/https` }
  }
  if (isLoopbackHost(u.hostname)) {
    return { offending: false, reason: `${label} 是明文 http 但指向本机（${u.hostname}），联调允许` }
  }
  return {
    offending: true,
    reason: `${label}=${value} 指向**非本机的明文 http 后端**`,
  }
}

function main() {
  const allow = process.env.POCKET_ALLOW_PLAINTEXT_API === '1'
  // 扫所有看起来像后端地址的 VITE_ 变量，避免下一个人换个变量名就绕过
  const candidates = Object.entries(process.env)
    .filter(([k, v]) => /^VITE_.*(API_BASE|API_URL|BACKEND|GATEWAY_URL)$/.test(k) && v && String(v).trim())
    .map(([k, v]) => [k, String(v).trim()])

  const rows = candidates.map(([k, v]) => ({ k, v, ...checkApiBase(v, { label: k }) }))
  const bad = rows.filter((r) => r.offending)

  console.log('=== BUG-F 明文后端构建守卫 ===')
  if (!rows.length) {
    console.log('  未设置任何 VITE_* 后端地址变量 → 同源相对路径，OK')
  }
  for (const r of rows) {
    console.log(`  ${r.offending ? 'FAIL' : 'ok  '}  ${r.k}=${r.v}\n        ${r.reason}`)
  }

  if (bad.length && allow) {
    console.log('\n  ⚠ 已设置 POCKET_ALLOW_PLAINTEXT_API=1，跳过拦截。')
    console.log('    这是为真机明文联调保留的逃生舱，**不要**在 release 流程里设置。')
    process.exit(0)
  }
  if (bad.length) {
    console.error(`\n✗ 构建中止：${bad.length} 个后端地址是明文 http（BUG-F）。`)
    console.error('  修法：改成 https://；若确为真机明文联调，显式设置')
    console.error('        $env:POCKET_ALLOW_PLAINTEXT_API=\'1\' 再构建。')
    process.exit(1)
  }
  console.log('\n✓ 守卫通过')
}

// 仅在被直接执行时跑 main（被 import 做单测时不跑）
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1].replace(/\\/g, '/')) {
  main()
} else if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  main()
}
