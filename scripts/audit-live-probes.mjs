#!/usr/bin/env node
// audit:live-probes —— 真网关探针的**手工回归入口**（默认只打印计划，不执行）。
//
// 为什么需要它（不是「整理文档」，是那个缺口本身）：
//   本仓的真网关探针有 **16 条**，分布在 `backend/internal/stt` 与 `backend/internal/server`，
//   每一条都被 `POCKET_LIVE_GATEWAY=1` 之类的环境变量门控、都**打真网关**（要钱、要时间）。
//   但仓里**没有任何一个入口**把它们串起来：
//     · `frontend/package.json` 里一条 live/gateway script 都没有；
//     · 唯一的说明散在设计文档里（按行号引用，而行号会随并发写入漂移）。
//   ⇒ 于是「跑一次真网关回归」这件事只能靠人记得住散落的片段 ⇒ 于是它通常不被跑。
//
// ★ 清单**由机器生成**，不手写：扫描 `*_test.go` 里对 `POCKET_LIVE_*` 的**代码引用**。
//   手写清单的下场是它与源码悄悄分叉，而**没有���何工具会告诉你它过期了**
//   —— 这正是本仓反复付过学费的同一类病（盘点类数字必然腐烂）。
//
// 用法：
//   node scripts/audit-live-probes.mjs            # 只打印计划（安全、不花钱）
//   node scripts/audit-live-probes.mjs --audio    # 额外标出「需要音频文件」的那几条
//   node scripts/audit-live-probes.mjs --run      # ⚠ 真跑（按打印出来的命令逐条执行）
//
// ⚠️ 它**不是判据**：默认不执行任何网络调用，也没有「通过/不通过」的退出码语义
//    ⇒ 按本仓 `notGates` 的约定，**刻意不接进 gates**（接进去只会让人误以为它是门）。

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const PKGS = ['backend/internal/stt', 'backend/internal/server']
const GATE_VAR = 'POCKET_LIVE_GATEWAY'

/** 剥掉 Go 注释（行 + 块 + 字符串态），只留可执行代码。 */
export function stripGoComments(src) {
  let out = ''
  let i = 0
  let state = 'code' // code | line | block | str | rune | raw
  while (i < src.length) {
    const c = src[i]
    const n = src[i + 1]
    if (state === 'code') {
      if (c === '/' && n === '/') { state = 'line'; i += 2; out += '  '; continue }
      if (c === '/' && n === '*') { state = 'block'; i += 2; out += '  '; continue }
      if (c === '"') { state = 'str'; out += c; i += 1; continue }
      if (c === "'") { state = 'rune'; out += c; i += 1; continue }
      if (c === '`') { state = 'raw'; out += c; i += 1; continue }
      out += c; i += 1; continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; out += '\n' } else { out += ' ' }
      i += 1; continue
    }
    if (state === 'block') {
      if (c === '*' && n === '/') { state = 'code'; i += 2; out += '  '; continue }
      out += c === '\n' ? '\n' : ' '
      i += 1; continue
    }
    if (state === 'raw') {
      if (c === '`') { state = 'code' }
      out += c; i += 1; continue
    }
    // str / rune
    if (c === '\\') { out += '  '; i += 2; continue }
    if ((state === 'str' && c === '"') || (state === 'rune' && c === "'")) state = 'code'
    out += c; i += 1
  }
  return out
}

function scan() {
  const found = []
  for (const pkg of PKGS) {
    const dir = path.join(ROOT, pkg)
    if (!fs.existsSync(dir)) continue
    for (const name of fs.readdirSync(dir).sort()) {
      if (!name.endsWith('_test.go')) continue
      const abs = path.join(dir, name)
      const raw = fs.readFileSync(abs, 'utf8')
      const code = stripGoComments(raw)
      // 量具自证：剥注释不该把函数定义也吃掉
      const funcsRaw = (raw.match(/^func\s+Test/gm) || []).length
      const funcsCode = (code.match(/^func\s+Test/gm) || []).length
      if (funcsRaw !== funcsCode) {
        throw new Error(`量具坏了：剥注释改变了 ${pkg}/${name} 的 Test 函数数（${funcsRaw} → ${funcsCode}）`)
      }
      const codeVars = [...new Set([...code.matchAll(/os\.Getenv\("(POCKET_LIVE[A-Z_]*)"\)/g)].map((m) => m[1]))]
      const anyVarInRaw = raw.includes(GATE_VAR)
      if (!codeVars.includes(GATE_VAR)) {
        // 只在注释/字符串里出现过 ⇒ 不算一条探针，但要如实报告这个现象
        if (anyVarInRaw) found.push({ pkg, name, vars: [], commentOnly: true })
        continue
      }
      found.push({ pkg, name, vars: codeVars.sort(), commentOnly: false, tests: funcsCode })
    }
  }
  return found
}

