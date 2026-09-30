/**
 * 补齐 6 个语言包缺失的 flashcards.deck.create / createPlaceholder。
 *
 * 背景：en-US / zh-CN / zh-TW 有这两个键，其余 6 个语言没有 →
 * 这些语言的卡组页会直接显示原始 key 名（locale-parity 测试会红）。
 * 译文沿用各语言包既有的「卡组」用词（Stapel / Mazo / Paquet / デッキ / 덱 / Baralho），
 * 不引入与该语言其他界面不一致的术语。
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const T = {
  'de-DE.json': { create: 'Neuer Stapel', createPlaceholder: 'Stapelname' },
  'es-ES.json': { create: 'Nuevo mazo', createPlaceholder: 'Nombre del mazo' },
  'fr-FR.json': { create: 'Nouveau paquet', createPlaceholder: 'Nom du paquet' },
  'ja-JP.json': { create: '新しいデッキ', createPlaceholder: 'デッキ名' },
  'ko-KR.json': { create: '새 덱', createPlaceholder: '덱 이름' },
  'pt-BR.json': { create: 'Novo baralho', createPlaceholder: 'Nome do baralho' },
}

for (const [file, vals] of Object.entries(T)) {
  const full = join(DIR, file)
  const json = JSON.parse(readFileSync(full, 'utf8'))
  json.flashcards.deck.create = vals.create
  json.flashcards.deck.createPlaceholder = vals.createPlaceholder
  writeFileSync(full, JSON.stringify(json, null, 2) + '\n', 'utf8')
  console.log(`${file.padEnd(13)} create="${vals.create}"  createPlaceholder="${vals.createPlaceholder}"`)
}
