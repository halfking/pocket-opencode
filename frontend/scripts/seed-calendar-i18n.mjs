#!/usr/bin/env node
/**
 * 一次性脚本：把日历的 i18n key 写进 9 份语言文件。
 *
 * 为什么不手改：9 份文件 × 30 个 key = 270 处手工编辑，漏一处就会被
 * check:i18n 卡住，而漏掉的那一处正是用户在另一种语言下看到的回显 key。
 * 一次性脚本写完即可删；它进仓的唯一理由是留下「这些 key 是怎么产生的」的线索。
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const dir = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

/** key 路径 → 各语言译文。顺序与 CalendarView/CalendarEventSheet 里的引用一致。 */
const KEYS = {
  'calendar.action.newEvent': {
    'zh-CN': '新建日程', 'zh-TW': '新增行程', 'en-US': 'New event', 'ja-JP': '予定を作成',
    'ko-KR': '일정 만들기', 'de-DE': 'Neuer Termin', 'fr-FR': 'Nouvel événement',
    'es-ES': 'Nuevo evento', 'pt-BR': 'Novo evento',
  },
  'calendar.action.editEvent': {
    'zh-CN': '编辑日程', 'zh-TW': '編輯行程', 'en-US': 'Edit event', 'ja-JP': '予定を編集',
    'ko-KR': '일정 편집', 'de-DE': 'Termin bearbeiten', 'fr-FR': 'Modifier l\'événement',
    'es-ES': 'Editar evento', 'pt-BR': 'Editar evento',
  },
  'calendar.action.deleteEvent': {
    'zh-CN': '删除日程', 'zh-TW': '刪除行程', 'en-US': 'Delete event', 'ja-JP': '予定を削除',
    'ko-KR': '일정 삭제', 'de-DE': 'Termin löschen', 'fr-FR': 'Supprimer l\'événement',
    'es-ES': 'Eliminar evento', 'pt-BR': 'Excluir evento',
  },
  'calendar.action.today': {
    'zh-CN': '今天', 'zh-TW': '今天', 'en-US': 'Today', 'ja-JP': '今日',
    'ko-KR': '오늘', 'de-DE': 'Heute', 'fr-FR': "Aujourd'hui",
    'es-ES': 'Hoy', 'pt-BR': 'Hoje',
  },
  'calendar.action.prevMonth': {
    'zh-CN': '上个月', 'zh-TW': '上個月', 'en-US': 'Previous month', 'ja-JP': '前の月',
    'ko-KR': '이전 달', 'de-DE': 'Vorheriger Monat', 'fr-FR': 'Mois précédent',
    'es-ES': 'Mes anterior', 'pt-BR': 'Mês anterior',
  },
  'calendar.action.nextMonth': {
    'zh-CN': '下个月', 'zh-TW': '下個月', 'en-US': 'Next month', 'ja-JP': '次の月',
    'ko-KR': '다음 달', 'de-DE': 'Nächster Monat', 'fr-FR': 'Mois suivant',
    'es-ES': 'Mes siguiente', 'pt-BR': 'Próximo mês',
  },
  'calendar.action.addOnDay': {
    'zh-CN': '在这天添加日程', 'zh-TW': '在這天新增行程', 'en-US': 'Add an event on this day',
    'ja-JP': 'この日に予定を追加', 'ko-KR': '이 날짜에 일정 추가',
    'de-DE': 'An diesem Tag einen Termin hinzufügen', 'fr-FR': 'Ajouter un événement ce jour',
    'es-ES': 'Añadir un evento ese día', 'pt-BR': 'Adicionar um evento neste dia',
  },
  'calendar.filter.label': {
    'zh-CN': '筛选来源', 'zh-TW': '篩選來源', 'en-US': 'Filter sources', 'ja-JP': 'ソースで絞り込み',
    'ko-KR': '출처 필터', 'de-DE': 'Quellen filtern', 'fr-FR': 'Filtrer les sources',
    'es-ES': 'Filtrar fuentes', 'pt-BR': 'Filtrar fontes',
  },
  'calendar.source.event': {
    'zh-CN': '日程', 'zh-TW': '行程', 'en-US': 'Events', 'ja-JP': '予定',
    'ko-KR': '일정', 'de-DE': 'Termine', 'fr-FR': 'Événements',
    'es-ES': 'Eventos', 'pt-BR': 'Eventos',
  },
  'calendar.source.task': {
    'zh-CN': '任务截止', 'zh-TW': '任務期限', 'en-US': 'Task due', 'ja-JP': 'タスク期限',
    'ko-KR': '작업 마감', 'de-DE': 'Aufgabenfällig', 'fr-FR': 'Échéance de tâche',
    'es-ES': 'Vencimiento de tarea', 'pt-BR': 'Prazo da tarefa',
  },
  'calendar.source.scheduled': {
    'zh-CN': '自动化', 'zh-TW': '自動化', 'en-US': 'Automations', 'ja-JP': '自動化',
    'ko-KR': '자동화', 'de-DE': 'Automationen', 'fr-FR': 'Automatisations',
    'es-ES': 'Automatizaciones', 'pt-BR': 'Automações',
  },
  'calendar.field.title': {
    'zh-CN': '标题', 'zh-TW': '標題', 'en-US': 'Title', 'ja-JP': 'タイトル',
    'ko-KR': '제목', 'de-DE': 'Titel', 'fr-FR': 'Titre',
    'es-ES': 'Título', 'pt-BR': 'Título',
  },
  'calendar.field.start': {
    'zh-CN': '开始时间', 'zh-TW': '開始時間', 'en-US': 'Start', 'ja-JP': '開始',
    'ko-KR': '시작', 'de-DE': 'Beginn', 'fr-FR': 'Début',
    'es-ES': 'Inicio', 'pt-BR': 'Início',
  },
  'calendar.field.end': {
    'zh-CN': '结束时间', 'zh-TW': '結束時間', 'en-US': 'End', 'ja-JP': '終了',
    'ko-KR': '종료', 'de-DE': 'Ende', 'fr-FR': 'Fin',
    'es-ES': 'Fin', 'pt-BR': 'Fim',
  },
  'calendar.field.allDay': {
    'zh-CN': '全天', 'zh-TW': '整天', 'en-US': 'All day', 'ja-JP': '終日',
    'ko-KR': '종일', 'de-DE': 'Ganztägig', 'fr-FR': 'Toute la journée',
    'es-ES': 'Todo el día', 'pt-BR': 'Dia inteiro',
  },
  'calendar.field.location': {
    'zh-CN': '地点', 'zh-TW': '地點', 'en-US': 'Location', 'ja-JP': '場所',
    'ko-KR': '장소', 'de-DE': 'Ort', 'fr-FR': 'Lieu',
    'es-ES': 'Ubicación', 'pt-BR': 'Local',
  },
  'calendar.field.note': {
    // 「Note」在法语与英语同形，会被 check:i18n-translated 当成未翻译（该门以
    // 「与 en-US 完全相同」为判据）。选一个真正不同的词：注释/备注。
    'zh-CN': '备注', 'zh-TW': '備註', 'en-US': 'Note', 'ja-JP': 'メモ',
    'ko-KR': '메모', 'de-DE': 'Notiz', 'fr-FR': 'Commentaire',
    'es-ES': 'Nota', 'pt-BR': 'Observação',
  },
  'calendar.placeholder.title': {
    'zh-CN': '例如：季度评审', 'zh-TW': '例如：季度檢討', 'en-US': 'e.g. Quarterly review',
    'ja-JP': '例：四半期レビュー', 'ko-KR': '예: 분기 검토',
    'de-DE': 'z. B. Quartalsreview', 'fr-FR': 'ex. Revue trimestrielle',
    'es-ES': 'p. ej. Revisión trimestral', 'pt-BR': 'ex. Revisão trimestral',
  },
  'calendar.label.allDay': {
    'zh-CN': '全天', 'zh-TW': '整天', 'en-US': 'All day', 'ja-JP': '終日',
    'ko-KR': '종일', 'de-DE': 'Ganztägig', 'fr-FR': 'Toute la journée',
    'es-ES': 'Todo el día', 'pt-BR': 'Dia inteiro',
  },
  'calendar.label.dueBy': {
    'zh-CN': '截止', 'zh-TW': '截止', 'en-US': 'Due', 'ja-JP': '期限',
    'ko-KR': '마감', 'de-DE': 'Fällig', 'fr-FR': 'Échéance',
    'es-ES': 'Vence', 'pt-BR': 'Prazo',
  },
  'calendar.badge.done': {
    'zh-CN': '已完成', 'zh-TW': '已完成', 'en-US': 'Done', 'ja-JP': '完了',
    'ko-KR': '완료', 'de-DE': 'Erledigt', 'fr-FR': 'Terminé',
    'es-ES': 'Hecho', 'pt-BR': 'Concluído',
  },
  'calendar.empty.day': {
    'zh-CN': '这一天还没有安排', 'zh-TW': '這一天還沒有安排', 'en-US': 'Nothing scheduled on this day',
    'ja-JP': 'この日の予定はありません', 'ko-KR': '이 날에는 일정이 없습니다',
    'de-DE': 'An diesem Tag ist nichts geplant', 'fr-FR': 'Rien de prévu ce jour-là',
    'es-ES': 'No hay nada programado ese día', 'pt-BR': 'Nada agendado neste dia',
  },
  'calendar.aria.entriesCount': {
    'zh-CN': '{n} 项安排', 'zh-TW': '{n} 項安排', 'en-US': '{n} items',
    'ja-JP': '{n} 件', 'ko-KR': '{n}건', 'de-DE': '{n} Einträge',
    'fr-FR': '{n} éléments', 'es-ES': '{n} elementos', 'pt-BR': '{n} itens',
  },
  'nav.calendar': {
    'zh-CN': '日历', 'zh-TW': '行事曆', 'en-US': 'Calendar', 'ja-JP': 'カレンダー',
    'ko-KR': '캘린더', 'de-DE': 'Kalender', 'fr-FR': 'Calendrier',
    'es-ES': 'Calendario', 'pt-BR': 'Calendário',
  },
  'errors.loadCalendarFailed': {
    'zh-CN': '加载日历失败', 'zh-TW': '載入行事曆失敗', 'en-US': 'Failed to load the calendar',
    'ja-JP': 'カレンダーの読み込みに失敗しました', 'ko-KR': '캘린더를 불러오지 못했습니다',
    'de-DE': 'Kalender konnte nicht geladen werden', 'fr-FR': 'Échec du chargement du calendrier',
    'es-ES': 'No se pudo cargar el calendario', 'pt-BR': 'Falha ao carregar o calendário',
  },
  'errors.saveCalendarFailed': {
    'zh-CN': '保存日程失败', 'zh-TW': '儲存行程失敗', 'en-US': 'Failed to save the event',
    'ja-JP': '予定の保存に失敗しました', 'ko-KR': '일정을 저장하지 못했습니다',
    'de-DE': 'Termin konnte nicht gespeichert werden', 'fr-FR': "Échec de l'enregistrement de l'événement",
    'es-ES': 'No se pudo guardar el evento', 'pt-BR': 'Falha ao salvar o evento',
  },

  // 日历并入「消息」tab 的分段控件文案。
  //
  // 只加 2 个 key，不是 3 个：「日历」那一档直接复用已有的 nav.calendar。
  // 同一个词在同一个界面出现两次就该共用一个 key —— 复制一份必然出现
  // 「改了 A 忘了 B」，而两处文案漂移在界面上是看不出来的。
  'messagesHub.view.label': {
    'zh-CN': '查看方式', 'zh-TW': '檢視方式', 'en-US': 'View',
    'ja-JP': '表示形式', 'ko-KR': '보기 방식',
    'de-DE': 'Ansicht', 'fr-FR': 'Affichage',
    'es-ES': 'Vista', 'pt-BR': 'Visualização',
  },
  'messagesHub.view.timeline': {
    'zh-CN': '时间线', 'zh-TW': '時間軸', 'en-US': 'Timeline',
    'ja-JP': 'タイムライン', 'ko-KR': '타임라인',
    'de-DE': 'Zeitleiste', 'fr-FR': 'Chronologie',
    'es-ES': 'Cronología', 'pt-BR': 'Linha do tempo',
  },
}

const LOCALES = ['zh-CN', 'zh-TW', 'en-US', 'ja-JP', 'ko-KR', 'de-DE', 'fr-FR', 'es-ES', 'pt-BR']

function setPath(obj, path, value) {
  const parts = path.split('.')
  let node = obj
  for (let i = 0; i < parts.length - 1; i += 1) {
    if (typeof node[parts[i]] !== 'object' || node[parts[i]] === null) node[parts[i]] = {}
    node = node[parts[i]]
  }
  node[parts[parts.length - 1]] = value
}

let missing = 0
for (const locale of LOCALES) {
  const file = join(dir, `${locale}.json`)
  const data = JSON.parse(readFileSync(file, 'utf8'))
  for (const [path, translations] of Object.entries(KEYS)) {
    const value = translations[locale]
    if (value === undefined) {
      console.error(`缺译文：${path} / ${locale}`)
      missing += 1
      continue
    }
    setPath(data, path, value)
  }
  writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`, 'utf8')
  console.log(`✓ ${locale}`)
}

if (missing > 0) {
  console.error(`\n${missing} 个 key 缺译文，未改动语言文件。`)
  process.exit(1)
}
console.log('\n全部写入完成。')