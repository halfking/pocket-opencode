// 补 study.due.allClear（BUG-AN 续）。
// 这个 key 之所以漏掉：它不出现在 t('...') 调用里，而是作为
// DueSummaryHeadlineKey 联合类型的字面量 + 函数返回值，
// 界面通过 `t(dueSummaryHeadlineKey(due.value))` 动态取用。
// 前一版 audit-i18n-keys.mjs 只认静态 t('x.y')，因此整块漏报。
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const locDir = join(here, '..', 'frontend', 'src', 'locales')

const ALL_CLEAR = {
  'zh-CN': '今天没有待办',
  'zh-TW': '今天沒有待辦',
  'en-US': 'Nothing due today',
  'ja-JP': '本日の予定はありません',
  'ko-KR': '오늘 할 일이 없습니다',
  'de-DE': 'Heute nichts fällig',
  'fr-FR': "Rien à faire aujourd'hui",
  'es-ES': 'Nada pendiente hoy',
  'pt-BR': 'Nada pendente hoje',
}

const report = []
for (const [loc, val] of Object.entries(ALL_CLEAR)) {
  const path = join(locDir, `${loc}.json`)
  const original = readFileSync(path, 'utf8')
  const obj = JSON.parse(original)

  const roundTrip = JSON.stringify(obj, null, 2)
  const normOrig = original.replace(/\r\n/g, '\n').replace(/\n$/, '')
  if (roundTrip !== normOrig) { report.push({ loc, action: 'SKIPPED_FORMAT_MISMATCH' }); continue }

  if (!obj.study.due) obj.study.due = {}
  if (obj.study.due.allClear) { report.push({ loc, action: 'ALREADY_PRESENT' }); continue }
  obj.study.due.allClear = val
  writeFileSync(path, JSON.stringify(obj, null, 2) + '\n', 'utf8')

  const back = JSON.parse(readFileSync(path, 'utf8'))
  report.push({ loc, action: 'ADDED', allClear: back.study.due.allClear, ok: back.study.due.allClear === val })
}
console.log(JSON.stringify(report, null, 1))
