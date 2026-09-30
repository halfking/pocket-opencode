#!/usr/bin/env node
/**
 * 补齐 email.dailySummary。
 *
 * 背景：「每日摘要」是邮件自动归纳的核心能力，路由 /email/summary 与
 * EmailSummaryView 都在，但全站没有任何入口链接——用户从 UI 上根本触达不到，
 * 表现为「邮件管理没有自动归纳整理的能力」。现在把入口接到收件箱「更多」菜单。
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const ADDITIONS = {
  'zh-CN': { dailySummary: '每日摘要' },
  'zh-TW': { dailySummary: '每日摘要' },
  'en-US': { dailySummary: 'Daily digest' },
  'ja-JP': { dailySummary: 'デイリー要約' },
  'ko-KR': { dailySummary: '일일 요약' },
  'de-DE': { dailySummary: 'Tägliche Zusammenfassung' },
  'fr-FR': { dailySummary: 'Résumé quotidien' },
  'es-ES': { dailySummary: 'Resumen diario' },
  'pt-BR': { dailySummary: 'Resumo diário' },
}

for (const [locale, add] of Object.entries(ADDITIONS)) {
  const file = join(DIR, `${locale}.json`)
  const raw = readFileSync(file, 'utf8')
  const eol = raw.includes('\r\n') ? '\r\n' : '\n'
  const data = JSON.parse(raw)
  data.email = { ...(data.email || {}), ...add }
  writeFileSync(file, JSON.stringify(data, null, 2).replace(/\n/g, eol) + eol, 'utf8')
  console.log(`${locale}: email.dailySummary added`)
}
console.log('done')
