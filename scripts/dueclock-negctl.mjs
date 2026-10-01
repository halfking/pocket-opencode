// pkm/dueclock 负控：对 verify-dueclock-wiring.mjs 证明它有区分能力。
//
// 判据「全绿」本身不说明什么——它可能只是永远通过。所以必须造一个**有缺陷的一侧**，
// 确认判据在缺陷侧确实转红。这里造三种缺陷（都能在真实历史里出现）：
//   1. 修复前的样子：三个到期判据接成 liveNowSec()（wire-dueclock.mjs 静默失败产出的就是这个）
//   2. 少接一处：只改一个 computed，另外两个仍响应式
//   3. 记录时间戳错接成 dueNowSec()（反向错误，会把时间戳拖成旧值）
// 每种都必须让判据报红；只要有一种报绿，判据就不可信。
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = fileURLToPath(new URL('.', import.meta.url))
const SRC = fileURLToPath(new URL('../frontend/src/stores/flashcards.ts', import.meta.url))
const VERIFY = join(HERE, 'verify-dueclock-wiring.mjs')
const orig = readFileSync(SRC, 'utf8')

function runVerify(path) {
  try {
    const out = execFileSync(process.execPath, [VERIFY, path], { encoding: 'utf8' })
    return { code: 0, out }
  } catch (e) {
    return { code: e.status ?? -1, out: (e.stdout ?? '') + (e.stderr ?? '') }
  }
}

const mutations = [
  {
    name: '修复前状态：三个到期判据全接 liveNowSec()',
    apply: (s) => s.split('const now = dueNowSec()').join('const now = liveNowSec()'),
  },
  {
    name: '少接一处：只把 dueByDeck 还原成 liveNowSec()',
    apply: (s) => {
      const i = s.indexOf('const dueByDeck = computed(() => {')
      const j = s.indexOf('const now = dueNowSec()', i)
      return s.slice(0, j) + 'const now = liveNowSec()' + s.slice(j + 'const now = dueNowSec()'.length)
    },
  },
  {
    name: '反向错误：记录时间戳接成 dueNowSec()',
    apply: (s) => s.split('enqueuedAt: liveNowSec()').join('enqueuedAt: dueNowSec()'),
  },
]

const dir = mkdtempSync(join(tmpdir(), 'dueclock-negctl-'))
let bad = 0
for (const m of mutations) {
  const p = join(dir, `mut${mutations.indexOf(m)}.ts`)
  const mutated = m.apply(orig)
  if (mutated === orig) {
    console.log(` ❌ 负控「${m.name}」根本没改到文件 —— 变异写法失效，这个负控不算数`)
    bad++
    continue
  }
  writeFileSync(p, mutated, 'utf8')
  const r = runVerify(p)
  const failedChecks = (r.out.match(/^ FAIL /gm) || []).length
  if (r.code === 0) {
    console.log(` ❌ 负控「${m.name}」判据仍然报绿（EXIT=0）—— 判据没有区分能力`)
    bad++
  } else {
    console.log(` ✅ 负控「${m.name}」判据报红：EXIT=${r.code}，${failedChecks} 项 FAIL`)
  }
}

if (bad) {
  console.log(`\n❌ 负控失败 ${bad} 项`)
  process.exit(1)
}
console.log(`\n✅ 三种缺陷侧判据均报红，verify-dueclock-wiring.mjs 有区分能力`)
