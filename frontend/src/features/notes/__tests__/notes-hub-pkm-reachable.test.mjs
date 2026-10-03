// notes-hub-pkm-reachable.test.mjs
//
// 判据：笔记 hub 的「打开 PKM」入口**不能**被 counts.total 门控。
//
// 2026-10-03 真机实测（Xiaomi 2411DRN47C）挖出的可达性缺陷：
// 2026-10-03 全局 IA 重组把一级 tab 收敛为 首页 / 笔记 / 消息 / 更多，
// 「PKM笔记」从底部「更多」的宫格里被移除（MoreHubView 的 items 里已没有它），
// 于是 /pkm/today 只剩 NotesHubView 里一个「打开 PKM」按钮能进。
// 而那一整块 see-all nav 当时挂在 `v-if="counts.total"` 上：
//
//     <EmptyState v-else-if="counts.total === 0" … />   ← 空库走这里
//     <nav v-if="counts.total"> … 打开 PKM … </nav>       ← 空库不渲染
//
// ⇒ 新装 / 空库用户 `counts.total === 0`，界面上**没有任何入口**能到 PKM 工作台，
//   除非他先手写出一条笔记。PKM 是功能，不是「有数据才让你看」的列表。
//
// 为什么用源码结构判据而不是挂载渲染：本仓库的同类门禁（settings-helper-text-tier、
// version-config-unavailable-wiring 等）都是读 .vue 源码判形状，不引入 vue 测试栈。
// 这里要判的恰恰是**模板里的门控关系**，读源码比挂载更直接。
//
// 负控（文件末尾）：把 PKM 那个 nav 改回 v-if="counts.total"，本文件必须转红。

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const SFC = join(here, '..', 'NotesHubView.vue')
const src = readFileSync(SFC, 'utf8')

/** 取某个 go('/xxx') 所在那一行**所属**的 <nav …> 开标签。 */
function owningNavTag(goCall) {
  const at = src.indexOf(goCall)
  assert.notEqual(at, -1, `NotesHubView.vue 里找不到 ${goCall}`)
  // 往上找最近的 <nav 开标签（本文件里 nav 不嵌套）。
  const before = src.slice(0, at)
  const open = before.lastIndexOf('<nav')
  assert.notEqual(open, -1, `${goCall} 上方没有 <nav>`)
  const close = src.indexOf('>', open)
  return src.slice(open, close + 1)
}

describe('NotesHubView 的 PKM 入口可达性', () => {
  it('「打开 PKM」确实渲染在某个 nav 里（判据自身的前提）', () => {
    assert.match(src, /notesHub\.link\.allPkm/, 'PKM 入口文案引用不见了，先确认这条判据还成立')
    assert.match(src, /go\('\/pkm\/today'\)/, 'PKM 入口的跳转不见了')
  })

  it('PKM 入口所在的 nav 没有被 counts.total 门控', () => {
    const navTag = owningNavTag("go('/pkm/today')")
    assert.doesNotMatch(
      navTag,
      /counts\.total/,
      `PKM 入口的 nav 被 counts 门控了：${navTag}\n`
        + '空库用户会因此在界面上找不到 PKM 工作台（2026-10-03 真机实测的缺陷）',
    )
  })

  it('「全部笔记」入口不被 counts.total 门控 —— 它是录音 FAB 的宿主', () => {
    // ⚠️ 这条断言 2026-10-03 17:16 **方向反转过**，反转的理由要留在文件里，
    //    否则下一个人会照着旧注释把它改回去。
    //
    // 本文件原来断言的是「/notes/voice 仍保留 counts 门控」，
    // 理由写着「列表类入口在空库时不该渲染」。那条理由**对 /meetings 成立，
    // 对 /notes/voice 不成立** —— 它漏看了一件事：
    //
    //   /notes/voice 渲染的是 NoteListView，而 NoteListView.vue:113
    //     <VoiceRecorderWidget :recording="isRecording" :busy="recorderUi.busy" @toggle="onMicToggle" />
    //   是**录音 FAB 的唯一挂载点**（.recorder-fab 只此一处）。
    //
    // 而 /notes（底部「笔记」tab，NotesHubView）自己**不渲染任何录音入口**，
    //   它的空态按钮「录音记一笔」走的是 NotesHubView.vue:145
    //     @action="go('/notes/new')"  —— 那是**新建笔记表单**，不是录音器。
    //
    // ⇒ 空库用户的实际体验：笔记 tab 里既没有 FAB，也没有能点开 FAB 的入口，
    //   语音转写整条功能不可达。真机 CDP 对照（Xiaomi 2411DRN47C）：
    //     A. #/notes       see-all nav 只有「打开 PKM」；.recorder-fab = ABSENT
    //     B. #/notes/voice  .recorder-fab 在，aria-label=开始录音，60×60 @(282,674)
    //   B 侧证明目标页本身是好的，缺的只是 A 侧那个入口。
    //
    // 判别力说明：这条判据取的是**门控关系**（正向形状：那个 nav 开标签里
    // 不能有 counts.total），不是「页面上有没有这个词」——后者加个空壳就过。
    const navTag = owningNavTag("go('/notes/voice')")
    assert.doesNotMatch(
      navTag,
      /counts\.total/,
      `「全部笔记」入口的 nav 被 counts 门控了：${navTag}\n`
        + '空库用户会因此在笔记页找不到录音 FAB，语音转写整条功能不可达'
        + '（2026-10-03 真机实测的缺陷，与本文件记录的 PKM 那条同源）',
    )
  })

  it('「全部会议」仍保留 counts 门控（纯列表展开项，没数据时不该出现）', () => {
    const navTag = owningNavTag("go('/meetings')")
    assert.match(
      navTag,
      /counts\.total/,
      `go('/meetings') 所在的 nav: ${navTag}\n`
        + '会议列表是纯展开项，空库时不该渲染（与录音工具入口不同）',
    )
  })
})
