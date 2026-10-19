/**
 * relative-time.test.ts — 列表行相对时间与会议时长的纯函数测试。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/utils/__tests__/relative-time.test.ts
 *
 * 背景：仓库里此前有三份各自硬编码中文的"X 分钟前"（NoteCard / EmailCard /
 * SessionCard）。本轮把两个新 Hub 接进来时不能再复制第四份，于是抽了这个
 * 带 i18n 的公共实现。用例重点锁三类容易错的地方：单位换算的边界、
 * 未来时间（不能显示「-3 分钟前」）、以及 7 天后回退到月/日的宽度。
 */
import { strict as assert } from 'node:assert'
import { describe, it } from 'node:test'

import { formatDuration, formatRelative, toEpochSeconds } from '../relative-time.ts'
import { readFileSync, readdirSync } from 'node:fs'
import { join, relative } from 'node:path'

/** 剥掉注释：判据锚点必须落在真代码上，不能落在自己写的说明文字上。 */
const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

/** 假翻译：把 key 直接变成可断言的串，省掉对 vue-i18n 的依赖。 */
const t = (key: string, named?: Record<string, unknown>) =>
  named ? `${key}:${JSON.stringify(named)}` : key

const NOW_MS = 1_700_000_000_000
const nowSec = Math.floor(NOW_MS / 1000)

describe('formatRelative', () => {
  it('60 秒内 → justNow', () => {
    assert.equal(formatRelative(nowSec - 5, t, NOW_MS), 'timefmt.justNow')
    assert.equal(formatRelative(nowSec - 59, t, NOW_MS), 'timefmt.justNow')
  })

  it('恰好 60 秒算「1 分钟前」而不是「刚刚」', () => {
    // 边界值：写成 `<` 而不是 `<=` 会让 59s→1min、60s→justNow 出现 1 分钟错位。
    assert.equal(formatRelative(nowSec - 60, t, NOW_MS), 'timefmt.minutesAgo:{"count":1}')
  })

  it('分钟 / 小时 / 天 的进位正确', () => {
    assert.equal(formatRelative(nowSec - 59 * 60, t, NOW_MS), 'timefmt.minutesAgo:{"count":59}')
    assert.equal(formatRelative(nowSec - 60 * 60, t, NOW_MS), 'timefmt.hoursAgo:{"count":1}')
    assert.equal(formatRelative(nowSec - 23 * 60 * 60, t, NOW_MS), 'timefmt.hoursAgo:{"count":23}')
    assert.equal(formatRelative(nowSec - 24 * 60 * 60, t, NOW_MS), 'timefmt.daysAgo:{"count":1}')
    assert.equal(formatRelative(nowSec - 6 * 24 * 60 * 60, t, NOW_MS), 'timefmt.daysAgo:{"count":6}')
  })

  it('未来时间按「刚刚」处理，不显示负数', () => {
    // 服务器时钟超前 / nextDueAt 这类场景。显示「-3 分钟前」对用户没有意义。
    assert.equal(formatRelative(nowSec + 180, t, NOW_MS), 'timefmt.justNow')
  })

  it('7 天以上回退到「月/日」，宽度可控', () => {
    const out = formatRelative(nowSec - 30 * 24 * 60 * 60, t, NOW_MS)
    assert.match(out, /^\d{1,2}\/\d{1,2}$/)
  })

  it('0 / 负数时间戳返回空串（不渲染「刚刚」骗人）', () => {
    // 缺失时间戳 ≠ 刚刚发生。LocalNote.createdAt 在导入路径上可能是 0。
    assert.equal(formatRelative(0, t, NOW_MS), '')
    assert.equal(formatRelative(-1, t, NOW_MS), '')
  })
})

describe('formatDuration', () => {
  it('不足 1 小时 → M:SS', () => {
    assert.equal(formatDuration(0), '')
    assert.equal(formatDuration(5_000), '0:05')
    assert.equal(formatDuration(65_000), '1:05')
    assert.equal(formatDuration(59 * 60_000 + 59_000), '59:59')
  })

  it('满 1 小时 → H:MM:SS，且秒位补零', () => {
    assert.equal(formatDuration(60 * 60_000), '1:00:00')
    assert.equal(formatDuration(3661_000), '1:01:01')
  })

  it('负数 / 非有限值返回空串', () => {
    assert.equal(formatDuration(-1), '')
    assert.equal(formatDuration(NaN), '')
  })
})

