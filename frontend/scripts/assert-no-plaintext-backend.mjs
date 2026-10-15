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

/**
 * 判定「后端协议 × WebView 壳 scheme」这一对（BUG-F 的**第二轴**）。
 *
 * 为什么 `checkApiBase` 不够：它只看后端地址，明文 http 指向本机时按设计放行；
 * 但壳的 scheme 是**另一根轴**，而且 Capacitor 是**运行时**从 assets 里读它的
 * （@capacitor/android 的 `CapConfig.java:254` 读 `server.androidScheme`，
 * 缺省 https）—— `vite build` 完全管不着这一轴，只有 `npx cap sync/copy`
 * 会把它写进 `android/app/src/main/assets/capacitor.config.json`。
 *
 * 两轴不匹配的形态：页面 origin = `https://localhost`（安全上下文）而后端是
 * 明文 http ⇒ 实时通道是 `ws://`，而 Chromium >= 111 的 "Insecure WebSocket"
 * 策略**硬阻断**它（不受 mixed content mode 管辖）⇒ WebSocket error → 无限重连，
 * 任务流式输出 / 审批 / 会话推送全部失效。更难认的是：debug 壳的
 * MIXED_CONTENT_ALWAYS_ALLOW 让 REST 照常成功，于是「页面能打开、数据能查」
 * 正好掩盖了「实时通道已死」；release 壳则是 REST 一起死。
 *
 * @returns {{ offending: boolean, relevant: boolean, reason: string }}
 *   relevant=false 表示这一轴不适用（后端不是明文），调用方可跳过打印。
 */
export function checkSchemePair(raw, androidScheme, { label = 'VITE_API_BASE' } = {}) {
  const value = String(raw ?? '').trim()
  const scheme = String(androidScheme ?? 'https').trim()
  if (!value) return { offending: false, relevant: false, reason: `${label} 未设置，这一轴不适用` }
  let u
  try {
    u = new URL(value)
  } catch {
    // 非法 URL 交给 checkApiBase 报，这里不重复报（避免一条问题出两行红）
    return { offending: false, relevant: false, reason: `${label} 不是合法 URL，交给第一轴判定` }
  }
  // https/wss 后端：wss 从安全上下文发起是被允许的，两轴一致，无需拦。
  if (u.protocol !== 'http:') {
    return { offending: false, relevant: false, reason: `${label} 不是明文 http，这一轴不适用` }
  }
  if (scheme === 'http') {
    return {
      offending: false,
      relevant: true,
      reason: `${label} 是明文 http，壳 scheme=http（非安全上下文），ws:// 放行`,
    }
  }
  if (scheme === 'https') {
    return {
      offending: true,
      relevant: true,
      reason: `${label} 是明文 http，但壳 scheme=https ⇒ ws:// 被 Chromium>=111 硬阻断（BUG-F 第二轴）`,
    }
  }
  return {
    offending: true,
    relevant: true,
    reason:
      `${label} 是明文 http，且 CAP_ANDROID_SCHEME=${scheme} 既非 http 也非 https；` +
      'CapConfig.validateScheme 对这种值只 warn 不拦，等于静默落到未知 origin',
  }
}

/** 明文后端 + 壳 scheme 没配套时的修法（改这一轴必须重跑 cap sync，vite build 不管用）。 */
export const SCHEME_FIX_HINT = [
  '  第二轴修法（只对明文后端 + Capacitor APK 生效）：',
  "    CAP_ANDROID_SCHEME=http npx cap sync android      # bash/zsh",
  "    $env:CAP_ANDROID_SCHEME='http'; npx cap sync android   # PowerShell",
  '    这一轴由 cap sync/copy 写进 android/app/src/main/assets/capacitor.config.json，',
  '    vite build 改不了它；只改环境变量不重跑 cap sync 等于没改。',
].join('\n')

function main() {
  const allow = process.env.POCKET_ALLOW_PLAINTEXT_API === '1'
  // 扫所有看起来像后端地址的 VITE_ 变量，避免下一个人换个变量名就绕过
  const candidates = Object.entries(process.env)
    .filter(([k, v]) => /^VITE_.*(API_BASE|API_URL|BACKEND|GATEWAY_URL)$/.test(k) && v && String(v).trim())
    .map(([k, v]) => [k, String(v).trim()])

  const rows = candidates.map(([k, v]) => ({ k, v, ...checkApiBase(v, { label: k }) }))

  // 第二轴：后端协议 × 壳 scheme 是否配套。缺省 https —— 与 capacitor.config.ts 一致。
  const scheme = process.env.CAP_ANDROID_SCHEME ?? 'https'
  const pairRows = candidates
    .map(([k, v]) => ({ k, v, ...checkSchemePair(v, scheme, { label: k }) }))
    .filter((r) => r.relevant)
  const schemeBad = pairRows.filter((r) => r.offending)
  const bad = [...rows, ...pairRows].filter((r) => r.offending)

  console.log('=== BUG-F 明文后端构建守卫 ===')
  if (!rows.length) {
    console.log('  未设置任何 VITE_* 后端地址变量 → 同源相对路径，OK')
  }
  for (const r of rows) {
    console.log(`  ${r.offending ? 'FAIL' : 'ok  '}  ${r.k}=${r.v}\n        ${r.reason}`)
  }
  if (pairRows.length) {
    console.log(`\n  -- 第二轴：后端协议 × 壳 scheme（CAP_ANDROID_SCHEME=${scheme}）--`)
    for (const r of pairRows) {
      console.log(`  ${r.offending ? 'FAIL' : 'ok  '}  ${r.k}\n        ${r.reason}`)
    }
  }

  if (bad.length && allow) {
    console.log('\n  ⚠ 已设置 POCKET_ALLOW_PLAINTEXT_API=1，跳过拦截。')
    console.log('    这是为真机明文联调保留的逃生舱，**不要**在 release 流程里设置。')
    if (schemeBad.length) {
      console.log('    注意：第二轴仍然存在——明文后端 + https 壳会让实时通道（ws://）被硬阻断。')
    }
    process.exit(0)
  }
  if (bad.length) {
    console.error(`\n✗ 构建中止：${bad.length} 条后端地址配置被拦（BUG-F）。`)
    console.error('  修法：改成 https://；若确为明文联调，显式设置')
    console.error('        $env:POCKET_ALLOW_PLAINTEXT_API=\'1\' 再构建。')
    if (schemeBad.length) {
      console.error(SCHEME_FIX_HINT)
    }
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
