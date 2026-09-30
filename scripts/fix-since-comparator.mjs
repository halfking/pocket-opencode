// fix-since-comparator.mjs — 一次性脚本：把 flashcards store 的增量过滤
// 从 `updated_at > $2` / `deleted_at > $2` 改为 `>=`（BUG-O，2026-09-30）。
// 改完即可删除；保留在仓库里是为了让这次语义变更可追溯。
import { readFileSync, writeFileSync } from 'node:fs'

const p = 'backend/internal/flashcards/store.go'
let s = readFileSync(p, 'utf8')
const upd = 'updated_at > $2'
const del = 'deleted_at > $2'
const beforeU = s.split(upd).length - 1
const beforeD = s.split(del).length - 1
s = s.split(upd).join('updated_at >= $2')
s = s.split(del).join('deleted_at >= $2')
// 同步修正文档注释里的语义描述
s = s.replace(
  '// ListNotesSince returns notes with updated_at > sinceSec, capped at limit.',
  '// ListNotesSince returns notes with updated_at >= sinceSec, capped at limit.',
)
s = s.replace(
  '// ListDeletedNotesSince returns ids whose deleted_at > sinceSec, capped.',
  '// ListDeletedNotesSince returns ids whose deleted_at >= sinceSec, capped.',
)
writeFileSync(p, s)
console.log(`updated_at: ${beforeU}, deleted_at: ${beforeD}`)
