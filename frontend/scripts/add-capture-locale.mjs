/**
 * add-capture-locale.mjs —— 给「一键加入学习 / 转为任务」按钮补齐 9 个语言包。
 *
 * 用法：node scripts/add-capture-locale.mjs
 *
 * 与 add-learning-locale.mjs 同样的原则：以 en-US 的结构为准写入，
 * 已存在且同值的键不写、值不同的保留人工译文并打印警告（幂等、不覆盖）。
 */
import { readFileSync, writeFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const CAPTURE = {
  'en-US': {
    add: 'Add to learning',
    done: 'Added to learning',
    toTask: 'Convert to task',
    taskDone: 'Task created',
    failed: 'Could not save. Try again.',
  },
  'zh-CN': {
    add: '加入学习',
    done: '已加入学习',
    toTask: '转为任务',
    taskDone: '已创建任务',
    failed: '保存失败，请重试',
  },
  'zh-TW': {
    add: '加入學習',
    done: '已加入學習',
    toTask: '轉為任務',
    taskDone: '已建立任務',
    failed: '儲存失敗，請重試',
  },
  'ja-JP': {
    add: '学習に追加',
    done: '学習に追加済み',
    toTask: 'タスクに変換',
    taskDone: 'タスクを作成しました',
    failed: '保存できませんでした。再試行してください。',
  },
  'ko-KR': {
    add: '학습에 추가',
    done: '학습에 추가됨',
    toTask: '할 일로 변환',
    taskDone: '할 일을 만들었습니다',
    failed: '저장하지 못했습니다. 다시 시도하세요.',
  },
  'fr-FR': {
    add: "Ajouter à l'apprentissage",
    done: 'Ajouté aux apprentissages',
    toTask: 'Convertir en tâche',
    taskDone: 'Tâche créée',
    failed: 'Enregistrement impossible. Réessayez.',
  },
  'de-DE': {
    add: 'Zum Lernen hinzufügen',
    done: 'Zum Lernen hinzugefügt',
    toTask: 'In Aufgabe umwandeln',
    taskDone: 'Aufgabe erstellt',
    failed: 'Speichern fehlgeschlagen. Erneut versuchen.',
  },
  'es-ES': {
    add: 'Añadir al aprendizaje',
    done: 'Añadido al aprendizaje',
    toTask: 'Convertir en tarea',
    taskDone: 'Tarea creada',
    failed: 'No se pudo guardar. Inténtalo de nuevo.',
  },
  'pt-BR': {
    add: 'Adicionar ao aprendizado',
    done: 'Adicionado ao aprendizado',
    toTask: 'Converter em tarefa',
    taskDone: 'Tarefa criada',
    failed: 'Não foi possível salvar. Tente novamente.',
  },
}

const warnings = []

for (const file of readdirSync(DIR).filter((f) => f.endsWith('.json'))) {
  const locale = file.replace('.json', '')
  const tr = CAPTURE[locale]
  if (!tr) {
    console.log(`${locale.padEnd(8)} SKIP (no translation for this locale)`)
    continue
  }
  const path = join(DIR, file)
  const json = JSON.parse(readFileSync(path, 'utf8'))
  if (!json.study) json.study = {}
  const existing = json.study.capture ?? {}
  for (const [k, v] of Object.entries(tr)) {
    if (existing[k] !== undefined && existing[k] !== v) {
      warnings.push(`${locale} study.capture.${k}: kept existing value`)
      continue
    }
    existing[k] = v
  }
  json.study.capture = existing
  writeFileSync(path, JSON.stringify(json, null, 2) + '\n', 'utf8')
  console.log(`${locale.padEnd(8)} updated`)
}

if (warnings.length) {
  console.log('\nkept existing values:')
  for (const w of warnings) console.log('  ' + w)
}
