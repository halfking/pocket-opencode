// dead-api-classify.test.mjs
//
// 锁住「死能力」四分类的**顺序语义**，以及棘轮安全性。
//
// 背景（2026-10-03 真实缺陷）：原分类顺序是
//     app > 0 → wired，test > 0 → testOnly，ownRefs > 0 → moduleInternal
// 于是**同时**被本模块使用、又被测试引用的符号被判成 testOnly，报告输出
//     ⚠️ 仅被 __tests__ 引用：能力被测过，但没接进 App
// 而它在 App 里正被使用。实测被误报 12 个，例如
// MEETING_SUMMARY_TIMEOUT_MS —— 它就是 meetingsApi.summarize 的 http 选项里
// 那个值（api/meetings.ts:86），而 meetingsApi.summarize 被 useLiveSummary 调用。
//
// 这类缺陷最坏的地方在于它**带 ⚠️ 且说的是假话**：读到的人会去「接线」一个
// 早就接好的东西，或者反过来怀疑自己的功能是不是没接上。
//
// 修复：ownRefs 提到 test 之前。判据必须钉住两件事——
//   ① 顺序语义本身；
//   ② **dead 集合不因调序而改变**。这是修复不削弱卡口的唯一依据：
//      dead 的定义是 app===0 && test===0 && ownRefs===0，调整判定顺序不改变
//      这个合取式，所以基线不动。若哪天有人为图省事把 dead 也并进别的分支，
//      棘轮就会放走真正的死代码，而这条断言会先红。
//
// 负控在文件末尾。

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { classifyRef, CLASS_BUCKETS } from '../../../scripts/dead-api-classify.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..', '..')
const CHECKER = join(SRC, '..', 'scripts', 'check-dead-api.mjs')
const MEETINGS_API = join(SRC, 'api', 'meetings.ts')

describe('死能力四分类：顺序语义', () => {
  it('外部非测试文件引用 → wired', () => {
    assert.equal(classifyRef({ app: 1, test: 5, ownRefs: 3 }), 'wired')
  })

  it('只有本模块在用（哪怕测试也在引）→ moduleInternal，不是 testOnly', () => {
    // 这就是本轮修的那一条。app=0 test=1 ownRefs=1：模块自己在用，只是外部拿不到。
    assert.equal(classifyRef({ app: 0, test: 1, ownRefs: 1 }), 'moduleInternal')
  })

  it('连本模块都没用、只被测试引用 → testOnly（真正的「没接进 App」）', () => {
    // 收紧后仍必须保留这个格子，否则警告桶被清空 = 警告能力消失。
    assert.equal(classifyRef({ app: 0, test: 1, ownRefs: 0 }), 'testOnly')
  })

  it('哪都没被引用 → dead', () => {
    assert.equal(classifyRef({ app: 0, test: 0, ownRefs: 0 }), 'dead')
  })

  it('dead 的定义不受判定顺序影响（棘轮安全性）', () => {
    // 穷举三计数的小组合：dead ⟺ 三者全 0。修复只调顺序，不改这个合取式。
    for (let app = 0; app <= 2; app++) {
      for (let test = 0; test <= 2; test++) {
        for (let ownRefs = 0; ownRefs <= 2; ownRefs++) {
          const allZero = app === 0 && test === 0 && ownRefs === 0
          assert.equal(
            classifyRef({ app, test, ownRefs }) === 'dead',
            allZero,
            `app=${app} test=${test} ownRefs=${ownRefs} 的 dead 判定与「三者全 0」不一致`,
          )
        }
      }
    }
  })

  it('每个 kind 在报告里都有对应的标题与标记（两处不许漂移）', () => {
    for (const kind of ['dead', 'testOnly', 'moduleInternal']) {
      const b = CLASS_BUCKETS.find((x) => x.kind === kind)
      assert.ok(b, `CLASS_BUCKETS 里缺 ${kind} 的展示信息`)
      assert.ok(b.title && b.title.trim(), `${kind} 缺标题`)
      assert.ok(b.mark && b.mark.trim(), `${kind} 缺标记`)
    }
  })
})

