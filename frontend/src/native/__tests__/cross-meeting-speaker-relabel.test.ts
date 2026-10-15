// cross-meeting-speaker-relabel.test.ts — §400：收尾合并**不得改到别的会议**的说话人标签。
//
// ★ 这条路径不在 §399 那道 `micOn` 门后面 —— 它跑在 `stop()` 里，
//   而 `stop()` 是每一次录音收尾都会走的常规路径。
//
// ── 缺陷链（全部是当前代码）──
//
// ① `segmentProfiles` 是**进程级** `Map`，只在 `freshSession` 时 clear
//   （`recordingRuntime.ts:198 / 305-308`）；`freshSession` 仅在
//   `start-fresh` / `replace-other` 为 true，`resume-self` 为 false。
// ② `MeetingDetailView.vue:295` 的续录判据是 `storedSegments.length > 0`
//   ⇒ 打开**任何一段已有分录的会议**再点录音，就走 `resume-self` ⇒ ① 不清。
// ③ `mergeOverSplitSpeakers` 用 `planSpeakerRelabel({ segmentProfiles: this.segmentProfiles, … })`，
//   而 `currentLabelOf: (segId) => byId.get(segId)?.speakerLabel ?? null`
//   —— `byId` 只含**当前会议**的段 ⇒ 外来 segId 拿到 `null`。
// ④ `isPlaceholderSpeaker(null)` 返回 **true**（`meeting-roster-homophone.ts:76`
//   的 `if (t === '') return true`）⇒ 守卫把「**查不到的段**」当成占位标签放行。
// ⑤ 原实现落库时遍历 `plan.updates` 全量，不看 `byId`
//   ⇒ **会议 A 的段被写成会议 B 聚类出的标签**。
//
// ⚠ 原实现里内存侧循环**有** `byId.get` 保护、落库侧**没有** ——
//   「内存里没改、库里改了」是最坏的一种不一致。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import path from 'node:path'
import fs from 'node:fs'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..')

// ★ 装载真模块：`planSpeakerRelabel` 与 `isPlaceholderSpeaker` 都是零 I/O 的纯函数。
const { planSpeakerRelabel } = await import(path.resolve(SRC, 'native/speaker-relabel.ts'))
const { isPlaceholderSpeaker } = await import(
  path.resolve(SRC, 'features/meetings/meeting-roster-homophone.ts')
)

const RUNTIME = path.resolve(SRC, 'native/recordingRuntime.ts')
const runtimeSrc = fs.readFileSync(RUNTIME, 'utf8')

/** 逐字照抄 `mergeOverSplitSpeakers` 传给 `planSpeakerRelabel` 的四个实参。 */
function planFor(segmentProfiles: Map<string, string>, segments: Array<{ id: string; speakerLabel: string | null }>) {
  const byId = new Map(segments.map((s) => [s.id, s]))
  return {
    byId,
    plan: planSpeakerRelabel({
      segmentProfiles,
      canonicalOf: new Map([['p2', 'p1']]),
      labelOf: (pid: string) => (pid === 'p1' ? '说话人 1' : pid === 'p2' ? '说话人 2' : undefined),
      currentLabelOf: (segId: string) => byId.get(segId)?.speakerLabel ?? null,
    }),
  }
}

/**
 * 读出生产源码里「落库用的那个集合」是怎么来的，以及它是不是**真的**被写进库。
 *
 * ⚠ 第一版我把反向保护写成了「在测试里自己算一遍 filter」——
 *   那条断言在**生产代码被整个关掉时照样绿**（它测的是测试自己）。
 *   「反向保护」若检测不到它声称要防的失败，比没有门更坏。⇒ 改成读源码。
 */
