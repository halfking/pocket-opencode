/**
 * add-streak-locale.mjs —— 给「连续学习天数」文案补齐 9 个语言包。
 *
 * 用法：node scripts/add-streak-locale.mjs
 *
 * 与 add-learning-locale.mjs 同款做法：以 en-US 的结构为准逐语言写入，
 * 避免漏一个语言就被 report-locale-gaps.mjs 报出来。
 * 幂等：已存在且同值的键不写；已存在但值不同则保持原样并打印警告。
 */
import { readFileSync, writeFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const TRANSLATIONS = {
  'en-US': {
    streak: {
      days: 'day streak',
      toNext: '{count} more to {next}',
      milestoneReached: '{days}-day milestone reached',
    },
  },
  'zh-CN': {
    streak: {
      days: '天连续',
      toNext: '再有 {count} 天到 {next} 天',
      milestoneReached: '已达成 {days} 天里程碑',
    },
  },
  'zh-TW': {
    streak: {
      days: '天連續',
      toNext: '再有 {count} 天到 {next} 天',
      milestoneReached: '已達成 {days} 天里程碑',
    },
  },
  'ja-JP': {
    streak: {
      days: '日連続',
      toNext: 'あと {count} 日で {next} 日',
      milestoneReached: '{days} 日マイルストーン達成',
    },
  },
  'ko-KR': {
    streak: {
      days: '일 연속',
      toNext: '{next} 일까지 {count} 일 더',
      milestoneReached: '{days}일 이정표 도달',
    },
  },
  'de-DE': {
    streak: {
      days: 'Tage in Folge',
      toNext: 'Noch {count} bis {next}',
      milestoneReached: '{days}-Tage-Meilenstein erreicht',
    },
  },
  'fr-FR': {
    streak: {
      days: 'jours d’affilée',
      toNext: 'Encore {count} pour {next}',
      milestoneReached: 'Jalon de {days} jours atteint',
    },
  },
  'es-ES': {
    streak: {
      days: 'días seguidos',
      toNext: '{count} más para {next}',
      milestoneReached: 'Hito de {days} días alcanzado',
    },
  },
  'pt-BR': {
    streak: {
      days: 'dias seguidos',
      toNext: 'Mais {count} para {next}',
      milestoneReached: 'Marco de {days} dias alcançado',
    },
  },
}

function isPlainObject(v) {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

let changed = 0
for (const file of readdirSync(DIR).filter((f) => f.endsWith('.json'))) {
  const loc = file.replace(/\.json$/, '')
  const tr = TRANSLATIONS[loc]
  if (!tr) {
    console.warn(`[streak] no translation for ${loc}, skipped`)
    continue
  }
  const path = join(DIR, file)
  const json = JSON.parse(readFileSync(path, 'utf8'))
  if (!isPlainObject(json.study)) json.study = {}
  let dirty = false
  for (const [section, entries] of Object.entries(tr)) {
    if (!isPlainObject(json.study[section])) json.study[section] = {}
    for (const [k, v] of Object.entries(entries)) {
      if (json.study[section][k] === undefined) {
        json.study[section][k] = v
        dirty = true
      } else if (json.study[section][k] !== v) {
        console.warn(`[streak] ${loc}: study.${section}.${k} already set, kept`)
      }
    }
  }
  if (dirty) {
    writeFileSync(path, JSON.stringify(json, null, 2) + '\n', 'utf8')
    console.log(`[streak] updated ${file}`)
    changed++
  } else {
    console.log(`[streak] ${file} already up to date`)
  }
}
console.log(`[streak] done, ${changed} file(s) changed`)