const args = process.argv.slice(2)
const wantAudio = args.includes('--audio')
const wantRun = args.includes('--run')

let rows
try {
  rows = scan()
} catch (e) {
  console.error(`FATAL: ${e.message}`)
  console.error('  退出码 2 = 量具坏了，不是「探针不存在」。')
  process.exit(2)
}

const probes = rows.filter((r) => !r.commentOnly)
const commentOnly = rows.filter((r) => r.commentOnly)

console.log('【真网关探针清单】清单由扫描 backend/internal/{stt,server}/*_test.go 生成')
console.log(`  探针文件 ${probes.length} 条 · ${probes.reduce((a, r) => a + (r.tests || 0), 0)} 个 Test 函数`
  + (commentOnly.length ? ` · 另有 ${commentOnly.length} 个文件只在注释里提到 ${GATE_VAR}（已排除）` : ''))
console.log('')

const groups = new Map()
for (const r of probes) {
  if (!groups.has(r.pkg)) groups.set(r.pkg, [])
  groups.get(r.pkg).push(r)
}
for (const [pkg, list] of groups) {
  console.log(`── ${pkg} ──`)
  for (const r of list) {
    const extra = r.vars.filter((v) => v !== GATE_VAR)
    const needsAudio = extra.includes('POCKET_LIVE_ASR_AUDIO') || extra.includes('POCKET_LIVE_ASR_MODEL')
    console.log(`  ${r.name}`)
    console.log(`      变量: ${r.vars.join(' ')}`)
    console.log(`      跑法: cd ${pkg === 'backend/internal/stt' ? 'backend' : 'backend'} && ${GATE_VAR}=1 \\`)
    console.log(`            go test ./${pkg.replace('backend/', '')} -run 'Test<该文件的测试名>' -v -count=1`)
    if (needsAudio) console.log('      ⚠️ 额外输入: 需要音频文件 ⇒ 依赖真机/真实录音素材')
    if (extra.includes('POCKET_LIVE_REFINE_MODEL')) console.log('      ⚠️ 额外输入: 需要指定精校模型名')
  }
  console.log('')
}

console.log('前置条件（缺任何一条都跑不起来）：')
console.log(`  1. ${GATE_VAR}=1            # 不设就全部 t.Skip（静默跳过，不会报错）`)
console.log('  2. POCKET_LLM_GATEWAY_URL / POCKET_LLM_GATEWAY_API_KEY   # 真网关地址与密钥')
console.log('  3. 一份音频（WAV/MP3），给带 POCKET_LIVE_ASR_AUDIO 的那几条')
console.log('')
console.log('⚠️ 这些调用**打真网关**：每次都要花钱、每次 10–40s，且会消耗配额。')
console.log('⚠️ 本脚本只是**清单与计划**：--run 也只是把上面打印的命令逐条交给你执行，不替你判断「通过」。')

if (wantAudio) {
  const need = probes.filter((r) => r.vars.includes('POCKET_LIVE_ASR_AUDIO'))
  console.log('')
  console.log(`需要音频文件的 ${need.length} 条：`)
  for (const r of need) console.log(`  ${r.pkg}/${r.name}`)
}

if (!wantRun) {
  console.log('')
  console.log('（默认只打印。确认清单与前置条件无误后再逐条执行；本脚本不会替你跑。）')
  process.exit(0)
}

// --run：只做前置条件体检，**不代跑**（代跑 = 替人下结论，而这里每条都要人看输出）
const env = process.env
const missing = []
if (!env[GATE_VAR]) missing.push(GATE_VAR)
if (!env.POCKET_LLM_GATEWAY_URL) missing.push('POCKET_LLM_GATEWAY_URL')
if (!env.POCKET_LLM_GATEWAY_API_KEY) missing.push('POCKET_LLM_GATEWAY_API_KEY')
if (missing.length) {
  console.error(`\nFATAL: 缺 ${missing.join(' / ')} ⇒ 现在跑只会全部静默 skip。`)
  process.exit(2)
}
console.log('\n前置条件齐了。逐条执行上面的命令；本脚本不代跑、不代判。')