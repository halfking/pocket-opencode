/**
 * add-learning-locale.mjs —— 给 Learning Core 的界面文案补齐 9 个语言包。
 *
 * 用法：node scripts/add-learning-locale.mjs
 *
 * 为什么不手改 9 个 JSON：
 *  - 漏一个语言就会被 report-locale-gaps.mjs 报出来，而 build:gate 依赖它；
 *  - 这里用「以 en-US 为准、其余语言给译文」的方式写入，结构由 base 决定，
 *    不存在某个语言多出/少一个键的可能。
 *
 * 幂等：已存在且同值的键不写；已存在但值不同的键保持原样并打印警告
 * （避免脚本悄悄覆盖人工翻译）。
 */
import { readFileSync, writeFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

/** 9 个语言的学习域文案。键路径与 en-US 结构一致。 */
const TRANSLATIONS = {
  'en-US': {
    due: {
      title: "Today's review",
      cardsDue: 'Cards due',
      inboxWaiting: 'To process',
      reviewing: 'In review',
      tasksDue: 'Tasks due',
      allClear: 'Nothing due today',
    },
    reminder: {
      title: 'Daily digest',
      next: 'Next: {time}',
      offline: 'Daily review needs the learning service. Reconnect to enable reminders.',
    },
    inbox: {
      title: 'Inbox',
      empty: 'Nothing collected yet. Use "Add to learning" on a note, email or RSS item.',
      advance: 'Start studying',
    },
    source: {
      note: 'Note',
      email: 'Email',
      rss: 'RSS',
      meeting: 'Meeting',
      chat: 'Chat',
      manual: 'Manual',
    },
  },
  'zh-CN': {
    due: {
      title: '今日回顾',
      cardsDue: '卡片到期',
      inboxWaiting: '待处理',
      reviewing: '复习中',
      tasksDue: '任务到期',
      allClear: '今天没有待学内容',
    },
    reminder: {
      title: '每日回顾提醒',
      next: '下次：{time}',
      offline: '每日回顾需要学习服务，重新连接后可开启提醒。',
    },
    inbox: {
      title: '学习收件箱',
      empty: '还没有收集材料。在笔记、邮件或 RSS 里选择「加入学习」即可。',
      advance: '开始学习',
    },
    source: {
      note: '笔记',
      email: '邮件',
      rss: 'RSS',
      meeting: '会议',
      chat: '对话',
      manual: '手工',
    },
  },
  'zh-TW': {
    due: {
      title: '今日回顧',
      cardsDue: '卡片到期',
      inboxWaiting: '待處理',
      reviewing: '複習中',
      tasksDue: '任務到期',
      allClear: '今天沒有待學內容',
    },
    reminder: {
      title: '每日回顧提醒',
      next: '下次：{time}',
      offline: '每日回顧需要學習服務，重新連線後可開啟提醒。',
    },
    inbox: {
      title: '學習收件匣',
      empty: '尚未收集材料。在筆記、郵件或 RSS 中選擇「加入學習」即可。',
      advance: '開始學習',
    },
    source: {
      note: '筆記',
      email: '郵件',
      rss: 'RSS',
      meeting: '會議',
      chat: '對話',
      manual: '手動',
    },
  },
  'ja-JP': {
    due: {
      title: '今日の復習',
      cardsDue: 'カードの期限',
      inboxWaiting: '未処理',
      reviewing: '復習中',
      tasksDue: 'タスクの期限',
      allClear: '今日はやることがありません',
    },
    reminder: {
      title: '毎日のダイジェスト',
      next: '次回：{time}',
      offline: '毎日の復習には学習サービスが必要です。再接続すると通知を有効にできます。',
    },
    inbox: {
      title: '受信トレイ',
      empty: 'まだ素材がありません。ノート・メール・RSS で「学習に追加」を使ってください。',
      advance: '学習を開始',
    },
    source: {
      note: 'ノート',
      email: 'メール',
      rss: 'RSS',
      meeting: '会議',
      chat: 'チャット',
      manual: '手動',
    },
  },
  'ko-KR': {
    due: {
      title: '오늘의 복습',
      cardsDue: '카드 마감',
      inboxWaiting: '처리 대기',
      reviewing: '복습 중',
      tasksDue: '할 일 마감',
      allClear: '오늘은 할 일이 없습니다',
    },
    reminder: {
      title: '매일 요약',
      next: '다음: {time}',
      offline: '매일 복습에는 학습 서비스가 필요합니다. 다시 연결하면 알림을 켤 수 있습니다.',
    },
    inbox: {
      title: '수신함',
      empty: '아직 수집한 자료가 없습니다. 노트·메일·RSS에서 "학습에 추가"를 사용하세요.',
      advance: '학습 시작',
    },
    source: {
      note: '노트',
      email: '메일',
      rss: 'RSS',
      meeting: '회의',
      chat: '채팅',
      manual: '수동',
    },
  },
  'fr-FR': {
    due: {
      title: "Révision du jour",
      cardsDue: 'Cartes dues',
      inboxWaiting: 'À traiter',
      reviewing: 'En révision',
      tasksDue: 'Tâches dues',
      allClear: "Rien à réviser aujourd'hui",
    },
    reminder: {
      title: 'Résumé quotidien',
      next: 'Prochain : {time}',
      offline: "Le résumé quotidien nécessite le service d'apprentissage. Reconnectez-vous pour activer les rappels.",
    },
    inbox: {
      title: 'Boîte de réception',
      empty: "Rien de collecté pour l'instant. Utilisez « Ajouter à l'apprentissage » sur une note, un e-mail ou un flux RSS.",
      advance: 'Commencer',
    },
    source: {
      note: 'Note',
      email: 'E-mail',
      rss: 'RSS',
      meeting: 'Réunion',
      chat: 'Discussion',
      manual: 'Manuel',
    },
  },
  'de-DE': {
    due: {
      title: 'Heutige Wiederholung',
      cardsDue: 'Karten fällig',
      inboxWaiting: 'Zu bearbeiten',
      reviewing: 'In Wiederholung',
      tasksDue: 'Aufgaben fällig',
      allClear: 'Heute nichts fällig',
    },
    reminder: {
      title: 'Tägliche Zusammenfassung',
      next: 'Nächstes: {time}',
      offline: 'Die tägliche Wiederholung benötigt den Lerndienst. Nach dem Verbinden lassen sich Erinnerungen aktivieren.',
    },
    inbox: {
      title: 'Posteingang',
      empty: 'Noch nichts gesammelt. Nutze „Zum Lernen hinzufügen“ bei Notiz, E-Mail oder RSS.',
      advance: 'Lernen starten',
    },
    source: {
      note: 'Notiz',
      email: 'E-Mail',
      rss: 'RSS',
      meeting: 'Meeting',
      chat: 'Chat',
      manual: 'Manuell',
    },
  },
  'es-ES': {
    due: {
      title: 'Repaso de hoy',
      cardsDue: 'Tarjetas pendientes',
      inboxWaiting: 'Por procesar',
      reviewing: 'En repaso',
      tasksDue: 'Tareas pendientes',
      allClear: 'Nada pendiente hoy',
    },
    reminder: {
      title: 'Resumen diario',
      next: 'Siguiente: {time}',
      offline: 'El resumen diario necesita el servicio de aprendizaje. Vuelve a conectar para activar los recordatorios.',
    },
    inbox: {
      title: 'Bandeja de entrada',
      empty: 'Aún no hay material. Usa «Añadir al aprendizaje» en una nota, correo o RSS.',
      advance: 'Empezar',
    },
    source: {
      note: 'Nota',
      email: 'Correo',
      rss: 'RSS',
      meeting: 'Reunión',
      chat: 'Chat',
      manual: 'Manual',
    },
  },
  'pt-BR': {
    due: {
      title: 'Revisão de hoje',
      cardsDue: 'Cartões pendentes',
      inboxWaiting: 'A processar',
      reviewing: 'Em revisão',
      tasksDue: 'Tarefas pendentes',
      allClear: 'Nada pendente hoje',
    },
    reminder: {
      title: 'Resumo diário',
      next: 'Próximo: {time}',
      offline: 'O resumo diário precisa do serviço de aprendizado. Reconecte para ativar os lembretes.',
    },
    inbox: {
      title: 'Caixa de entrada',
      empty: 'Nada coletado ainda. Use "Adicionar ao aprendizado" em uma nota, e-mail ou RSS.',
      advance: 'Começar',
    },
    source: {
      note: 'Nota',
      email: 'E-mail',
      rss: 'RSS',
      meeting: 'Reunião',
      chat: 'Chat',
      manual: 'Manual',
    },
  },
}

function deepMerge(base, patch, path, warnings) {
  const out = Array.isArray(base) ? [...base] : { ...(base ?? {}) }
  for (const [k, v] of Object.entries(patch)) {
    const p = path ? `${path}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      out[k] = deepMerge(out[k], v, p, warnings)
    } else if (out[k] === undefined) {
      out[k] = v
    } else if (out[k] !== v && path) {
      warnings.push(`${p}: kept existing value (${JSON.stringify(out[k])})`)
    }
  }
  return out
}

const base = JSON.parse(readFileSync(join(DIR, 'en-US.json'), 'utf8'))
const warnings = []
const baseWithLearning = deepMerge(base, TRANSLATIONS['en-US'], 'study', warnings)
writeFileSync(join(DIR, 'en-US.json'), JSON.stringify(baseWithLearning, null, 2) + '\n', 'utf8')
console.log('en-US.json updated')

for (const file of readdirSync(DIR).filter((f) => f.endsWith('.json') && f !== 'en-US.json')) {
  const locale = file.replace('.json', '')
  const tr = TRANSLATIONS[locale]
  if (!tr) {
    console.log(`${locale.padEnd(8)} SKIP (no translation for this locale)`)
    continue
  }
  const json = JSON.parse(readFileSync(join(DIR, file), 'utf8'))
  const merged = deepMerge(json, tr, 'study', warnings)
  writeFileSync(join(DIR, file), JSON.stringify(merged, null, 2) + '\n', 'utf8')
  console.log(`${locale.padEnd(8)} updated`)
}

if (warnings.length) {
  console.log('\nkept existing values:')
  for (const w of warnings) console.log('  ' + w)
}
