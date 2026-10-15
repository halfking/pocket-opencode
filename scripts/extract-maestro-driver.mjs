/** 从 maestro-client.jar 里解出 driver APK，确认包名/版本是否与手头那份一致。
 *
 * ★ 2026-10-07 跨平台化：原来这里把 JAR / OUT / JDK 三处路径全部写死成
 *   Windows 形态（`C:/workspace/openpocket/…`、`C:/Program Files/Eclipse Adoptium/…`）。
 *   后果不是「在 macOS 上打不开」这么简单，而是**下游 harness 跟着一起死**：
 *   `maestro-run.mjs` 的 `DRIVER_APKS` 默认就指���这个脚本的 OUT，
 *   于是 macOS/Linux 上 ensureDriver() 永远报
 *   `[FAIL] APK 不存在: C:/workspace/openpocket/logs/maestro/driver-extracted/maestro-app.apk`。
 *   而这个报错形态与「MIUI 拦截安装」**完全同形**（都在同一行日志、都非 0 退出），
 *   于是真因（路径不存在）被误判成设备策略问题 ——
 *   实测在这台机器上白白绕了好几轮。
 *
 * 现在的取值顺序（每个都尊重已有设置）：
 *   1. 环境变量 POCKET_MAESTRO_JAR / POCKET_MAESTRO_DRIVER_DIR
 *   2. 本机 maestro 安装目录下 lib/maestro-client.jar（新版 jar 在，优先）
 *   3. PATH 上的 maestro 命令推断出的安装目录
 * 找不到就**响亮退出并说明怎么配**，不静默产出空目录。
 */
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const HOME = homedir()

/** 候选 maestro 安装目录（跨平台）。 */
const MAESTRO_DIRS = [
  `${HOME}/.maestro`,
  `${HOME}/.maestro-2`,
  'C:/Users/86133/.maestro',
  join(ROOT, 'logs/maestro/dist/maestro'),
]

function findJar() {
  if (process.env.POCKET_MAESTRO_JAR) return process.env.POCKET_MAESTRO_JAR
  for (const d of MAESTRO_DIRS) {
    const p = `${d}/lib/maestro-client.jar`
    if (existsSync(p)) return p
  }
  return null
}

function findOut() {
  if (process.env.POCKET_MAESTRO_DRIVER_DIR) return process.env.POCKET_MAESTRO_DRIVER_DIR
  // 默认放在**仓库内**而不是某个人的绝对路径：worktree / CI / 换机都能用。
  return `${ROOT}/logs/maestro/driver-extracted`
}

/** jar 命令：JDK 自带的 jar 跨平台可用；找不到就退回 unzip（JDK 缺失时的兜底）。 */
function jarTool() {
  const home = process.env.JAVA_HOME
  if (home && existsSync(`${home}/bin/jar`)) return `${home}/bin/jar`
  if (process.platform === 'win32') return null // Windows 上没 JAVA_HOME 就让用户自己配
  return null
}

const JAR = findJar()
const OUT = findOut()

if (!JAR) {
  console.error(`[extract-driver] 找不到 maestro-client.jar。已找过：`)
  for (const d of MAESTRO_DIRS) console.error(`  ${d}/lib/maestro-client.jar`)
  console.error(`  解法：export POCKET_MAESTRO_JAR=<你的 maestro-client.jar 路径>`)
  process.exit(2)
}
if (!existsSync(JAR)) {
  console.error(`[extract-driver] POCKET_MAESTRO_JAR 指向的文件不存在：${JAR}`)
  process.exit(2)
}

mkdirSync(OUT, { recursive: true })
console.log(`[extract-driver] jar  = ${JAR}`)
console.log(`[extract-driver] 输出 = ${OUT}`)

const jar = jarTool()
let apks
if (jar) {
  const listing = execFileSync(jar, ['tf', JAR], { encoding: 'utf8', maxBuffer: 33554432 })
  apks = listing.split(/\r?\n/).filter((l) => /\.apk$/i.test(l.trim()))
  for (const a of apks) {
    execFileSync(jar, ['xf', JAR, a.trim()], { cwd: OUT })
  }
} else {
  // 无 jar 命令时的兜底：unzip 也是 JDK 之外的常见选择。
  const listing = execFileSync('unzip', ['-Z1', JAR], { encoding: 'utf8', maxBuffer: 33554432 })
  apks = listing.split(/\r?\n/).filter((l) => /\.apk$/i.test(l.trim()))
  for (const a of apks) {
    execFileSync('unzip', ['-q', '-o', JAR, a.trim(), '-d', OUT], { encoding: 'utf8' })
  }
}

console.log(`jar 内 APK 条目（${apks.length}）:`)
for (const a of apks) console.log(`  ${a.trim()} -> ${join(OUT, a.trim().split('/').pop())}`)

if (!apks.length) {
  console.error('[extract-driver] jar 里没有 APK，driver 装不上是必然的。')
  process.exit(2)
}
console.log('[extract-driver] ✅ 完成')