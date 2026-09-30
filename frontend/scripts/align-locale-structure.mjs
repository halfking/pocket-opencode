/** 按 en-US 权威结构对齐各语言包：把放错段的 key 搬回正确段。 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')
const L = ['zh-CN', 'zh-TW', 'ja-JP', 'ko-KR', 'de-DE', 'fr-FR', 'es-ES', 'pt-BR']

function leaf(o, p = '', out = {}) {
  for (const [k, v] of Object.entries(o || {})) {
    const q = p ? `${p}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) leaf(v, q, out)
    else out[q] = v
  }
  return out
}

const en = leaf(JSON.parse(fs.readFileSync(path.join(DIR, 'en-US.json'), 'utf8')))

for (const loc of L) {
  const file = path.join(DIR, `${loc}.json`)
  const json = JSON.parse(fs.readFileSync(file, 'utf8'))
  let moved = 0
  for (const seg of ['routes', 'settings']) {
    const box = json[seg]
    if (!box) continue
    for (const k of Object.keys(box)) {
      if (en[`${seg}.${k}`] === undefined) {
        // 放错段：搬到 en-US 实际所在的段
        let placed = false
        for (const dest of ['routes', 'settings']) {
          if (en[`${dest}.${k}`] !== undefined) {
            json[dest] = json[dest] || {}
            json[dest][k] = box[k]
            delete box[k]
            moved++
            placed = true
            break
          }
        }
        if (!placed) console.log(`  ${loc}: 孤儿 key ${seg}.${k}（en-US 也没有，已删除）`)
      }
    }
  }
  // 孤儿清理后若段空了直接删掉
  for (const seg of ['routes', 'settings']) {
    if (json[seg] && Object.keys(json[seg]).length === 0) delete json[seg]
  }
  fs.writeFileSync(file, JSON.stringify(json, null, 2) + '\n')
  const p = leaf(json)
  const missing = Object.keys(en).filter((k) => p[k] === undefined)
  console.log(
    `${loc.padEnd(6)} moved=${moved}  routes=${Object.keys(json.routes || {}).length} settings=${Object.keys(json.settings || {}).length} missing=${missing.length}`,
  )
  if (missing.length) console.log('   仍缺:', missing.join(', '))
}