describe('toEpochSeconds', () => {
  // 这组用例是审计发现的真实缺陷的钉子。
  //
  // 本地库的 *_at 单位不统一：LocalNote.updatedAt / LocalMeeting.startedAt /
  // PkmNote.updatedAt / LocalEmail.date 都是**毫秒**（写入用 Date.now()），
  // 而 Notification.created_at 与 RSS 的解析结果是**秒**。
  // 混进 formatRelative 时 diff 恒为大负数 → 每一行都显示「刚刚」；
  // 混进排序时毫秒项恒大于秒项 → 「统一时间线」变成按来源分组的假时间线。
  // 两种错都**不报错**，只是看起来不对，是最难被验收抓到的一类。
  const NOW_SEC = 1_700_000_000
  const NOW_MS = NOW_SEC * 1000

  it('毫秒 → 秒（笔记 / 会议 / PKM / 邮件四个来源）', () => {
    assert.equal(toEpochSeconds(NOW_MS), NOW_SEC)
  })

  it('秒 → 秒，原样（任务消息 / RSS 两个来源）', () => {
    assert.equal(toEpochSeconds(NOW_SEC), NOW_SEC)
  })

  it('同一真实时刻的毫秒与秒表示归一后相等 —— 这正是排序能混排的前提', () => {
    assert.equal(toEpochSeconds(NOW_MS), toEpochSeconds(NOW_SEC))
  })

  it('归一后 formatRelative 对两种单位给出一致结果', () => {
    const t = (k: string, n?: Record<string, unknown>) => (n ? `${k}:${JSON.stringify(n)}` : k)
    const asMs = formatRelative(toEpochSeconds(NOW_MS - 3 * 86400 * 1000), t, NOW_MS)
    const asSec = formatRelative(toEpochSeconds(NOW_SEC - 3 * 86400), t, NOW_MS)
    assert.equal(asMs, asSec)
    assert.equal(asMs, 'timefmt.daysAgo:{"count":3}')
  })

  it('阈值两侧按同一条规则判定（与 emails-store 的 emailDateToMs 对齐）', () => {
    // 1e12 毫秒 = 2001-09-09，1e12 秒 = 公元 33658 年。
    // 所以本 App 会产出的任何数据（2020 年以后，ms ≥ 1.58e12）都稳稳落在
    // 阈值之上，毫秒/秒判别不会出错。
    //
    // **这条判据的真实边界要说准**：它不是精确的单位探测。
    // 2001-09-09 之前的**毫秒**时间戳（< 1e12）会被误判成秒。
    // 那是 2001 年的数据，本产品不可能产生，仓库里也没有——
    // 所以这里接受这个边界，但把它钉成显式用例，免得将来有人
    // 把阈值调小或改判据时不知道自己在动什么。
    assert.equal(toEpochSeconds(1_000_000_000_000), 1_000_000_000) // == 1e12 → 视为毫秒
    assert.equal(toEpochSeconds(1_700_000_000_000), 1_700_000_000) // 2023 ms
    assert.equal(toEpochSeconds(1_700_000_000), 1_700_000_000)      // 2023 s
    // 已知边界：小于阈值的毫秒会被误判为秒（2001 年以前才会出现）
    assert.equal(toEpochSeconds(978_307_200_000), 978_307_200_000)
  })

  it('0 / 负数 / NaN / Infinity → 0（无效值不得被当成纪元）', () => {
    assert.equal(toEpochSeconds(0), 0)
    assert.equal(toEpochSeconds(-1), 0)
    assert.equal(toEpochSeconds(NaN), 0)
    assert.equal(toEpochSeconds(Infinity), 0)
  })
})

