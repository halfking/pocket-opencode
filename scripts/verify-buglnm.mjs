#!/usr/bin/env node
/**
 * verify-buglnm.mjs — BUG-L / BUG-M / BUG-N 的真后端端到端验证（2026-09-30）。
 *
 * 三个缺陷都由 scripts/probe-write-methods.mjs 的 method 级探测发现，
 * 静态读代码看不出来，因为它们的共同点是「路由前缀注册了、handler 却按
 * method 拒绝」或「handler 压根没有这个分支」。
 *
 * 判据不只看状态码：
 *   BUG-L  POST /api/flashcards/notes  必须 201，且 PG 里有 note + card 两行；
 *   BUG-N  PUT  /api/notes/:id          必须 200，且**回读的正文是改后的**，
 *          列表摘要 snippet 也必须同步（不同步的话"标题还在列表"这类断言
 *          会恒真，掩盖正文根本没存上）；
 *   BUG-M  review 空 body 必须 400 而不是 500。
 *
 * 用法：node scripts/verify-buglnm.mjs
 * 退出码：0 全过；1 有失败。
 */
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASE = process.env.POCKET_BASE || 'http://localhost:8088'
const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const pass = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]

const results = []
const check = (name, ok, detail) => {
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  — ${detail}` : ''}`)
}

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: pass }),
})
if (!login.ok) {
  console.error('LOGIN_FAIL', login.status)
  process.exit(1)
}
const { token } = await login.json()
const H = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }

const stamp = Date.now()
const api = async (method, path, body) => {
  const res = await fetch(BASE + path, {
    method,
    headers: H,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await res.text()
  let json = null
  try { json = JSON.parse(text) } catch { /* 非 JSON 保留 text */ }
  return { status: res.status, json, text }
}

// ---------------------------------------------------------------- BUG-L
// 建卡组 -> 建卡片。修前 POST /api/flashcards/notes 恒 405 "GET only"。
const deckName = `audit-deck-${stamp}`
const deckRes = await api('POST', '/api/flashcards/decks', { name: deckName })
check('BUG-K 前置：POST /api/flashcards/decks', deckRes.status === 201,
  `${deckRes.status} ${(deckRes.json?.deckId || deckRes.json?.id || deckRes.text).slice(0, 60)}`)
const deckId = deckRes.json?.deckId || deckRes.json?.id

const front = `front-${stamp}`
const noteRes = await api('POST', '/api/flashcards/notes', {
  deckId, front, back: 'back-body', tags: ['audit'],
})
check('BUG-L POST /api/flashcards/notes 不再 405', noteRes.status === 201,
  `${noteRes.status} ${noteRes.text.slice(0, 80)}`)

// 回读：note 必须真的在库里，且 front 对得上。
if (noteRes.status === 201) {
  const list = await api('GET', '/api/flashcards/notes?since=0')
  const notes = list.json?.notes || []
  const mine = notes.find((n) => n.front === front)
  check('BUG-L 回读：note 已落库且 front 一致', !!mine,
    mine ? `noteId=${mine.id} deckId=${mine.deckId}` : `列表 ${notes.length} 条中未找到 ${front}`)

  const all = await api('GET', '/api/flashcards?since=0')
  const cards = all.json?.cards || []
  const card = mine ? cards.find((c) => c.noteId === mine.id) : null
  check('BUG-L 回读：初始 card 已生成', !!card,
    card ? `cardId=${card.id} state=${card.state}` : `cards ${cards.length} 条中未找到 noteId=${mine?.id}`)
}

// ---------------------------------------------------------------- BUG-M
const anyCard = (await api('GET', '/api/flashcards?since=0')).json?.cards?.[0]
if (anyCard) {
  const rev = await api('POST', `/api/flashcards/cards/${anyCard.id}/review`, {})
  check('BUG-M 空 body review 返回 400 而非 500', rev.status === 400,
    `${rev.status} ${rev.text.slice(0, 60)}`)

  const bad = await api('POST', `/api/flashcards/cards/${anyCard.id}/review`, { rating: 9 })
  check('BUG-M rating=9 返回 400', bad.status === 400, `${bad.status} ${bad.text.slice(0, 60)}`)
} else {
  check('BUG-M 前提：至少有一张 card', false, 'GET /api/flashcards 无卡片，跳过 review 断言')
}

// ---------------------------------------------------------------- BUG-N
const oldTitle = `audit-note-old-${stamp}`
const newTitle = `audit-note-new-${stamp}`
const newBody = `EDITED-BODY-${stamp}`

const create = await api('POST', '/api/notes', { title: oldTitle, content: 'original body' })
check('BUG-N 前置：POST /api/notes', create.status === 201 || create.status === 200,
  `${create.status} ${create.text.slice(0, 60)}`)
const noteId = create.json?.note?.id || create.json?.id

if (noteId) {
  const upd = await api('PUT', `/api/notes/${noteId}`, { title: newTitle, content: newBody })
  check('BUG-N PUT /api/notes/:id 不再 405', upd.status === 200,
    `${upd.status} ${upd.text.slice(0, 100)}`)

  // 强断言：回读必须看见**新正文**，而不是只看标题还在。
  const back = await api('GET', `/api/notes/${noteId}`)
  const bTitle = back.json?.title ?? ''
  const bSnippet = back.json?.snippet ?? ''
  check('BUG-N 回读：标题已更新', bTitle === newTitle, `title=${JSON.stringify(bTitle)}`)
  check('BUG-N 回读：正文/摘要包含新内容', bSnippet.includes(newBody),
    `snippet=${JSON.stringify(bSnippet.slice(0, 80))}`)

  // 列表摘要也必须反映新正文 —— 这条是"编辑真的生效"的独立旁证。
  const list = await api('GET', '/api/notes')
  const inList = (list.json?.notes || []).find((n) => n.id === noteId)
  check('BUG-N 列表：摘要已同步为新正文',
    !!inList && String(inList.snippet ?? '').includes(newBody),
    inList ? `list snippet=${JSON.stringify(String(inList.snippet).slice(0, 80))}` : '列表中未找到该 note')

  const del = await api('DELETE', `/api/notes/${noteId}`)
  check('BUG-N 清理：DELETE 成功', del.status === 200, `${del.status}`)
} else {
  check('BUG-N 前提：笔记创建返回 id', false, create.text.slice(0, 80))
}

// 顺手清掉本轮建的卡组数据。
if (deckId) {
  // 后端暂无 DELETE /api/flashcards/decks/:id，这里只说明，不假装清理成功。
  console.log(`\nNOTE: 卡组 ${deckName} (${deckId}) 留在库里 —— 后端没有删除卡组的路由。`)
}

const failed = results.filter((r) => !r.ok)
console.log(`\n=== SUMMARY: ${results.length - failed.length} / ${results.length} pass ===`)
if (failed.length) {
  console.log('FAILED:')
  for (const f of failed) console.log(`  - ${f.name}: ${f.detail ?? ''}`)
  process.exit(1)
}
