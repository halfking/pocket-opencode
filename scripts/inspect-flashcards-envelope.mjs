// inspect-flashcards-envelope.mjs — 查 /api/flashcards 增量 envelope 的实际内容。
// 2026-09-30：真机 UI 建的卡片已确认落 PG（front=正面-233899，1 张 card），
// 但闪卡列表显示「0 cards」。要分清是 envelope 没带卡片，还是前端统计逻辑问题。
// 用法：node scripts/inspect-flashcards-envelope.mjs [since]
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASE = process.env.POCKET_BASE || 'http://localhost:8088'
const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const pass = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: pass }),
})
const { token } = await login.json()
const H = { Authorization: `Bearer ${token}` }

const since = process.argv[2] || '0'
for (const path of ['/api/flashcards?since=0&limit=200', '/api/flashcards/notes?since=0&limit=200']) {
  const res = await fetch(BASE + path, { headers: H })
  const body = await res.json()
  console.log(`\n=== ${path} -> ${res.status} ===`)
  for (const key of ['cards', 'decks', 'notes', 'serverTimeMs']) {
    const v = body[key]
    console.log(`  ${key}: ${Array.isArray(v) ? v.length + ' items' : JSON.stringify(v)}`)
  }
  if (Array.isArray(body.cards) && body.cards.length) {
    console.log('  card sample:', JSON.stringify(body.cards.slice(0, 3), null, 1).slice(0, 600))
    const byDeck = {}
    for (const c of body.cards) byDeck[c.deckId] = (byDeck[c.deckId] || 0) + 1
    console.log('  cards per deckId:', JSON.stringify(byDeck))
  }
  if (Array.isArray(body.decks) && body.decks.length) {
    console.log('  deck ids:', body.decks.map((d) => `${d.deckId || d.id}:${d.name}`).join(', '))
  }
  if (Array.isArray(body.notes) && body.notes.length) {
    console.log('  note sample:', JSON.stringify(body.notes.slice(0, 2)).slice(0, 300))
  }
}
