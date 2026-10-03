// lib/dev-pass.mjs —— dev 口令的**唯一**取法。
//
// ## 为什么要有这个（2026-10-03）
//
// 仓库里曾有 **32 个**探针/验证脚本用同一种写法取 dev 口令：
//
//   const devPass = (readFileSync('backend/internal/server/server_assistant.go', 'utf8')
//     .match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || ''
//
// 那个 `devPass = "…"` 常量**已经被 `b6187bc1`（dev 旁路移除硬编码 admin 口令）删掉了**。
// 于是刮取**必然**返回空串（不是「有时失败」）：
//
//   login(password='') → 401 → **拿不到 token**
//   → 后面每一条探测都是**未鉴权**的
//   → `/api/marketplace/*` 一律返回 401（requireAuth 先于路由匹配跑）
//   → 而这份输出被当成「带有效 token 的观测」记进了 handoff，
//     变成一条「带有效 token 仍 401」的**幽灵结论**，连续三轮被当待查项搬来搬去。
//
// 同一个病还有另外两个变体，本模块一并覆盖：
//   - `readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'))`（换路径拼法）
//   - 硬编码**另一个 worktree** 的绝对路径 `C:/workspace/openpocket/wt3/...`
//     （那棵树可能根本不存在 ⇒ 读文件直接抛）
//
// ## 契约：缺口令必须**响亮地**失败
//
// 静默失败会产出**看起来像结论**的输出——这比崩溃危险得多。
// 所以这里**不是**返回空串让调用方继续跑，而是 `process.exit(2)`，
// 且在**碰设备/发请求之前**就退。
//
// ## 用法
//
//   import { requireDevPass } from './lib/dev-pass.mjs'
//   const password = requireDevPass()      // 缺 → 打印说明 + exit 2
//
// 刻意**不提供** `tryRequireDevPass()` 这种返回 null 的变体：
// 每个调用点都必须决定「没有口令该怎么办」，而本仓库的答案是「停下」。
import { existsSync } from 'node:fs'

// 依次尝试。优先级理由：
//   POCKET_AUTH_PASS  —— 后端 dev bootstrap 用的就是它（config.go:101），最贴切
//   POCKET_DEV_PASS   —— 探针侧约定的显式名
//   POCKET_MASTER     —— App 侧的主密码，历史上被当作 dev 口令用（但它同时也是
//                        App 的主密码，**不要**在这里打日志）
const VARS = ['POCKET_AUTH_PASS', 'POCKET_DEV_PASS', 'POCKET_MASTER']

export function resolveDevPass(env = process.env) {
  for (const k of VARS) {
    const v = env[k]
    if (typeof v === 'string' && v.length > 0) return v
  }
  return ''
}

/**
 * 取 dev 口令；没有就打印可操作的说明并 `exit 2`。
 * @param {{env?:NodeJS.ProcessEnv, exit?:(n:number)=>void, log?:(s:string)=>void}} [opts]
 * @returns {string}
 */
export function requireDevPass(opts = {}) {
  const env = opts.env || process.env
  const exit = opts.exit || ((n) => process.exit(n))
  const log = opts.log || ((s) => console.error(s))
  const pwd = resolveDevPass(env)
  if (pwd) return pwd

  log('')
  log('❌ DEV_PASS_MISSING —— 没有 dev 口令，**不继续跑**。')
  log('')
  log('为什么必须停下：拿着空口令去登录会拿到 401，于是「没有 token」这件事被')
  log('藏了起来，后面每一条探测都变成**未鉴权**的，输出里的 401 会被误读成')
  log('「这条路由有问题」。BUG-V12 就是这么把一条幽灵结论养了三轮的。')
  log('')
  log('怎么给（任选其一，都只走环境，不入库）：')
  for (const k of VARS) log(`  $env:${k}='<你的 dev 口令>'   # PowerShell`)
  log('')
  if (existsSync('backend/internal/server/server_assistant.go')) {
    log('⚠️ 不要再从源码里刮口令：那个 devPass 常量已被 b6187bc1 删除，')
    log('   刮取**必然**得到空串。实测 32 个脚本都栽在这里。')
    log('   总量见：node scripts/check-dev-pass-sourcing.mjs')
  }
  exit(2)
  // exit 在测试里被替换掉时继续走，保证调用方拿到的是确定的失败而不是 undefined
  throw new Error('DEV_PASS_MISSING')
}

/** dev 登录用的用户名（同样只从环境取，带一个与后端一致的缺省）。 */
export function devUser(env = process.env) {
  return env.POCKET_DEV_USER || env.POCKET_AUTH_USER || 'admin'
}