describe('接线护栏：判据不能有第二份实现', () => {
  const checker = readFileSync(CHECKER, 'utf8')

  it('check-dead-api.mjs 必须 import 这个判据，而不是自己再写一份', () => {
    assert.match(
      checker,
      /import \{[^}]*classifyRef[^}]*\} from '\.\/dead-api-classify\.mjs'/,
      'check-dead-api.mjs 没有 import classifyRef —— 判据有两份实现，迟早漂移',
    )
    // 内联的 if 链必须已经不存在（哪怕还在注释里也算，说明没真删）
    const noComments = checker.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    assert.doesNotMatch(
      noComments,
      /if \(app > 0\)\s*kind\s*=/,
      'check-dead-api.mjs 仍在内联做分类 —— 判据有两份实现',
    )
  })

  it('MEETING_SUMMARY_TIMEOUT_MS 确实被本模块真实使用（不许靠注释撑分类）', () => {
    // 判据的真实性依赖 ownRefs 数的是**代码**。这里把真实代码行钉住：
    // 剥掉注释后仍应剩下 http 选项里的那一处。
    const src = readFileSync(MEETINGS_API, 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, ' ')
      .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    const uses = (src.match(/\bMEETING_SUMMARY_TIMEOUT_MS\b/g) || []).length
    // 1 次 = export 声明；2 次 = 声明 + http 选项里的使用。
    assert.equal(uses, 2, `剥注释后应剩 2 处（声明 + 使用），实际 ${uses}`)
    assert.match(src, /timeoutMs:\s*MEETING_SUMMARY_TIMEOUT_MS/)
  })
})

describe('判据自检：负控必须转红', () => {
  it('把顺序退回 test 优先 → 那条「模块内在用」立刻不再成立', () => {
    const buggy = ({ app, test, ownRefs }) => {
      if (app > 0) return 'wired'
      if (test > 0) return 'testOnly'
      if (ownRefs > 0) return 'moduleInternal'
      return 'dead'
    }
    // 真实判据在本轮要修的那组输入上给出 moduleInternal
    assert.equal(classifyRef({ app: 0, test: 1, ownRefs: 1 }), 'moduleInternal')
    // 旧顺序给出 testOnly —— 这正是 2026-10-03 那条 ⚠️ 的来源
    assert.equal(buggy({ app: 0, test: 1, ownRefs: 1 }), 'testOnly')
    assert.notEqual(
      classifyRef({ app: 0, test: 1, ownRefs: 1 }),
      buggy({ app: 0, test: 1, ownRefs: 1 }),
      '负控本该转红却判成了通过 —— 顺序语义没被这条判据区分开',
    )
  })

  it('把 dead 并进 testOnly → 棘轮安全性断言转红（真正的死代码会被放走）', () => {
    const sloppy = ({ app, test, ownRefs }) => {
      if (app > 0) return 'wired'
      if (ownRefs > 0) return 'moduleInternal'
      if (test > 0) return 'testOnly'
      return 'moduleInternal' // ← 错了：dead 被吞成 moduleInternal
    }
    // 判据的形态：dead ⟺ 三者全 0
    assert.equal(classifyRef({ app: 0, test: 0, ownRefs: 0 }), 'dead')
    assert.equal(
      sloppy({ app: 0, test: 0, ownRefs: 0 }),
      'moduleInternal',
      '负控样本没有真的改坏（前提不成立）',
    )
    assert.notEqual(
      classifyRef({ app: 0, test: 0, ownRefs: 0 }),
      sloppy({ app: 0, test: 0, ownRefs: 0 }),
      '负控本该转红却判成了通过 —— 棘轮安全性没被这条判据区分开',
    )
  })

  it('把 testOnly 桶整个删掉 → 格子缺失断言转红', () => {
    const withoutTestOnly = CLASS_BUCKETS.filter((b) => b.kind !== 'testOnly')
    assert.equal(CLASS_BUCKETS.length, 3, '真实实现应有三个展示桶')
    assert.equal(
      withoutTestOnly.length,
      2,
      '负控样本没有真的删掉桶（前提不成立）',
    )
    assert.ok(
      !withoutTestOnly.some((b) => b.kind === 'testOnly'),
      '负控样本没有真的删掉 testOnly 桶',
    )
  })
})
