/**
 * stage-i18n-nav-flashcards.mjs — 把 BUG-P 需要的 `nav.flashcards` 键精确写入索引。
 *
 * 与 stage-i18n-bugk.mjs 同一套约束与做法（HEAD 版本 → parse → 改 → stringify
 * → hash-object -w → update-index），不碰工作区、不 stash、不夹带并发会话在
 * locales/*.json 里的未提交改动。
 *
 * 只加**一个**键，不改任何已有文案。
 */
import { execFileSync } from 'node:child_process'

const LOCALES_DIR = 'frontend/src/locales'
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })

/** 闪卡在各语言下的名称。图标是 Material Symbols 的 style，语义一致，无需按语言区分。 */
const LABEL = {
  'zh-CN': '闪卡',
  'en-US': 'Flashcards',
  'ja-JP': '単語カード',
  'ko-KR': '플래시카드',
  'fr-FR': 'Cartes mémoire',
  'de-DE': 'Lernkarten',
  'es-ES': 'Tarjetas',
  'pt-BR': 'Cartões',
  'zh-TW': '閃卡',
}

let staged = 0
const report = []

for (const [locale, label] of Object.entries(LABEL)) {
  const path = `${LOCALES_DIR}/${locale}.json`
  const before = git('show', `HEAD:${path}`)
  const obj = JSON.parse(before)
  if (!obj?.nav) {
    report.push(`${locale}: 缺少 nav 块，跳过`)
    continue
  }
  if (obj.nav.flashcards) {
    report.push(`${locale}: nav.flashcards 已存在（值="${obj.nav.flashcards}"），不覆盖`)
    continue
  }
  obj.nav.flashcards = label

  const body = JSON.stringify(obj, null, 2)
  const text = before.endsWith('\n') ? body + '\n' : body
  try {
    JSON.parse(text)
  } catch (e) {
    report.push(`${locale}: 序列化后 JSON 非法，已放弃：${e.message}`)
    continue
  }

  const hash = execFileSync('git', ['hash-object', '-w', '--stdin'], { input: text, encoding: 'utf8' }).trim()
  git('update-index', '--cacheinfo', `100644,${hash},${path}`)
  staged++
  report.push(`${locale}: nav.flashcards="${label}"  ${hash.slice(0, 10)}`)
}

console.log(report.join('\n'))
console.log(`\nstaged ${staged} locale file(s)`)
