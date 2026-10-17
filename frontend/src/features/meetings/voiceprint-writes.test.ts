// voiceprint-writes.test.ts —— 声纹库写入决策的行为门。
//
// 为什么能测到行为而不是文本：判据落在**那条 SQL 实际会写哪几列**上，
// 而 voiceprint-writes.ts 刻意不 import 原生模块，所以这段代码在 Node 里真的跑得起来。
// （voiceprints-store.ts 本身测不了：它 import local-db.ts，后者 import
//   './sqlite-web-init' 无扩展名 ⇒ ERR_MODULE_NOT_FOUND —— 同款问题让
//   flashcardIo.test.ts 至今挂在 test-coverage-waivers 里。）
//
// 这道门守的是一个**已经发生过的**缺陷：改一次名字就把样本数 +1
//（原实现里 saveVoiceprint 对已存在的行执行 sample_count = existing + 1，
//  而 labelSpeaker 每次改名都调它）。
//
// ⚠ 列解析器必须自证：若 setColumns 对任何语句都返回同一个值，
// 下面所有断言都会安静地恒真。所以第 1 条就拿一条**写三列**的语句验它。

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describe, it } from 'node:test'
import {
  nextSampleCount, planVoiceprintWrite,
  SQL_ENROLL_AGAIN, SQL_LIST, SQL_RENAME,
} from './voiceprint-writes.ts'

/** 解析一条 UPDATE 的 SET 子句，返回它会写入的列名。 */
function setColumns(sql: string): string[] {
  const m = sql.match(/\bSET\b([\s\S]*?)\bWHERE\b/i)
  assert.ok(m, `这条语句里找不到 SET…WHERE 子句：${sql}`)
  return m[1]
    .split(',')
    .map((part) => part.trim().split(/\s*=\s*/)[0].trim())
    .filter(Boolean)
}

describe('voiceprint-writes', () => {
  it('setColumns 解析器自证：会数出多条语句的不同列数', () => {
    assert.equal(setColumns(SQL_ENROLL_AGAIN).length, 3)
    assert.deepEqual(setColumns(SQL_ENROLL_AGAIN).sort(), ['display_name', 'embedding', 'sample_count'])
    assert.deepEqual(setColumns(SQL_RENAME).sort(), ['display_name'])
    // 反向对照：解析器不是恒定的
    assert.notDeepEqual(setColumns(SQL_ENROLL_AGAIN), setColumns(SQL_RENAME))
  })

  it('改名只写 display_name 一列 —— 不动样本数、不动声纹向量', () => {
    assert.deepEqual(setColumns(SQL_RENAME), ['display_name'])
  })

  it('列表查询不取 sample_count（避免把「改过几次名」当成「几个样本」展示）', () => {
    assert.ok(!/\bsample_count\b/.test(SQL_LIST), `SQL_LIST 不该取 sample_count：${SQL_LIST}`)
  })

  it('已存在 → 只改名；不存在 → 带向量插入', () => {
    assert.deepEqual(planVoiceprintWrite(true), { op: 'rename' })
    assert.deepEqual(planVoiceprintWrite(false), { op: 'insert' })
  })

  it('两条分支必须不同，否则 upsert 的存在性判定就是摆设', () => {
    assert.notDeepEqual(planVoiceprintWrite(true), planVoiceprintWrite(false))
  })

  it('只有「再注册一个音频样本」才允许让样本数 +1', () => {
    assert.equal(nextSampleCount(1), 2)
    assert.equal(nextSampleCount(null), 2)
    assert.equal(nextSampleCount(undefined), 2)
    assert.equal(nextSampleCount(7), 8)
  })

  it('负控：把改名的 SQL 换成 enroll 那条，本门必须抓到样本数被写', () => {
    // 复现原始 bug 的等价形状 —— 只换掉语句，其余不变。
    assert.ok(setColumns(SQL_ENROLL_AGAIN).includes('sample_count'))
    // 而改名那条不该含它：这一条断言就是本门真正的牙齿。
    assert.ok(!setColumns(SQL_RENAME).includes('sample_count'))
  })
})