/**
 * §885 D55：「时长格式化」是一个**有 6 个同名实现、2 种单位、5 种输出格式**的概念，
 * 而仓里对它的**单位契约**在注释层面就是错的。
 *
 * 普查读数（`formatDuration` 全仓 6 处）：
 *
 * | 位置                          | 参数单位 | 输出形态          | 负数 | 非有限 |
 * |-------------------------------|----------|-------------------|------|--------|
 * | utils/relative-time.ts:87     | 毫秒     | `M:SS` / `H:MM:SS`| ✅   | ✅     |
 * | features/tasks/health.ts:63   | 毫秒     | 刚刚/N秒/N分/N时  | ✅   | ✅     |
 * | features/meetings/meeting-list.ts:39 | 毫秒 | `N分S秒`      | ✅   | ⚠ 无   |
 * | features/sessions/RoundTimeline.vue:185 | 毫秒 | `123ms/1.2s/3m 5s` | ❌ | ❌ |
 * | features/opencode/SessionDetailView.vue:300 | **秒** | `N 小时 M 分钟` | ❌ | ❌ |
 * | features/opencode/SessionListView.vue:211   | **秒** | `N 小时 M 分钟` | ❌ | ❌ |
 *
 * D55 两条：
 *   ① `utils/relative-time.ts::formatDuration` 的 JSDoc 原文写「**秒** → M:SS」，
 *      而实现收的是**毫秒**（`Math.round(ms / 1000)`，参数名一直是 `ms`）。
 *      ⚠ 它错在**唯一该声明单位**的位置上：同一文件里 `formatRelative` 的
 *      `@param ts` 明写「Unix 秒」、`toEpochSeconds` 又把「秒」定义为归一后的单位
 *      ⇒ **同一个文件里「秒」有两个互相矛盾的定义**。
 *      而 `NotesHubView.vue:391` 的注释记着真有人照本文件的输出形状做过字符串解析
 *      ⇒ 这一段确实被当成契约读过。
 *   ② `RoundTimeline` 与 opencode 两个**都缺负数/非有限防护**。
 *      `RoundTimeline` 的 `c.durationMs` 来自 `useSessionEvents` 归约后的
 *      **外部 session.activity 事件**（`durationMs?: number`，无校验）；
 *      模板的 `v-if="c.durationMs"` 挡得住 0/undefined，**挡不住负数**。
 *
 * ⚠ 阴性结论（别重复查）：opencode 两个**参数名已经是 `seconds`**，与另 4 处
 *   的 `ms` 区分开了 ⇒ 它们**当前不会**被误用；⛔ 未实测其数据来源的单位，
 *   故**不改名**（改名要断言单位，无据不改）。本节只补防护。
 */
