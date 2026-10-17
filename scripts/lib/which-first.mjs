// which-first.mjs —— 外部工具的定位：**不写死**，按「env → PATH → 常见位置」解析。
//
// 为什么要有这个文件
// ------------------
// 工具路径写死有两个已发生的同型故障：
//   · `scripts/lib/adb-cdp.mjs` 曾写死某台 Windows 开发机的 adb 绝对路径
//     ⇒ 换个宿主必然 spawn ENOENT，而错误发生在 spawn 层，
//        完全看不出「这个仓库的工具链绑在一台机器上」；
//   · `scripts/check-gofmt.mjs` 的 gofmt 只认 PATH
//     ⇒ 在 Go 装在 `~/sdk/go/bin`（本仓开发机的实际装法）的机器上，
//        `npm run gates` 永远停在 check:gofmt。
//
// 两处的**解析逻辑完全一样**，所以按 2026-10-07 的先例（a8cfb14b：
// 「收编到 lib 的单一来源」）提出来共用，而不是各抄一份。
//
// 设计要点
// --------
//   · 带 `/` 或 `\` 的候选按**字面路径**判断存在性；否则走 which/where。
//     两者混用会导致「相对路径被 which 当命令名查」的静默错误。
//   · 找不到就返回 null，**由调用方决定怎么办**。本库不代为兜底成
//     原命令名 —— 那样会把「工具没找到」重新藏回 spawn ENOENT 里，
//     而那正是本模块要消灭的错误形态。
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { homedir } from 'node:os'

/**
 * 依次尝试候选，返回第一个可用的（绝对路径，或 PATH 上的命令名）。
 * 全部不可用返回 null。
 *
 * @param {Array<string|null|undefined>} cands 候选，按优先级排列
 * @returns {string|null}
 */
export function whichFirst(cands) {
  for (const c of cands) {
    if (!c) continue
    if (c.includes('/') || c.includes('\\')) {
      if (existsSync(c)) return c
      continue
    }
    try {
      const r = execFileSync(process.platform === 'win32' ? 'where' : 'which', [c],
        { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] })
      const first = (r.stdout || '').split(/\r?\n/).map((s) => s.trim()).filter(Boolean)[0]
      if (r.status === 0 && first) return first
    } catch { /* which/where 不可用就跳到下一个候选 */ }
  }
  return null
}

/**
 * gofmt 的候选位置。
 *
 * 顺序说明：`GOFMT` → `POCKET_GOFMT`（与本仓 `POCKET_*` 覆盖惯例一致）
 * → PATH → GOROOT → `$HOME/go/bin` → `$HOME/sdk/go/bin` → 系统安装位置。
 * 最后保留裸命令名，使 PATH 上有 gofmt 时行为与从前完全一致。
 */
export function gofmtCandidates() {
  const exe = process.platform === 'win32' ? 'gofmt.exe' : 'gofmt'
  const goroot = process.env.GOROOT
  return [
    process.env.GOFMT,
    process.env.POCKET_GOFMT,
    exe,
    goroot && `${goroot}/bin/${exe}`,
    `${homedir()}/go/bin/${exe}`,
    `${homedir()}/sdk/go/bin/${exe}`,
    '/usr/local/go/bin/' + exe,
    '/opt/homebrew/bin/' + exe,
    process.platform === 'win32' ? 'C:/Program Files/Go/bin/gofmt.exe' : null,
  ]
}
