// fix-bugn-heading.mjs — 修 BUG-N 小节标题里的措辞（初稿写成"编辑笔记恒 405"）。
// 真机复验后：编辑走本地 SQLite 一直是好的，BUG-N 是 API 契约不匹配。
import { readFileSync, writeFileSync } from 'node:fs'

const P = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
let s = readFileSync(P, 'utf8')

const from = '### 4.15.3 BUG-N：编辑笔记恒 405（store 层也缺能力）'
const to = '### 4.15.3 BUG-N：`PUT /api/notes/:id` 恒 405（API 契约不匹配，非当前 UI 故障）'
if (s.includes(from)) {
  s = s.replace(from, to)
  writeFileSync(P, s)
  console.log('标题已修正')
} else {
  console.log('MISS 标题')
  // 兜底：把含 4.15.3 的标题行原样打出来，便于人工核对
  for (const line of s.split('\n')) {
    if (line.includes('4.15.3')) console.log('实际标题:', JSON.stringify(line))
  }
}
