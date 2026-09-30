// BUG-AM 修复：study.reminder.* 三个 key 在代码里被使用，但 9 个语言文件里
// 整块 study.reminder 都不存在 -> vue-i18n 回退到 key 本身，
// 用户在 #/study 页面上直接看到 "study.reminder.title" / "study.reminder.offline"。
//
// 安全约束：先验证每个文件能 JSON.stringify(obj, null, 2) 原样往返，
// 往返不一致的文件**不写**，只在报告里列出——绝不能为了加 key 重排整个文件格式。
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const locDir = join(here, '..', 'frontend', 'src', 'locales')

const T = {
  'zh-CN': { title: '每日回顾提醒', next: '下次：{time}', offline: '学习服务未连接，提醒暂不可用' },
  'zh-TW': { title: '每日回顧提醒', next: '下次：{time}', offline: '學習服務未連接，提醒暫不可用' },
  'en-US': { title: 'Daily review reminder', next: 'Next: {time}', offline: 'Learning service is offline; reminders are unavailable for now' },
  'ja-JP': { title: '日次レビューのお知らせ', next: '次回：{time}', offline: '学習サービスに未接続のため、リマインダーは現在利用できません' },
  'ko-KR': { title: '일일 복습 알림', next: '다음: {time}', offline: '학습 서비스에 연결되지 않아 알림을 사용할 수 없습니다' },
  'de-DE': { title: 'Tägliche Wiederholungserinnerung', next: 'Nächste: {time}', offline: 'Lerndienst nicht verbunden – Erinnerungen derzeit nicht verfügbar' },
  'fr-FR': { title: 'Rappel de révision quotidienne', next: 'Prochain : {time}', offline: "Service d'apprentissage hors ligne : rappels indisponibles pour le moment" },
  'es-ES': { title: 'Recordatorio de repaso diario', next: 'Próximo: {time}', offline: 'Servicio de aprendizaje sin conexión: recordatorios no disponibles por ahora' },
  'pt-BR': { title: 'Lembrete de revisão diária', next: 'Próximo: {time}', offline: 'Serviço de aprendizagem off-line: lembretes indisponíveis no momento' },
}

const files = Object.keys(T)
const report = []
for (const loc of files) {
  const path = join(locDir, `${loc}.json`)
  const original = readFileSync(path, 'utf8')
  const obj = JSON.parse(original)

  // 往返校验：保证写回不会重排格式
  const roundTrip = JSON.stringify(obj, null, 2)
  const normOrig = original.replace(/\r\n/g, '\n').replace(/\n$/, '')
  if (roundTrip !== normOrig) {
    report.push({ loc, action: 'SKIPPED_FORMAT_MISMATCH', roundTripLen: roundTrip.length, origLen: normOrig.length })
    continue
  }

  const had = Object.prototype.hasOwnProperty.call(obj.study, 'reminder')
  // 插到 decks 之后，保持阅读顺序
  const study = {}
  for (const [k, v] of Object.entries(obj.study)) {
    study[k] = v
    if (k === 'decks') study.reminder = T[loc]
  }
  if (!had && !study.reminder) study.reminder = T[loc]
  obj.study = study

  writeFileSync(path, JSON.stringify(obj, null, 2) + '\n', 'utf8')
  report.push({ loc, action: had ? 'REPLACED' : 'ADDED', keys: Object.keys(T[loc]).length })
}
console.log(JSON.stringify(report, null, 1))