// ── 接缝层 ────────────────────────────────────────────────────────────
// 上面测的是「rename 这条语句写哪几列」。但真正的 bug 发生在**调用点**：
// labelSpeaker 过去调的是 saveVoiceprint（已存在的行会 +1 样本）。
// 纯模块测不到调用关系，所以这一段去读真实源码。
describe('声纹写入接缝（labelSpeaker 走哪条路径）', () => {
  const REC = new URL('../../native/recordingRuntime.ts', import.meta.url)

  /** 取 `async <name>(…) { … }` 的函数体（大括号配平）。 */
  function fnBody(src: string, decl: string): string {
    const start = src.indexOf(decl)
    assert.ok(start >= 0, `源码里找不到 ${decl}`)
    const open = src.indexOf('{', start)
    let depth = 0
    for (let i = open; i < src.length; i++) {
      if (src[i] === '{') depth++
      else if (src[i] === '}' && --depth === 0) return src.slice(open, i + 1)
    }
    throw new Error(`${decl} 的函数体没配平`)
  }

  /** 这段源码实际调用了哪些写入口（按调用形状 `name(` 采信，不是裸词）。 */
  function callsWritePath(body: string): string[] {
    return ['upsertVoiceprintLabel', 'saveVoiceprint', 'renameVoiceprint', 'enrollFromAudio']
      .filter((n) => new RegExp(`\\b${n}\\s*\\(`).test(body))
  }

  it('录音中改名必须走 upsertVoiceprintLabel，不能走会 +1 样本数的 saveVoiceprint', () => {
    const body = fnBody(readFileSync(REC, 'utf8'), 'async labelSpeaker(')
    const calls = callsWritePath(body)
    assert.deepEqual(calls, ['upsertVoiceprintLabel'],
      `labelSpeaker 的写入口应是且仅是 upsertVoiceprintLabel，实测 ${JSON.stringify(calls)}`)
  })

  it('负控：把调用换回 saveVoiceprint，本接缝必须判红（用合成样本，不依赖活文件）', () => {
    const mutant = 'async labelSpeaker(id, name) { await saveVoiceprint({ id, displayName: name, embedding }) }'
    assert.deepEqual(callsWritePath(mutant), ['saveVoiceprint'])
    assert.notDeepEqual(callsWritePath(mutant), ['upsertVoiceprintLabel'])
  })
})
// ─────────────────────────────────────────────────────────────────────────
// §822：读取失败**必须显示错误，不能显示「还没有声纹」**。
//
// `VoiceprintSheet.vue` 的 `load()` 此前在 catch 里 `items.value = []`
// ⇒ 用户被告知自己有 0 条声纹，而真相是「没读出来」—— 两者对用户含义完全相反，
// 后者会让人以为录过的人全都白录了。同 §820 属「谎报」家族。
//
// ⚠ 判据只能是源码扫描：Vue SFC 的 `load()` 在 node 里跑不到（无组件实例），
//   且把决策提成纯函数也救不了 —— 剩下的动作只有「设不设这个 ref」，判据仍看不到执行。
//   ⇒ 这里只钉住**具体缺陷不再回来**，并对模板优先级给出真正有牙的顺序断言。
describe('§822 声纹库读取失败不得伪装成空列表', () => {
  const src = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'VoiceprintSheet.vue'), 'utf8')
  const load = src.slice(src.indexOf('async function load('), src.indexOf('watch('))

  it('★ catch 里不得把 items 置空（那是在编造一个「你没有声纹」的事实）', () => {
    const catchBody = load.slice(load.indexOf('catch'))
    assert.ok(
      !/items\.value\s*=\s*\[\s*\]/.test(catchBody),
      '读取失败又把 items 清空 ⇒ 用户看到「还没有声纹」，而实际是没读出来',
    )
  })

  it('★ catch 里必须登记 loadError（否则模板无从判断该不该显示错误）', () => {
    const catchBody = load.slice(load.indexOf('catch'))
    assert.match(catchBody, /loadError\.value\s*=/, '失败没有登记原因')
  })

  it('★ 模板必须先判 loadError 再判空列表（顺序反了等于没改）', () => {
    const iErr = src.indexOf('v-else-if="loadError"')
    const iEmpty = src.indexOf('v-else-if="items.length === 0"')
    assert.ok(iErr > 0 && iEmpty > 0, '模板里找不到这两个分支')
    assert.ok(iErr < iEmpty, '空态排在错误态之前 ⇒ 失败时仍然显示「还没有声纹」')
  })

  it('★ 「清空全部」按钮在加载失败时必须隐藏（items 是过期内容，不能参与计数）', () => {
    assert.match(
      src,
      /v-if="items\.length > 0 && !loadError"/,
      '错误态下仍让过期 items 驱动「清空全部」入口 ⇒ 用户可能对着过期数字做破坏性操作',
    )
  })

  it('负控：正常加载成功时按钮仍要在（别把入口一起关掉）', () => {
    assert.match(src, /items\.length > 0 && !loadError/, '条件必须同时包含长度与错误态两项')
    const cond = src.match(/v-if="(items\.length > 0[^"]*)"/)
    assert.match(String(cond && cond[1]), /!loadError/)
    assert.match(String(cond && cond[1]), /items\.length > 0/)
  })
})
