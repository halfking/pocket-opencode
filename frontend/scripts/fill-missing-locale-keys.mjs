#!/usr/bin/env node
/**
 * 语言包结构对齐（2026-09-30 真机审计）。
 *
 * 现状：en-US / zh-CN 各 292 个 key，其余 7 个语言（de/es/fr/ja/ko/pt/zh-TW）
 * 各只有 198 个 —— **各缺 94 个**，缺的是 flashcards 整模块、nav.home、
 * nav.study、routes.notifications 等新增文案。
 *
 * 根因是本地化流程只补了中英两种：新增 i18n key 时 zh-CN/en-US 更新了，
 * 其余语言没跟上。后果是这些语言的用户在页头和卡片模块里
 * 直接看到 "flashcards.edit.template" 这样的原始 key 名。
 *
 * 这里以 en-US 为基准补齐缺失的 key。值取 en-US 原文而不是伪造翻译：
 *   1. 与 i18n 的 fallbackLocale='en-US' 行为一致，运行时表现可预期；
 *   2. 不用机器翻译或简繁互转去污染语言标注（繁体包里放简体、
 *      德语包里放中文，比显示英文更糟）。
 * 补齐后这些 key 处于「结构完整、待人工翻译」状态，
 * 后续翻译直接覆盖同名 key 即可。
 *
 * Run: node scripts/fill-missing-locale-keys.mjs
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')
const BASE = 'en-US'

/** 扁平化成 'a.b.c' → value */
function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out[key] = v
  }
  return out
}

/** 把 'a.b.c' 写回嵌套对象 */
function setPath(target, dotted, value) {
  const parts = dotted.split('.')
  let node = target
  for (let i = 0; i < parts.length - 1; i++) {
    const p = parts[i]
    if (!node[p] || typeof node[p] !== 'object') node[p] = {}
    node = node[p]
  }
  node[parts[parts.length - 1]] = value
}

/** 依 en-US 的键顺序重建，保证各语言包结构与基准完全一致 */
function rebuild(target, baseFlat) {
  const targetFlat = flatten(target)
  const out = {}
  for (const key of Object.keys(baseFlat)) {
    setPath(out, key, key in targetFlat ? targetFlat[key] : baseFlat[key])
  }
  return out
}

const baseFlat = flatten(JSON.parse(fs.readFileSync(path.join(DIR, `${BASE}.json`), 'utf8')))
let changed = 0

for (const file of fs.readdirSync(DIR).filter((n) => n.endsWith('.json'))) {
  if (file === `${BASE}.json`) continue
  const full = path.join(DIR, file)
  const before = flatten(JSON.parse(fs.readFileSync(full, 'utf8')))
  const missing = Object.keys(baseFlat).filter((k) => !(k in before))
  const rebuilt = rebuild(JSON.parse(fs.readFileSync(full, 'utf8')), baseFlat)
  fs.writeFileSync(full, JSON.stringify(rebuilt, null, 2) + '\n')
  changed++
  console.log(
    `${file.padEnd(12)} 补齐 ${String(missing.length).padStart(3)} 个 key  →  ${Object.keys(flatten(rebuilt)).length}`,
  )
}
console.log(`\n已处理 ${changed} 个语言包，基准 ${BASE} 共 ${Object.keys(baseFlat).length} 个 key。`)
console.log('提示：补齐值为 en-US 原文，属「待人工翻译」状态，不影响结构完整性与 fallback 行为。')
