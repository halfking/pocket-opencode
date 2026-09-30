/**
 * 补齐 zh-TW 缺失的两个卡组键（与既有繁体用词保持一致：卡組 / 新增）。
 * en-US 与 zh-CN 都有，zh-TW 没有 → 繁体用户会直接看到原始 key 名。
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')
const FILE = join(DIR, 'zh-TW.json')

const json = JSON.parse(readFileSync(FILE, 'utf8'))
json.flashcards.deck.create = '新增卡組'
json.flashcards.deck.createPlaceholder = '卡組名稱'

writeFileSync(FILE, JSON.stringify(json, null, 2) + '\n', 'utf8')
console.log('已补齐 zh-TW.flashcards.deck.create / createPlaceholder')
console.log(JSON.stringify(json.flashcards.deck, null, 2))
