// gofmt-oracle.mjs —— 「用哪个 gofmt 当判据」的**单一来源**。
//
// 为什么单独成文件（2026-10-20）
// ------------------------------
// 本仓有两道门把 **gofmt 当判据**：
//   · scripts/check-gofmt.mjs                  —— 格式（缩进/对齐/行尾）
//   · scripts/check-doc-empty-string-literal.mjs —— doc comment 里的 '' 会不会被改写
//
// ⚠⚠ 它们**必须用同一个 gofmt**。否则同一台机器上两道门可能各说各话：
//   一道按 go1.24 判、另一道按 go1.27 判 ⇒ 结论互相矛盾，且极难排查
//   （「gofmt 门说是真债，doc 门说不用改」这类读数完全自相矛盾）。
//   而本机就同时装着 1.24.5 与 1.27.2，只差 PATH 指向谁 ⇒ 这不是假想场景。
//   ⇒ 解析逻辑只此一份，两个门都从这里取。
//
// 为什么必须锁版本
// ----------------
// **gofmt 不同版本对同一段 doc 的答案不同**。Go 1.25 给 go/printer 的 doc 判定
// 加了 `tok != token.IDENT` ⇒ 例：
//
//   type T struct {
//   // X has '' for empty
//   X int
//   }
//
// Go 1.24：**当 doc comment ⇒ 改写成 ”**；Go 1.25+：**不当 doc ⇒ 原样保留**。
//
// 而本仓 CI 是 `setup-go` + `go-version-file: backend/go.mod`
// ⇒ **权威版本就是 backend/go.mod 声明的那个** ⇒ 本地也必须用它，
//   否则「本地绿、CI 红」（或反过来）这类问题会以最难查的形式出现。
//
// 找不到匹配版本时 ⇒ **判「无法判定」，交由调用方 exit 1**（不是「通过」）。
// 理由同 scripts/check-gofmt.mjs 原有的「找不到 gofmt ⇒ exit 1」：
// **「无法判定」不是「通过」**。

import { spawnSync } from 'node:child_process'
import { readFileSync, realpathSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { whichFirst, gofmtCandidates } from './which-first.mjs'

const SELF = path.resolve(fileURLToPath(import.meta.url))

/** 仓库根（scripts/lib/ → 上两级）。 */
export const REPO_ROOT = path.resolve(path.dirname(SELF), '..', '..')

/**
 * 读 backend/go.mod 的 `go` 指令 ⇒ 声明的大版本（如 '1.27'）。
 * 读不到 ⇒ null（调用方按「无法判定」处理，不静默降级）。
 */
export function readDeclaredGoMinor(root = REPO_ROOT) {
  try {
    const src = readFileSync(path.join(root, 'backend', 'go.mod'), 'utf8')
    const m = src.match(/^go\s+(\d+)\.(\d+)/m)
    return m ? `${m[1]}.${m[2]}` : null
  } catch {
    return null
  }
}

/**
 * 取某个 gofmt 二进制的版本 ⇒ {full:'1.27.2', minor:'1.27'} 或 null。
 * 两条来源：GOROOT 的 VERSION 文件 → `go version -m`。
 */
export function gofmtVersionOf(gofmtPath) {
  const parse = (s) => {
    const m = String(s).match(/go(\d+\.\d+)(?:\.(\d+))?/)
    if (!m) return null
    return { full: m[2] ? `${m[1]}.${m[2]}` : m[1], minor: m[1] }
  }
  try {
    const bin = realpathSync(gofmtPath)
    const goroot = path.resolve(path.dirname(bin), '..')
    const p = parse(readFileSync(path.join(goroot, 'VERSION'), 'utf8'))
    if (p) return p
  } catch { /* 不是标准安装布局 ⇒ 换下一条来源 */ }
  try {
    const r = spawnSync('go', ['version', '-m', gofmtPath], { encoding: 'utf8' })
    if (!r.error && r.status === 0) {
      const p = parse((r.stdout || '').split('\n')[0])
      if (p) return p
    }
  } catch { /* go 不在 PATH 上 */ }
  return null
}

/**
 * 从候选里挑出**与声明大版本一致**的那个 gofmt。
 *
 * ★ 这条是纯函数、候选表是参数 ⇒ 「一个都不匹配 ⇒ 判无法判定」这条**失败分支
 *   可被自证**（否则它只是一段从没跑过的代码，而「无法判定」恰恰是最该被验的分支：
 *   它是「门在量具失效时会不会静默放行」的唯一防线）。
 *
 * @param {string[]} candidates 候选位置（裸名或绝对路径）
 * @param {string|null} declared 声明的大版本，如 '1.27'；null = 读不到声明
 * @returns {{path:string,version:string,declared:string,probed:Array}
 *          |{error:string,declared:string|null,probed:Array}}
 */
export function pickMatchingGofmt(candidates, declared) {
  const probed = []
  const seen = new Set()
  for (const c of candidates) {
    const abs = whichFirst([c])
    if (!abs || seen.has(abs)) continue
    seen.add(abs)
    const version = gofmtVersionOf(abs)
    probed.push({ path: abs, version: version ? version.full : null })
    if (declared && version && version.minor === declared) {
      return { path: abs, version: version.full, declared, probed }
    }
  }
  if (!declared) {
    return {
      error: '读不到 backend/go.mod 的 go 指令 ⇒ 无法确定权威 gofmt 的版本。',
      declared, probed,
    }
  }
  const lines = probed.map((p) => `    ${p.path}  →  ${p.version || '版本未知'}`).join('\n')
  return {
    error: `找不到与 backend/go.mod 声明的 Go ${declared} 匹配的 gofmt。\n`
      + `  已探测：\n${lines || '    （一个都没找到）'}\n`
      + `  本仓的门禁把 gofmt 当判据，而 Go 1.25 改过 doc comment 的判定\n`
      + `  （go/printer 新增 tok != token.IDENT）⇒ 用错版本会得出与 CI 相反的结论。\n`
      + `  ⇒ 这是「无法判定」，不是「通过」。\n`
      + `  修法：装 Go ${declared}.x，或用 GOFMT=/path/to/gofmt 显式指定。`,
    declared, probed,
  }
}

/** 用真实的候选表与真实的声明去解析权威 gofmt。 */
export function resolveAuthoritativeGofmt() {
  return pickMatchingGofmt(gofmtCandidates(), readDeclaredGoMinor())
}

/** 一行可读的 oracle 描述（两道门都打印它 ⇒ 两边的读数可对照）。 */
export function describeOracle(oracle) {
  if (!oracle || oracle.error) return '（未解析）'
  return `${oracle.path}（go${oracle.version}，backend/go.mod 声明 Go ${oracle.declared}）`
}
