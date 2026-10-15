// meeting-summary-skill-scope.test.mjs — §416：「总结技能」的作用域。
//
// ── 为什么这道门存在 ──
//
// 用户需求原文：「在录音时需要进行即时总结」「需要到网上搜索相关的总结的技能」。
// 当场读数（§416.1）：产品里有**两条**总结链，而**技能选择器只接上了其中一条**：
//
//   ┌ 链                              │ 谁触发           │ 提示词                  │ 产出形态        │ 读技能？ │
//   ├────────────────────────────────┼──────────────────┼─────────────────────────┼────────────────┼─────────┤
//   │ 录音中即时总结 useLiveSummary   │ 录音中每 30s/3 段 │ Go buildSummaryPrompt   │ **JSON 契约**  │ ❌      │
//   │ 录完后手动「总结」 onSummarize  │ 用户点一下       │ MEETING_SKILLS[i]       │ **自由中文文本**│ ✅      │
//   │ 会后精校 refine                │ 录音收尾         │ buildRefinePrompt       │ JSON           │ ❌      │
//
// ⇒ 用户在设置里挑「决议清单」，**对录音中看到的那份总结零影响**。
//
// 这不是「功能没做」，是**设置在说谎**：它把技能芯片摆在录音之前，
// 而技能只作用于录音之后。可这不是本门要修的东西（属待拍板 #12 的口径），
// 本门只负责**让这个作用域不腐烂**。
//
// ── 为什么要钉而不是只写在文档里 ──
//
// 同仓先例 `check:refine-summary-meta-split`：待拍板项的前提一旦被人修掉，
// 待拍板清单不会自己更新，下一段人会把一条**已经不成立**的理由当成第一优先级。
// ⇒ 这些断言转红时，要按「#12 的前提变了，去更新决策简报」来读，
//   而不是当成「代码坏了」。
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const MEETINGS = path.resolve(HERE, '..')

/**
 * ⚠ 读入后**必须归一化行尾**（`check:crlf-needles` 的规定）。
 *   本判据用 `'\n]'` 之类带换行转义的针找代码位；不归一化的话，
 *   哪天这个文件被 CRLF 存一遍，所有这类针会**同时**失配 ⇒ 判据静默空转。
 */
const normalizeEol = (s) => s.replace(/\r\n/g, '\n')
const read = (...p) => normalizeEol(readFileSync(path.join(SRC, ...p), 'utf8'))
const LIVE = read('composables', 'useLiveSummary.ts')
const API = read('api', 'meetings.ts')
const SKILLS = read('features', 'meetings', 'meeting-skills.ts')

/** ⚠ 判据文件里不留完整字面量（§415）：旧串一律拼接构造。 */
const SKILL_MD_NAME = ['meeting-minutes', '.md'].join('')

/** 括���配平切出 `f(` 的实参列表。本仓不写分号，正则切会在嵌套调用上断掉。 */
function callArgs(src, head) {
  const at = src.indexOf(head)
  if (at < 0) return null
  const open = src.indexOf('(', at + head.length - 1)
  let depth = 0
  for (let i = open; i < src.length; i++) {
    const c = src[i]
    if (c === '(') depth++
    else if (c === ')') {
      depth--
      if (depth === 0) return src.slice(open + 1, i)
    }
  }
  return null
}

/** `meetingsApi.summarize` 的形参列表（声明层）。 */
function summarizeParams() {
  const at = API.indexOf('async summarize(')
  if (at < 0) return null
  const open = API.indexOf('(', at)
  let depth = 0
  for (let i = open; i < API.length; i++) {
    if (API[i] === '(') depth++
    else if (API[i] === ')') {
      depth--
      if (depth === 0) return API.slice(open + 1, i)
    }
  }
  return null
}

/** 技能 id 与提示词（从 TS 常量里取，不重新誊一遍 —— 誊一遍就成了第二份真相）。 */
function skillPrompts() {
  const at = SKILLS.indexOf('MEETING_SKILLS: MeetingSkill[] = [')
  if (at < 0) return []
  // ⚠ 刻意**不用**「换行 + 右方括号」当锚：那是个 CRLF 脆弱针
  //   （`check:crlf-needles` 会因为它在 LF/CRLF 两种变体上都判不出命中而报红），
  //   而这里完全可以用**前后两个不含换行的锚点**夹出同一块。
  const end = SKILLS.indexOf('DEFAULT_MEETING_SKILL', at)
  if (end < 0) return []
  const block = SKILLS.slice(at, end)
  return [...block.matchAll(/prompt:\s*`([\s\S]*?)`/g)].map((m) => m[1])
}