describe('§885 · 时长格式化的单位与防护', () => {
  // ⚠⚠ 扫描根必须是 `src/`，即从 `src/utils/__tests__/` **上跳两级**。
  //   第一版写成 `new URL('../../', import.meta.url)`（只上跳一级 ⇒ 落在 `src/utils/`），
  //   实测只扫到 1 处 formatDuration，而下面两条断言**照样绿** ——
  //   也就是说「漏扫」不会让它们喊。这不是小失误：
  //   **假绿比假红贵**，它会让你以为全仓都查过了。
  //   ⇒ 所以「扫够了没有」不是一条独立用例，而是**每条断言的前置**（见 assertScanned）。
  const SRC_ROOT = new URL('../../', import.meta.url).pathname

  /** 递归列出源码文件（只扫一层会漏两级目录 —— §880.6 那条）。 */
  const walk = (dir: string, out: string[] = []): string[] => {
    for (const name of readdirSync(dir, { withFileTypes: true })) {
      const p = join(dir, name.name)
      if (name.isDirectory()) {
        if (name.name === '__tests__' || name.name === 'node_modules') continue
        walk(p, out)
      } else if (/\.(ts|vue)$/.test(name.name) && !/\.test\.ts$/.test(name.name)) out.push(p)
    }
    return out
  }

  /** 剥注释后取 `function formatDuration(...)` 的函数体（逐字符配平，不用正则跨结构）。 */
  const bodiesOf = (): { file: string; params: string; body: string }[] => {
    const out: { file: string; params: string; body: string }[] = []
    for (const abs of walk(SRC_ROOT)) {
      const code = strip(readFileSync(abs, 'utf8'))
      const re = /function\s+formatDuration\s*(?:<[^>]*>)?\s*\(/g
      let m: RegExpExecArray | null
      while ((m = re.exec(code))) {
        let depth = 0
        let end = -1
        for (let i = m.index + m[0].length - 1; i < code.length; i++) {
          if (code[i] === '(') depth++
          else if (code[i] === ')') { depth--; if (depth === 0) { end = i; break } }
        }
        const braceAt = code.indexOf('{', end)
        if (braceAt < 0) continue
        let d = 0
        let close = -1
        for (let i = braceAt; i < code.length; i++) {
          if (code[i] === '{') d++
          else if (code[i] === '}') { d--; if (d === 0) { close = i; break } }
        }
        if (close < 0) continue
        out.push({
          file: relative(SRC_ROOT, abs),
          params: code.slice(end + 1, braceAt).trim(),
          body: code.slice(braceAt, close + 1),
        })
      }
    }
    return out
  }

  const bodies = bodiesOf()

  /**
   * 「扫够了没有」的前置闸。
   *
   * ⭐ 为什么不能只做成一条独立用例：单独那条绿着，而后面两条在**漏扫出来的
   * 子集**上照样绿（第一版实测：只扫到 1 处，两条断言全绿）。
   * ⇒ 漏扫必须让**每一条**依赖全量扫描的断言都红，所以它是前置而不是同伴。
   */
  const assertScanned = (): void => {
    assert.ok(bodies.length >= 6,
      `只扫到 ${bodies.length} 处 formatDuration（应为 6）：${bodies.map((b) => b.file).join(', ')}\n` +
      '扫描器本身坏了 —— 下面几条会在子集上假绿。')
  }

  it('★★★ 阳性对照：扫描器至少找到 6 处 formatDuration（否则下面几条都是空的）', () => {
    assertScanned()
  })

  it('★★★ 每个 formatDuration 都必须显式处理「非有限」与「负数」', () => {
    assertScanned()
    // 钉性质：负数时长在任何语义下都是错的，而 `!x` 挡不住它（-1 是 truthy）。
    //   这正是 utils/relative-time.ts 早为 formatRelative 写下的原则，
    //   health.ts 照做了，RoundTimeline 与 opencode 两个此前漏了。
    for (const b of bodies) {
      assert.match(b.body, /Number\.isFinite\(/,
        `${b.file}::formatDuration 没有显式判有限值 ⇒ NaN/Infinity 会被原样渲染成时长（D55）`)
      assert.match(b.body, /<\s*0|>\s*=\s*0|<\s*=\s*0/,
        `${b.file}::formatDuration 没有显式判负数/零 ⇒ 负值会落进计算并伪装成「时长为零」（D55）`)
    }
  })

  it('★★★ formatDuration 的单位只能写在注释里一次，且必须与实现的换算一致', () => {
    // 本条钉的是「注释不许再声明与实现相反的单位」。
    // ⚠ 只能对**有 JSDoc 的导出实现**判（opencode 两个是 .vue 里的本地函数，
    //   仓里不给它们写文档是既有状态，本节不代拍）⇒ 范围据实限定并标注。
    const src = readFileSync(new URL('../relative-time.ts', import.meta.url), 'utf8')
    const at = src.indexOf('export function formatDuration(')
    assert.ok(at > 0, '★ 阳性对照失败：找不到 relative-time.ts::formatDuration')
    const doc = src.slice(0, at)
    const lastBlock = doc.lastIndexOf('/**')
    assert.ok(lastBlock >= 0, '★ 阳性对照失败：该导出函数没有 JSDoc')
    const block = doc.slice(lastBlock)
    // 实现收毫秒（体内有 /1000）⇒ 注释里就不得把入参说成「秒」。
    const body = src.slice(at, src.indexOf('\n}', at))
    assert.match(body, /ms\s*\/\s*1_?000/, '★ 阳性对照失败：实现里找不到 ms/1000 换算，本条前提不成立')
    assert.doesNotMatch(block, /^\s*\*\s*秒\s*→/m,
      'JSDoc 第一行仍把入参声明成「秒」，而实现收的是毫秒 —— ' +
      '同一文件里 toEpochSeconds/formatRelative 都以「秒」为归一单位，这是第二套定义（D55）')
    assert.match(block, /毫秒/, 'JSDoc 必须写明收毫秒（这是本文件唯一的单位声明位置）')
  })

  it('★★ 反向断言：新增 formatDuration 必须是有意识的决定', () => {
    // 与 §880/§883 同一条纪律 —— 阴性结论会过期，清单要有反向断言。
    //
    // ⚠ 第一版数的是**去重后的文件数**，实测被顶穿：在 health.ts 里再写一个
    //   局部 `function formatDuration` 时文件数仍是 6 ⇒ 这条照样绿。
    //   ⇒ 改成数**定义总数**（新文件、新增局部定义都会把它顶红）。
    assert.equal(bodies.length, 6,
      `formatDuration 的实现变成 ${bodies.length} 处：${bodies.map((b) => b.file).join(', ')}\n` +
      '新增同名实现前请先确认单位一致，或在上面那条断言里显式豁免并写明理由。')
  })
})
