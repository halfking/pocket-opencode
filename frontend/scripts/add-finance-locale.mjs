#!/usr/bin/env node
/**
 * 为 settings 区块补齐 finance / financeDesc 两个 key（记账入口）。
 *
 * 背景：SettingsView.vue 的「记账」行是模板里写死的中文，切到英文语言包时
 * 同一页出现中英混排（真机实测）。en-US 为权威结构，补齐后由
 * locales/__tests__/locale-parity.test.mjs 守护 key 集合一致。
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const ADDITIONS = {
  'zh-CN': { finance: '记账', financeDesc: '手动 + 笔记自动入账 · 月度收支统计' },
  'zh-TW': { finance: '記帳', financeDesc: '手動 + 筆記自動入帳 · 月度收支統計' },
  'en-US': { finance: 'Bookkeeping', financeDesc: 'Manual + auto from notes · monthly income & expenses' },
  'ja-JP': { finance: '家計簿', financeDesc: '手動 + メモから自動記帳 · 月次の収支統計' },
  'ko-KR': { finance: '가계부', financeDesc: '수동 + 노트 자동 기장 · 월별 수입·지출 통계' },
  'de-DE': { finance: 'Buchhaltung', financeDesc: 'Manuell + automatisch aus Notizen · monatliche Einnahmen und Ausgaben' },
  'fr-FR': { finance: 'Comptabilité', financeDesc: 'Manuel + automatique depuis les notes ·收支 mensuels' },
  'es-ES': { finance: 'Contabilidad', financeDesc: 'Manual + automático desde notas · ingresos y gastos mensuales' },
  'pt-BR': { finance: 'Contabilidade', financeDesc: 'Manual + automático a partir de notas · receitas e despesas mensais' },
}

for (const [locale, add] of Object.entries(ADDITIONS)) {
  const file = join(DIR, `${locale}.json`)
  const raw = readFileSync(file, 'utf8')
  const eol = raw.includes('\r\n') ? '\r\n' : '\n'
  const data = JSON.parse(raw)
  data.settings = { ...data.settings, ...add }
  const next = JSON.stringify(data, null, 2).replace(/\n/g, eol) + eol
  writeFileSync(file, next, 'utf8')
  console.log(`${locale}: +${Object.keys(add).length} keys (eol=${eol === '\r\n' ? 'CRLF' : 'LF'})`)
}
console.log('done')