/** 全仓扫「谁读那个 .md」—— 只统计**产品代码**里的引用（排除本门自己）。 */
function consumersOfSkillMd() {
  const hits = []
  const walk = (dir) => {
    for (const name of readdirSync(dir)) {
      if (name === 'node_modules' || name.startsWith('.')) continue
      const abs = path.join(dir, name)
      if (statSync(abs).isDirectory()) { walk(abs); continue }
      const rel = path.relative(SRC, abs)
      // 排除判据文件自己：它必须提到那个名字才能查它。
      // ⚠ **只有这一个豁免**，且是必需的那个（2026-10-11 实测：
      //   排除 `meeting-skills.test.ts` 是多余的 —— 它并不引用那份 .md，
      //   全仓除本门外引用数为 0。凭空加的豁免会让这道门慢慢变成「什么都行」）。
      if (rel.endsWith('meeting-summary-skill-scope.test.mjs')) continue
      try {
        if (normalizeEol(readFileSync(abs, 'utf8')).includes(SKILL_MD_NAME)) hits.push(rel)
      } catch { /* 二进制读不动就当没引用 */ }
    }
  }
  walk(SRC)
  return hits
}

describe('§416 · 「总结技能」的作用域（这些是待拍板 #12 的前提）', () => {
  it('前提自证：三处锚点都抽得到（抽不到 ⇒ 下面几条会变成恒真）', () => {
    assert.ok(skillPrompts().length >= 4, `只抽到 ${skillPrompts().length} 条技能提示词`)
    assert.ok(summarizeParams() !== null, '抽不到 meetingsApi.summarize 的形参列表')
    assert.ok(callArgs(LIVE, 'meetingsApi.summarize(') !== null, '抽不到实时总结链的实参')
  })

  it('★ 录音中的即时总结**不读**技能选择器', () => {
    const args = callArgs(LIVE, 'meetingsApi.summarize(')
    assert.doesNotMatch(args, /skill/i,
      `实时总结链开始传技能了（实参：${args.trim().slice(0, 120)}）—— ` +
      '#12 的前提已变：要么去决策简报更新，要么撤这条登记')
    const params = summarizeParams()
    assert.doesNotMatch(params, /skill/i,
      `meetingsApi.summarize 声明了技能形参：${params.replace(/\s+/g, ' ').slice(0, 160)} —— 同上`)
  })

  it('★ 四条技能提示词**都没有产出契约**（⇒ 手动总结的产出形状不可知）', () => {
    // 手动总结那一条链的产出被原样写进 `meeting.summary`，而行动项**不**从它取
    // （恒取自 liveSummary，既有门 meeting-action-items-source 已钉）。
    // ⇒ 技能提示词要求什么形状的产出，决定了「用户按技能得到的那个东西」长什么样。
    // 四条里一条都没要求 JSON ⇒ 形状由模型当场所定。
    const prompts = skillPrompts()
    const withContract = prompts.filter((p) => p.includes('action_items'))
    assert.deepEqual(withContract, [],
      `这些技能提示词开始要求 JSON 契约了：${withContract.length} 条 —— ` +
      '#12 的前提已变（这正是选项 B/C 要做的事），去更新决策简报再改这里')
  })

  it('★ 唯一规定了 JSON 契约的那份技能文件，今天是**死文件**', () => {
    // `skills/meeting-minutes.md` 写了完整 JSON 契约 + 增量规则，
    // 而**全仓没有任何代码读它**；真正在跑的 `MEETING_SKILLS[0]` 反而没有契约。
    //
    // ⚠ 刻意**不删**它：若 #12 选 B（技能市场），.md 正是最自然的载体形态。
    //   所以这里登记的是「它今天零消费者」，不是「它该消失」。
    const mdPath = path.join(MEETINGS, 'skills', SKILL_MD_NAME)
    assert.ok(normalizeEol(readFileSync(mdPath, 'utf8')).includes('action_items'),
      '那份 .md 不再规定 action_items 了 ⇒ 下面关于「唯一」的结论要重写')
    assert.deepEqual(consumersOfSkillMd(), [],
      '那份 .md 有人读了 ⇒ 本门对「零消费者」的结论已过期，去查它被接到哪去了')
  })

  it('★★ 负控：实时总结一旦接上技能，本判据必须立刻翻脸', () => {
    // 只有构造出「接上了」的形态，才知道上面那两条断言是不是恒真。
    const wired = LIVE.replace(
      'meetingsApi.summarize(',
      'meetingsApi.summarize(',
    ).replace(/liveSummary\.value\?\.summary,\n(\s*)opts\?\.meta\?\.value,/, 'skillPrompt,\n$1opts?.meta?.value,')
    const args = callArgs(wired, 'meetingsApi.summarize(')
    assert.ok(args && /skill/i.test(args), '负控本身没造出「接上了」的形态')
  })

  it('★★ 负控：技能提示词一旦带上契约，本判据必须立刻翻脸', () => {
    const withJson = SKILLS.replace(/prompt: `你是会议记录助手[\s\S]*?`/,
      'prompt: `你是会议记录助手。返回 JSON：{"action_items":[{"text":"","due":""}]}`')
    assert.notEqual(withJson, SKILLS, '负控没改成')
    const has = [...withJson.matchAll(/prompt:\s*`([\s\S]*?)`/g)].some((m) => m[1].includes('action_items'))
    assert.equal(has, true, '负控前提失败：造出来的样本没有契约')
  })
})