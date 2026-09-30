/**
 * stage-i18n-bugk.mjs — 精确把 BUG-K 的 i18n 键写入 git 索引（2026-09-30）。
 *
 * ## 为什么需要这么绕
 *
 * BUG-K 要求把列表页按钮文案从「新建卡组」改成「新建卡片」（原文案与行为不符：
 * 按钮跳的是 /flashcards/new 卡片页）。但 `frontend/src/locales/*.json` 里同时
 * 混着**并发会话的大量未提交改动**（redclaw / finance / settingsMenu / rss &
 * email 错误键 / 导航项等，zh-CN 单文件就有几十行）。`git add` 整个文件必然
 * 夹带他人改动，所以不能走常规路径。
 *
 * `git add -p` 是交互式的，本环境不支持。
 *
 * ## 做法
 *
 * 不碰工作区、不 stash、不动并发会话已 staged 的内容：
 *   1. 取 HEAD 版本的 JSON（干净，无他人改动）
 *   2. 只在上面应用 BUG-K 的 3 个键
 *   3. `git hash-object -w` 写出 blob
 *   4. `git update-index --cacheinfo` 把这个 blob 放进索引
 *   5. 之后 `git commit -- <path>` 提交的就是这个精确构造的版本
 *
 * 工作区文件保持原样（含他人的改动），提交后 theirs 仍在工作区，不丢失。
 *
 * 用法：node scripts/stage-i18n-bugk.mjs [--check]
 */
import { execFileSync } from 'node:child_process'

const LOCALES_DIR = 'frontend/src/locales'
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
const CHECK = process.argv.includes('--check')

/** 各语言 BUG-K 需要的键值。fallbackLocale 之外仍显式补齐，避免非中文用户看到裸键名。 */
const T = {
  'zh-CN': {
    listCreate: { key: 'flashcards.list.create', from: '新建卡组', to: '新建卡片' },
    deckCreate: '新建卡组',
    deckCreatePlaceholder: '卡组名称',
  },
  'en-US': {
    listCreate: { key: 'flashcards.list.create', from: 'Create deck', to: 'New card' },
    deckCreate: 'New deck',
    deckCreatePlaceholder: 'Deck name',
  },
  'ja-JP': { listCreate: { key: 'flashcards.list.create', from: 'デッキを作成', to: '新しいカード' },
    deckCreate: '新しいデッキ', deckCreatePlaceholder: 'デッキ名' },
  'ko-KR': { listCreate: { key: 'flashcards.list.create', from: '덱 만들기', to: '새 카드' },
    deckCreate: '새 덱', deckCreatePlaceholder: '덱 이름' },
  'fr-FR': { listCreate: { key: 'flashcards.list.create', from: 'Créer un paquet', to: 'Nouvelle carte' },
    deckCreate: 'Nouveau paquet', deckCreatePlaceholder: 'Nom du paquet' },
  'de-DE': { listCreate: { key: 'flashcards.list.create', from: 'Stapel erstellen', to: 'Neue Karte' },
    deckCreate: 'Neues Deck', deckCreatePlaceholder: 'Deck-Name' },
  'es-ES': { listCreate: { key: 'flashcards.list.create', from: 'Crear mazo', to: 'Nueva tarjeta' },
    deckCreate: 'Nuevo mazo', deckCreatePlaceholder: 'Nombre del mazo' },
  'pt-BR': { listCreate: { key: 'flashcards.list.create', from: 'Criar baralho', to: 'Novo cartão' },
    deckCreate: 'Novo baralho', deckCreatePlaceholder: 'Nome do baralho' },
  'zh-TW': { listCreate: { key: 'flashcards.list.create', from: '新增卡組', to: '新增卡片' },
    deckCreate: '新增卡組', deckCreatePlaceholder: '卡組名稱' },
}

let staged = 0
const report = []

for (const [locale, cfg] of Object.entries(T)) {
  const path = `${LOCALES_DIR}/${locale}.json`
  let text
  try {
    text = git('show', `HEAD:${path}`)
  } catch {
    report.push(`${locale}: HEAD 无此文件，跳过`)
    continue
  }

  const before = text
  let obj
  try {
    obj = JSON.parse(text)
  } catch (e) {
    report.push(`${locale}: HEAD 版本 JSON 解析失败，跳过：${e.message}`)
    continue
  }
  const fc = obj?.flashcards
  if (!fc?.list || !fc?.deck) {
    report.push(`${locale}: 缺少 flashcards.list / flashcards.deck，跳过`)
    continue
  }

  // 1) list.create 文案修正：按钮跳的是 /flashcards/new（新建卡片页），
  //    原文案却写「新建卡组」，文案与行为不符（BUG-K）。
  fc.list.create = cfg.listCreate.to
  // 2) deck 块新增两个键，供 FlashcardEditView 的就地建卡组入口使用。
  fc.deck.create = cfg.deckCreate
  fc.deck.createPlaceholder = cfg.deckCreatePlaceholder

  // 序列化格式必须与原文件一致，否则 diff 会淹没在格式变化里。
  // 实测 `JSON.stringify(obj, null, 2)` 与本仓库的 locales 格式**逐字节相同**，
  // 唯一差别是它多一个末尾换行 —— 所以这里按原文件是否以换行结尾来决定加不加。
  const body = JSON.stringify(obj, null, 2)
  text = before.endsWith('\n') ? body + '\n' : body

  try {
    JSON.parse(text)
  } catch (e) {
    report.push(`${locale}: 序列化后 JSON 非法，已放弃：${e.message}`)
    continue
  }

  if (text === before) {
    report.push(`${locale}: 无变化`)
    continue
  }
  if (CHECK) {
    report.push(`${locale}: 待写入 ${text.length - before.length} 字节（--check 模式，未实际写入）`)
    continue
  }

  const hash = execFileSync('git', ['hash-object', '-w', '--stdin'], { input: text, encoding: 'utf8' }).trim()
  git('update-index', '--cacheinfo', '100644', hash, path)
  staged++
  report.push(`${locale}: 已写入索引 ${hash.slice(0, 10)}`)
}

console.log(report.join('\n'))
console.log(`\nstaged ${staged} locale file(s)`)
if (!CHECK && staged > 0) {
  console.log('\n下一步：git commit -- <上面的 locale 路径>')
  console.log('（务必带 pathspec，否则会连带提交并发会话已 staged 的 54 个改动）')
}