function productionWiring(): { filtered: boolean; writesOwn: boolean } {
  const filtered = /ownUpdates\s*=\s*plan\.updates\.filter\(\(u\)\s*=>\s*byId\.has\(u\.segId\)\)/.test(runtimeSrc)
  const writesOwn = /ownUpdates\.map\(\(u\)\s*=>\s*updateSegmentSpeaker\(u\.segId/.test(runtimeSrc)
  const writesRaw = /plan\.updates\.map\(\(u\)\s*=>\s*updateSegmentSpeaker\(u\.segId/.test(runtimeSrc)
  assert.equal(writesRaw, false,
    '落库仍在遍历未过滤的 plan.updates ⇒ docs §400 的缺陷原样存在')
  return { filtered, writesOwn }
}

describe('§400 · 收尾合并不得改到别的会议', () => {
  it('★★ 负控前提：未知段确实会被判成占位标签（这正是缺陷的成因）', () => {
    assert.equal(isPlaceholderSpeaker(null), true,
      'isPlaceholderSpeaker(null) 不再为 true ⇒ 外来 segId 会被守卫挡住，§400 的链断在第 ④ 环，' +
      '请更新本门与 docs §400（那是好事，但结论要改）。')
  })

  it('★★ planSpeakerRelabel 确实会产出不属于当前会议的段（缺陷链第 ⑤ 环的原料）', () => {
    const { byId, plan } = planFor(
      new Map([['A-seg-1', 'p1'], ['A-seg-2', 'p2']]), // 残留：会议 A
      [{ id: 'B-seg-1', speakerLabel: '说话人 1' }], // 当前：会议 B
    )
    const foreign = plan.updates.filter((u) => !byId.has(u.segId))
    assert.ok(foreign.length > 0,
      '纯函数这一步已经没有外来段了 ⇒ 防线要么已修、要么链已变；' +
      '若确已修，请把本用例改成「反向保护」。')
  })

  it('★★ 落库前必须按当前会议的分段过滤，且写进库的就是这个过滤后的集合', () => {
    const w = productionWiring()
    assert.ok(w.filtered,
      'recordingRuntime.ts 里找不到「落库前按 byId 过滤」这道防线 —— ' +
      '若是有意移除，请先读 docs §400：它防的是「收尾会议 B 时改掉会议 A 的说话人标签」。')
    assert.ok(w.writesOwn,
      '过滤后的集合没有被真正写库（updateSegmentSpeaker 走的不是 ownUpdates）' +
      ' ⇒ 过滤形同虚设。')
  })

  it('★★ 反向保护：过滤后的集合必须**非空**（防「关掉跨会议写入的同时关掉了正事」）', () => {
    // 这一条同时验两件事：① 生产接线是通的；② 当前会议自己的段仍会被回写。
    // ⚠ 不能只算「过滤后还剩几个」—— 那样关掉整条功能也照样通过；
    //   必须先确认生产接线在（上一条已钉），再验语义。
    const w = productionWiring()
    assert.ok(w.filtered && w.writesOwn, '生产接线已断 ⇒ 本条无意义，先修接线')

    const { byId, plan } = planFor(
      new Map([['B-seg-1', 'p1'], ['B-seg-2', 'p2']]), // 全是当前会议的
      [{ id: 'B-seg-1', speakerLabel: '说话人 1' }, { id: 'B-seg-2', speakerLabel: '说话人 2' }],
    )
    const own = plan.updates.filter((u) => byId.has(u.segId))
    assert.ok(own.some((u) => u.segId === 'B-seg-2'),
      `聚类把 p2 并进 p1 后，当前会议自己的 B-seg-2 应当被改写，实得 ${JSON.stringify(plan.updates)}` +
      ' ⇒ 有人把整条 relabel 关掉了。')
  })

  it('负控：用户亲手改过名的段仍然不被自动覆盖（§40 修回来的东西不许回退）', () => {
    const { byId, plan } = planFor(
      new Map([['B-seg-1', 'p2'], ['B-seg-2', 'p2']]),
      // B-seg-1 是用户自己起的名 ⇒ 不是占位标签 ⇒ 不得被覆盖
      [{ id: 'B-seg-1', speakerLabel: '张伟' }, { id: 'B-seg-2', speakerLabel: '说话人 2' }],
    )
    const own = plan.updates.filter((u) => byId.has(u.segId))
    assert.equal(own.some((u) => u.segId === 'B-seg-1'), false,
      '用户改过的名字被自动聚类覆盖了 ⇒ §40 那个缺陷复发')
    assert.ok(own.some((u) => u.segId === 'B-seg-2'),
      '占位标签的那条反而没被改写 ⇒ 占位守卫失效')
  })
})