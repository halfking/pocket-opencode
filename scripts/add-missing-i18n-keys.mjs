// BUG-AN 修复：补齐「代码在用、但 9 个语言文件全缺」的 9 个 key。
// 由 audit-i18n-keys.mjs 全量对账得出，不是一个个撞出来的。
//   nav.flashcards        -> MoreHubView「更多」页的闪卡入口
//   study.due.*  (5)      -> StudyHubView「今日待办」四个计数卡的标题
//   study.inbox.* (3)     -> StudyHubView「收件箱」区块
//
// 安全约束同 add-study-reminder-i18n.mjs：写回前先验证 JSON.stringify(obj,null,2)
// 能与原文往返，不一致就跳过并报告，绝不为了加 key 重排整个文件格式。
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const locDir = join(here, '..', 'frontend', 'src', 'locales')

const T = {
  'zh-CN': {
    'nav.flashcards': '闪卡',
    'study.due.title': '今日待办',
    'study.due.cardsDue': '待复习卡片',
    'study.due.inboxWaiting': '收件箱待处理',
    'study.due.reviewing': '待回顾',
    'study.due.tasksDue': '待办任务',
    'study.inbox.title': '收件箱',
    'study.inbox.empty': '收件箱已清空',
    'study.inbox.advance': '推进处理',
  },
  'zh-TW': {
    'nav.flashcards': '閃卡',
    'study.due.title': '今日待辦',
    'study.due.cardsDue': '待複習卡片',
    'study.due.inboxWaiting': '收件匣待處理',
    'study.due.reviewing': '待回顧',
    'study.due.tasksDue': '待辦任務',
    'study.inbox.title': '收件匣',
    'study.inbox.empty': '收件匣已清空',
    'study.inbox.advance': '推進處理',
  },
  'en-US': {
    'nav.flashcards': 'Flashcards',
    'study.due.title': "Today's due",
    'study.due.cardsDue': 'Cards due',
    'study.due.inboxWaiting': 'Inbox waiting',
    'study.due.reviewing': 'To review',
    'study.due.tasksDue': 'Tasks due',
    'study.inbox.title': 'Inbox',
    'study.inbox.empty': 'Inbox is empty',
    'study.inbox.advance': 'Advance',
  },
  'ja-JP': {
    'nav.flashcards': 'フラッシュカード',
    'study.due.title': '本日の予定',
    'study.due.cardsDue': '復習待ちカード',
    'study.due.inboxWaiting': '受信トレイの未処理',
    'study.due.reviewing': 'レビュー待ち',
    'study.due.tasksDue': '期限近いタスク',
    'study.inbox.title': '受信トレイ',
    'study.inbox.empty': '受信トレイは空です',
    'study.inbox.advance': '次へ進める',
  },
  'ko-KR': {
    'nav.flashcards': '플래시카드',
    'study.due.title': '오늘의 할 일',
    'study.due.cardsDue': '복습 대기 카드',
    'study.due.inboxWaiting': '수신함 대기',
    'study.due.reviewing': '복습 대기',
    'study.due.tasksDue': '마감 임박 작업',
    'study.inbox.title': '수신함',
    'study.inbox.empty': '수신함이 비어 있습니다',
    'study.inbox.advance': '진행하기',
  },
  'de-DE': {
    'nav.flashcards': 'Karteikarten',
    'study.due.title': 'Heute fällig',
    'study.due.cardsDue': 'Karten fällig',
    'study.due.inboxWaiting': 'Posteingang wartet',
    'study.due.reviewing': 'Zu wiederholen',
    'study.due.tasksDue': 'Aufgaben fällig',
    'study.inbox.title': 'Posteingang',
    'study.inbox.empty': 'Posteingang ist leer',
    'study.inbox.advance': 'Fortfahren',
  },
  'fr-FR': {
    'nav.flashcards': 'Cartes mémoire',
    'study.due.title': "À faire aujourd'hui",
    'study.due.cardsDue': 'Cartes à réviser',
    'study.due.inboxWaiting': "Boîte de réception en attente",
    'study.due.reviewing': 'À réviser',
    'study.due.tasksDue': 'Tâches à faire',
    'study.inbox.title': 'Boîte de réception',
    'study.inbox.empty': 'La boîte de réception est vide',
    'study.inbox.advance': 'Traiter',
  },
  'es-ES': {
    'nav.flashcards': 'Tarjetas',
    'study.due.title': 'Pendientes de hoy',
    'study.due.cardsDue': 'Tarjetas pendientes',
    'study.due.inboxWaiting': 'Bandeja de entrada pendiente',
    'study.due.reviewing': 'Por repasar',
    'study.due.tasksDue': 'Tareas pendientes',
    'study.inbox.title': 'Bandeja de entrada',
    'study.inbox.empty': 'La bandeja de entrada está vacía',
    'study.inbox.advance': 'Procesar',
  },
  'pt-BR': {
    'nav.flashcards': 'Cartões de memorização',
    'study.due.title': 'Pendências de hoje',
    'study.due.cardsDue': 'Cartões a revisar',
    'study.due.inboxWaiting': 'Caixa de entrada pendente',
    'study.due.reviewing': 'A revisar',
    'study.due.tasksDue': 'Tarefas pendentes',
    'study.inbox.title': 'Caixa de entrada',
    'study.inbox.empty': 'A caixa de entrada está vazia',
    'study.inbox.advance': 'Processar',
  },
}

const report = []
for (const [loc, keys] of Object.entries(T)) {
  const path = join(locDir, `${loc}.json`)
  const original = readFileSync(path, 'utf8')
  const obj = JSON.parse(original)

  const roundTrip = JSON.stringify(obj, null, 2)
  const normOrig = original.replace(/\r\n/g, '\n').replace(/\n$/, '')
  if (roundTrip !== normOrig) {
    report.push({ loc, action: 'SKIPPED_FORMAT_MISMATCH' })
    continue
  }

  const setPath = (root, dotted, value) => {
    const parts = dotted.split('.')
    let cur = root
    for (let i = 0; i < parts.length - 1; i++) {
      if (!cur[parts[i]] || typeof cur[parts[i]] !== 'object') cur[parts[i]] = {}
      cur = cur[parts[i]]
    }
    cur[parts[parts.length - 1]] = value
  }

  const before = readFileSync(path, 'utf8')
  for (const [k, v] of Object.entries(keys)) setPath(obj, k, v)
  writeFileSync(path, JSON.stringify(obj, null, 2) + '\n', 'utf8')

  // 复核：写回后重新解析，确认每个 key 都能读到且值正确
  const verify = JSON.parse(readFileSync(path, 'utf8'))
  const readBack = (dotted) => dotted.split('.').reduce((a, p) => (a == null ? a : a[p]), verify)
  const bad = Object.keys(keys).filter((k) => readBack(k) !== keys[k])
  report.push({ loc, action: 'ADDED', n: Object.keys(keys).length, verifyFail: bad })
  void before
}
console.log(JSON.stringify(report, null, 1))
