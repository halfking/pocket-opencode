/**
 * z-index-ladder.test.mjs — 层级阶梯门禁。
 *
 * `styles/tokens.css` 自己写着「全局唯一权威。新增浮层必须在此登记，
 * **禁止散落字面值**」。这句话在 2026-10-04 之前是**没有强制力的**——
 * 全仓实测 23 处裸数字 z-index、2 处引用未定义 token、3 处 fallback 与
 * token 取值冲突。逐条列在这里是因为它们各自都长得像「无害的本地值」：
 *
 *  ① 未定义 token（真缺陷）：`var(--z-popover, 30)`，而 `--z-popover`
 *     从未定义 ⇒ 实际取 30，**低于** `--z-sticky`(50) 与 `--z-bottom-nav`(70)，
 *     tooltip 被吸顶栏与底栏盖住。
 *  ② fallback 冲突：`var(--z-fab, 40)`（token 是 60）、`--z-bottom-nav, 20`
 *     （token 是 70）、`--z-sheet, 1000`（token 是 1300）。token 一旦
 *     没加载上，元素就落到**另一个层**——同一件事两处写法。
 *  ③ 裸数字：分两类。见下方 ALLOWLIST。
 *
 * 2026-10-06 补记：原先登记为「待目视核对」的 7 处全屏 `position: fixed`
 * 遮罩已**全部归位到 token**，不再需要目视核对——它们各自的源码注释白纸黑字
 * 写着「同 BottomSheet.vue」，即作者本意就是 sheet 层；而静态分析确认祖先链
 * `#app / .app-root / .app-layout / main / .content / .top-bar` **均不创建
 * 层叠上下文**，所以它们与底栏/吸顶栏同处一个根层叠上下文，40/50/80/999/1000
 * 全部偏低（Playwright 最小复现：z=40 时 `elementFromPoint` 返回 `header.top-bar`，
 * z=60 时返回 `div.bind-overlay`）。ALLOWLIST 里因此不再有任何「待目视核对」。
 *
 * ⚠️ ALLOWLIST 的 key 曾长期钉在**声明所在行**，往文件上方加一行注释就会让 key 漂移，
 *   于是「改了一处无关代码」会把本门禁变成红的。历史咬人记录：
 *     ① 2026-10-06 给 `EmailInboxView.showLocal` 加 10 行注释，667/730 → 684/747；
 *     ② 同文件迁移到内核（删近 100 行）后 → 664/727 —— 一次会话内三次；
 *     ③ 2026-10-07 给 `SettingsSTT` 加「录音语音提示开关」，604 → 634；
 *     ④ 2026-10-08 给 `SettingsSTT` 加 MiniMax 区块与 radio 样式，634 → 801。
 *   前三次都只是把行号手改回去 —— **治标**。第四次改成**规则块起始行**，
 *   与 `bottom-chrome-gate.test.mjs` 早已在用的约定一致（那里 `where` 就是
 *   块起始行）。块起始行仍会漂，但只在**规则块本身增删**时漂，
 *   而不再是「上方加一行注释就漂」。
 *
 * ★ 2026-10-29（第八次咬人后的根治）：§948 之后 EmailInboxView 又被加 3 行注释，
 *   738/825 → 741/828 —— 块起始行也撑不住了。**key 全面迁到 `文件:选择器`**
 *   （对插入免疫），`collect()` 补齐单行规则的 selector 提取，陈旧条目判据
 *   改查「选择器还活着且块里有 z-index」。行号 key 在 positionAt 的探针里
 *   仍兼容（`文件:数字` 形态），ALLOWLIST 本身不再收行号 key。
 *
 * ★ 同源的第二类腐烂：**ALLOWLIST 的理由会与代码脱节**。行号对了不代表
 *   理由还对——本会话里 `EmailInboxView` 两条的理由是从迁移前的旧代码抄的，
 *   迁移后那两行早已换成 `.more-menu` 与 `.to-top`，理由却还写着
 *   「列表项内部标签 / 徽标的兄弟排序」。行号门禁只查「key 存在」，
 *   查不出「理由是否还对得上那个元素」。改动相邻代码时要顺手核对理由。
 *
 * 门禁守：
 *   1. 每个 `var(--z-*)` 都必须是**已定义**的 token（不再允许靠 fallback 蒙混）；
 *   2. 允许保留 fallback，但**必须与 token 取值相等**（不一致即红）；
 *   3. 裸数字 z-index 必须在 ALLOWLIST 里，且每条都带理由；
 *   4. 阶梯按 tokens.css 的**声明顺序**严格递增。
 *
 * ⚠️ 本门禁只管「谁盖谁」。元素**是否该给底栏让位**由
 * `bottom-chrome-gate.test.mjs` 管——两者互补，缺一不可。
 *
 * Run: node --test src/styles/__tests__/z-index-ladder.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync, readdirSync, writeFileSync, mkdirSync, rmSync, rmdirSync } from 'node:fs'
import { join, dirname, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const tokensCss = readFileSync(join(SRC, 'styles/tokens.css'), 'utf8')

/** SSOT：token 名 → 取值，按声明顺序。 */
const TOKENS = new Map()
for (const m of tokensCss.matchAll(/(--z-[a-z-]+):\s*(\d+);/g)) {
  // matchAll 产出的是数组（RegExpMatchArray），**没有** .group() 方法。
  // 这里用下标取值；写成 m.group(1) 会得到 "m.group is not a function"。
  TOKENS.set(m[1], Number(m[2]))
}

/**
 * 允许的裸数字 z-index —— **每条都要写清为什么它不是全局层**。
 *
 * 判别标准：这个值是否要跟全局浮层（导航/弹层/吸顶栏）比较？
 *  - 否 ⇒ 组件自己 stacking context 内的兄弟排序，局部值合法。
 *  - 是 ⇒ 必须登记为 token。
 *
 * 2026-10-06：这里曾有 8 条「待目视核对」的全屏遮罩，现已全部归位（见文件头）。
 * 现在的每一条都是**局部兄弟排序**——即在组件自有 stacking context 内、
 * 不与全局 chrome 比较的合法局部值。
 */
const ALLOWLIST = {
  // （这 5 条 2026-10-29 已迁到下方「文件:选择器」key；行号锚的全部历史见下方记录。）
  // ⚠️ 这两个 key 已被行号漂移咬到**七次**：
  //   ① 2026-10-06 给 `EmailInboxView.showLocal` 加 10 行注释，667/730 → 684/747；
  //   ② 同文件迁移到内核（删近 100 行）后 → 664/727；
  //   ③ 2026-10-07 给 `SettingsSTT` 加「录音语音提示开关」，604 → 634；
  //   ④ 2026-10-08 给 `SettingsSTT` 加 MiniMax 区块与 radio 样式，634 → 801；
  //   ⑤–⑥ §841 / §854（见上方注释里的历史咬人记录）；
  //   ⑦ 2026-10-21（§948）：§948 给 EmailInboxView 的
  //      `pullInboxFromServer().catch(() => {})` 补了一句理由注释（+3 行），
  //      735/822 → 738/825（键钉的是**规则块起始行**，`.to-top` 是多行块，起始行在 825）。
  // 结论已经不是「治标」的问题了：**「规则块起始行」这个第三种锚法在
  // 一个会被反复加注释的 <style> 上方同样会漂**。
  //   **建议（待属主拍板）**：key 改成 `文件:选择器`（如 `EmailInboxView.vue:.more-menu`），
  //   对插入免疫；本轮只按既有先例重新编号，不改这道门的语义与结构。
  // ★ 2026-10-29：key 从「文件:块起始行」改为「文件:选择器」（采纳上方待拍板的建议）。
  //   直接诱因：§948 之后 EmailInboxView 又被加了 3 行注释，738/825 → 741/828
  //   —— 同一个 key **第八次**咬人。行号锚（无论声明行还是块起始行）在
  //   一个会被反复编辑的 <style> 上方必然继续漂；选择器锚对插入免疫。
  //   配套：collect() 补齐了**单行规则**的 selector 提取（原实现只认多行规则，
  //   单行规则会往上行抓到 `<style scoped>` 之类的垃圾值）。
  //   ⚠ 同名选择器在**同一文件**里出现多个块各自带裸 z-index 时，
  //     「文件:选择器」必须写成多份（key 唯一，选择器相同 ⇒ 各配一条理由）；
  //     目前没有这种情况，若出现，第一个未匹配项会直接报「未登记」，不会静默。
  'components/interactive/SwipeableListItem.vue:.swipe-content': '卡片内删除按钮在内容之上的兄弟排序（组件自有 stacking context）',
  'components/interactive/SwipeableListItem.vue:.swipe-actions': '同上，absolute；内容与按钮的局部前后关系',
  'components/interactive/PullToRefresh.vue:.refresh-indicator': '下拉指示器 absolute，在容器内的局部层级',
  'components/interactive/VoiceCommandAssistant.vue:.voice-icon': '面板内子项的局部排序',
  'components/interactive/DualScreenLayout.vue:.resize-handle': '.resize-handle 在面板内的局部排序（absolute，非 fixed）',
  'features/email/EmailInboxView.vue:.more-menu': '`.more-menu` 顶栏「更多」下拉菜单：absolute，需盖住同层的顶栏按钮（position: static，无层级）',
  'features/email/EmailInboxView.vue:.to-top': '`.to-top` 回顶按钮：absolute 悬浮，需盖住列表内容（静态流，无层级）',
  'features/email/EmailSettingsView.vue:.page-head': '设置行内部控件的兄弟排序',
  'features/settings/SettingsSTT.vue:.top-bar': '⚠️ 页内**自备的吸附顶栏**（该路由 meta.hideAppHeader:true，壳层顶栏被隐藏），position:sticky + z-index:10，**低于** --z-sticky(50) 与 --z-bottom-nav(70) ⇒ 不会压住全局 chrome。⚠️ 2026-10-06 更正：原理由写「STT 面板内局部排序」，但它不是面板内兄弟排序，而是要跟全局 chrome 争层的页内顶栏 —— 两类风险，理由不能混。⚠️ 2026-10-29：key 从行号（634→800，曾四次漂移）改为选择器锚。',
  'styles.css:.nav-push-enter-active': '路由过渡 enter 态的局部前后关系',
  'styles.css:.nav-push-leave-active': '同上',
  'styles.css:.nav-pop-enter-active': '同上',
  'styles.css:.nav-pop-leave-active': '同上',
  'features/email/EmailSpamCleanupView.vue:.page-head': '页内 sticky 小节标题，低于全局吸顶栏',
  'features/email/EmailAccountAddView.vue:.page-head': '页内 sticky 小节标题，低于全局吸顶栏',
}

/**
 * 收集所有 z-index 使用点。
 *
 * ⚠️ 这里必须靠**父目录的 withFileTypes 列表**区分文件与目录。
 * 早期实现对每个条目都 `readdirSync(full)`，对**文件**会抛 ENOTDIR；
 * 于是 `catch { continue }` 把所有 .vue/.css 全跳过了，USES 恒为空 ——
 * 「0 处 z-index」这个假象让 ALLOWLIST 全部被判成陈旧条目。
 * 量具自己报的数要先自证非空。
 */
function collect() {
  const out = []
  const files = []
  const walk = (dir) => {
    let entries
    try {
      entries = readdirSync(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const e of entries) {
      if (['__tests__', 'node_modules', 'android', 'ios', 'dist'].includes(e.name)) continue
      const full = join(dir, e.name)
      if (e.isDirectory()) walk(full)
      else if (/\.(vue|css)$/.test(e.name)) files.push(full)
    }
  }
  walk(SRC)
  for (const full of files) {
    // ⚠️ 必须把分隔符统一成 `/`。2026-10-04 实测踩到：
    //   files 是用 join() 收集的，Windows 上得到 `features\email\EmailInboxView.vue`
    //   （反斜杠），而 ALLOWLIST 的 key 写的是正斜杠 ⇒ 两者永不相等 ⇒
    //   15 条 key 里有 11 条被判成「陈旧条目」，其中 4 条 styles.css 反而因为
    //   相对路径里没有分隔符而侥幸匹配。失败现场看起来像「行号漂移」，
    //   实际是**分隔符不一致**—— 逐行核对过，那 11 行的 z-index 都好好地
    //   待在该行上，一个都没漂。
    //   这类 bug 只在 Windows 上复现，Linux CI 上是绿的。
    const rel = full.slice(SRC.length + 1).split(sep).join('/')
    const srcLines = readFileSync(full, 'utf8').split('\n')
    // ★ `where` 改成**规则块起始行**，不再用「声明所在行」（2026-10-08）。
    //
    //   为什么：往文件上方加一行注释/一段样式就会让「声明行」漂移，
    //   于是「改了一处无关代码」把门禁变红。文件头记着这个 key
    //   **一次会话内咬过三次**（604 → 634），本轮咬了第四次
    //   （给 SettingsSTT 加 MiniMax 区块 + radio 样式后 634 → 801）。
    //
    //   「块起始行」比「声明行」稳，但仍会漂 —— 所以只把它当**量具坐标**，
    //   真正稳定的锚点是**选择器**。本门禁同时产出 selector，供
    //   TestAllowlistKeysResolveToLiveSelectors 那条判据守住：
    //   「key 指向的选择器必须还在，且该选择器块里真的有 z-index」。
    //
    //   ⚠ 提取选择器不能只取 `{` 的上一行 —— 实测踩过：`.chat-icon-btn:active`
    //   前面是 `{`、再前面是**注释行**；嵌套写法下上一行是 `}`。
    //   所以向上找第一个**既不是 `{` 也不是注释/空行**的行。
    srcLines.forEach((line, i) => {
      const m = /z-index:\s*([^;]+)/.exec(line)
      if (!m) return
      // 向上找最近的 `{`，那一行是规则块起始行。
      let up = i
      while (up > 0 && !srcLines[up].includes('{')) up -= 1
      const blockLine = up // 0-based
      // ★ 2026-10-29：selector 提取补**单行规则**这一支。
      //   原实现只会往上找（多行规则正确），但本仓大量规则是单行写法
      //   （`.more-menu { position: ...; z-index: 4; ... }`）——`{` 与
      //   选择器在**同一行**，往上找会命中 `<style scoped>` 或注释，
      //   selector 就成了垃圾值。先看块起始行 `{` 之前有没有内容：
      //   有 ⇒ 那就是选择器（单行规则）；没有 ⇒ 再按老办法往上找。
      const pre = srcLines[blockLine].slice(0, srcLines[blockLine].indexOf('{')).trim()
      let selector
      if (pre && !pre.startsWith('@')) {
        selector = pre.replace(/\s*\{.*$/, '').trim()
      } else {
        // 从块起始行继续往上找选择器：跳过 `{`、注释、空行、`@media` 的括号行。
        let selIdx = blockLine - 1
        while (selIdx >= 0) {
          const t = srcLines[selIdx].trim()
          if (t && !t.includes('{') && !t.startsWith('//') && !t.startsWith('*') &&
              !t.startsWith('/*') && !t.startsWith('}') && !t.startsWith('@')) break
          selIdx -= 1
        }
        selector = selIdx >= 0 ? srcLines[selIdx].trim().replace(/\s*\{.*$/, '').trim() : ''
      }
      out.push({
        // key 用块起始行：比声明行稳，且与 bottom-chrome-gate 的约定一致。
        where: `${rel}:${blockLine + 1}`,
        declLine: i + 1,
        blockLine: blockLine + 1,
        selector,
        value: m[1].trim(),
      })
    })
  }
  return out
}

const USES = collect()

test('tokens.css 的阶梯按声明顺序严格递增（防止有人插错位置）', () => {
  const seq = [...TOKENS.values()]
  assert.ok(seq.length >= 9, `token 数量异常：${seq.length}`)
  for (let i = 1; i < seq.length; i += 1) {
    assert.ok(
      seq[i] > seq[i - 1],
      `阶梯非递增：第 ${i} 项 ${seq[i - 1]} → ${seq[i]}（${[...TOKENS.keys()][i - 1]} → ${[...TOKENS.keys()][i]}）`,
    )
  }
})

test('① 每个 var(--z-*) 都必须是已定义的 token（不再靠 fallback 蒙混）', () => {
  const bad = USES.filter((u) => /^var\(--z-/.test(u.value)).filter((u) => {
    const name = /var\((--z-[a-z-]+)/.exec(u.value)[1]
    return !TOKENS.has(name)
  })
  assert.deepEqual(
    bad.map((b) => `${b.where} → ${b.value}`),
    [],
    '引用了未定义的 z token（曾经的真缺陷：--z-popover）',
  )
})

test('② 保留的 fallback 必须与 token 取值相等（不一致即「同一件事两处写法」）', () => {
  const bad = []
  for (const u of USES) {
    const m = /^var\((--z-[a-z-]+),\s*(\d+)\)$/.exec(u.value)
    if (!m) continue
    const [, name, fb] = m
    if (!TOKENS.has(name)) {
      bad.push(`${u.where} → ${u.value}（token 未定义）`)
      continue
    }
    if (Number(fb) !== TOKENS.get(name)) {
      bad.push(`${u.where} → ${u.value}（token ${name}=${TOKENS.get(name)}，fallback=${fb}）`)
    }
  }
  assert.deepEqual(bad, [], 'fallback 与 token 取值冲突')
})

test('③ 裸数字 z-index 必须在 ALLOWLIST 里，且每条都写了理由', () => {
  const bad = USES.filter((u) => /^\d+$/.test(u.value))
    .map((u) => selectorKey(u))
    .filter((w) => !ALLOWLIST[w])
  assert.deepEqual(
    bad,
    [],
    '出现未登记的裸数字 z-index；请登记进 tokens.css 的 token，或在 ALLOWLIST 里写明理由',
  )
})

test('ALLOWLIST 本身不得有陈旧条目（指向已不存在的规则块）', () => {
  const live = new Set(USES.map((u) => selectorKey(u)))
  const stale = Object.keys(ALLOWLIST).filter((k) => !live.has(k))
  assert.deepEqual(stale, [], 'ALLOWLIST 里有陈旧条目：对应选择器已不存在（或该规则块里已没有裸 z-index）')
})

test('ALLOWLIST 里不得登记 token 化位置（那是重复登记）', () => {
  const dup = USES.filter((u) => /^var\(--z-/.test(u.value))
    .map((u) => selectorKey(u))
    .filter((w) => ALLOWLIST[w])
  assert.deepEqual(dup, [], '这些位置已用 token，不该同时出现在 ALLOWLIST')
})

test('【变异自测】注入一个未登记 z-index，本门禁必须转红', () => {
  const injected = [...USES, { where: 'injected/probe.vue:1', selector: '.probe', value: '4242' }]
  const bad = injected.map((u) => selectorKey(u)).filter((w) => !ALLOWLIST[w])
  assert.ok(bad.includes('injected/probe.vue:.probe'), '注入的未登记值必须被抓到，否则本自测恒真')
})

// ===========================================================================
// ALLOWLIST 的**理由**必须与元素相符（2026-10-06 新增）
//
// ## 为什么
//
// 本文件头已经写着一条「同源的第二类腐烂」：
//   **ALLOWLIST 的理由会与代码脱节 —— 行号对了不代表理由还对得上那个元素。**
//   而上面那条「不得有陈旧条目」只查 **key 是否存在**，**查不出理由是否还对得上**。
//
// ## 本轮抓到的实例
//
//   `features/settings/SettingsSTT.vue:634`（初记为 604）的理由写「STT 面板内局部排序」，
//   而那个规则的 `position` 是 **sticky** —— 它是**页内自备的吸附顶栏**
//   （该路由 `meta.hideAppHeader: true`），要跟全局 chrome 争层，
//   与「组件自有 stacking context 内的兄弟排序」是**两类风险**。
//   门禁头里那句「现在的每一条都是局部兄弟排序」对它就是假的。
//
// ## 口径
//
// 只要求「**非 static/relative** 的定位要在理由里点名」，
// 不要求理由必须写选择器 —— 后者是文风，不是正确性。
// `relative` 不要求：`position: relative` 不创建层叠上下文（除 z-index 非 auto 时），
// 本仓这些条目都是无 z-index 的普通流内元素。
// ===========================================================================

/** USES 条目 → ALLOWLIST 的 key（`文件:选择器`，2026-10-29 起替代「文件:块起始行」）。 */
function selectorKey(u) {
  return `${u.where.slice(0, u.where.lastIndexOf(':'))}:${u.selector || '?'}`
}

/**
 * 按 ALLOWLIST 的 key 找到规则块的**当前**起始行（0-based）。
 *   - 行号 key（`文件:数字`）—— 兼容历史探针/夹具直接给行。
 *   - 选择器 key（`文件:选择器`）—— 在文件里找「该选择器行 + 其后最近的 `{`」。
 *     同名选择器出现多次时取**第一个带 z-index 的块**；都没有则取第一个块。
 */
function blockLineFor(key) {
  const cut = key.lastIndexOf(':')
  const rel = key.slice(0, cut)
  const tail = key.slice(cut + 1)
  let text
  try { text = readFileSync(join(SRC, rel), 'utf8') } catch { return -1 }
  const lines = text.split('\n')
  if (/^\d+$/.test(tail)) return Number(tail) - 1
  let firstBlock = -1
  for (let i = 0; i < lines.length; i += 1) {
    if (!lines[i].trim().startsWith(tail.trim())) continue
    let up = i
    while (up < lines.length && !lines[up].includes('{')) up += 1
    if (up >= lines.length) continue
    if (firstBlock < 0) firstBlock = up
    if (lines.slice(i, up + 1).some((l) => /z-index\s*:/.test(l))) return up
  }
  return firstBlock
}

/** 取该行所在 CSS 规则块里的 `position`（找不到就是 null）。 */
function positionAt(where) {
  const blockLine = blockLineFor(where)
  if (blockLine < 0) return null
  const cut = where.lastIndexOf(':')
  const rel = where.slice(0, cut)
  const p = join(SRC, rel)
  let text
  try { text = readFileSync(p, 'utf8') } catch { return null }
  const lines = text.split('\n')
  let up = blockLine
  // ⚠️ 必须**按规则块收尾**，不能「从 '{' 往后扫 N 行」——
  //   那样会把**后面别的规则**的 position 错安到这一条上。
  //   实测踩到：`styles.css:272` 的 `.nav-push-enter-active` **根本没有 position**，
  //   而它后面 40 行里有 sticky 的规则 ⇒ 扫窗法会给出一个纯属捏造的定位。
  let depth = 0
  let started = false
  for (let i = up - 1; i < lines.length; i += 1) {
    const L = lines[i]
    depth += (L.match(/\{/g) || []).length
    depth -= (L.match(/\}/g) || []).length
    if (depth > 0) started = true
    if (started && depth <= 0) break
    if (i - (up - 1) > 60) break           // 兜底：不让一个坏文件把自检卡死
    const m = /position\s*:\s*(sticky|fixed|absolute|relative)/.exec(L)
    if (m) return m[1]
  }
  return null
}

test('ALLOWLIST 的理由必须与元素的 position 相符（行号对了不代表理由还对）', () => {
  const bad = []
  for (const [where, why] of Object.entries(ALLOWLIST)) {
    const pos = positionAt(where)
    if (!pos || pos === 'relative') continue
    // ⚠️ 必须**先剥掉 --z-* 的 token 名**再查。
    //   实测踩到：把 SettingsSTT 的理由改成「…position: fixed，**低于** --z-sticky(50)…」，
    //   明明把 sticky 删了，判据却因为理由里还留着 **--z-sticky** 这个 token 名而放行 ——
    //   也就是说它接受的是一个**与本元素无关的 token 名**，不是真的声明了定位。
    const said = why.replace(/--z-[a-z0-9-]+/g, '')
    if (!said.includes(pos)) bad.push(`${where}（position:${pos}）理由没提定位：「${why}」`)
  }
  assert.deepEqual(bad, [],
    '这些条目的理由与元素的实际定位不符。sticky / fixed / absolute 的层级风险与' +
    '「组件自有 stacking context 内的兄弟排序」**不是同一类**，写错会让后来人' +
    '按错误的理由去改或去继承。')
})

test('【量具自证】positionAt 必须按**规则块**收尾（不能扫到下一条规则去）', () => {
  // ⚠️ 第一版自证拿**真实文件**当探针：把 `styles.css:272` 期望成 'absolute'。
  //   但那个规则块**根本没有 position 声明**，真实答案是 null —— 我的期望值是拍的。
  //   更糟：后来拿「去掉收尾」当变异，发现**它也不红** ——
  //   因为那处后面 60 行里本来就没有别的 position，**变异选错了探针位置**。
  // ⇒ 换成**合成夹具**：同一个文件里，前一条无 position、后一条 sticky。
  //   只有「按块收尾」的实现才会对第一条返回 null。
  const dir = join(SRC, '__probe_fixture__')
  const file = join(dir, 'ladder-probe.css')
  mkdirSync(dir, { recursive: true })
  try {
    writeFileSync(file, [
      '.probe-a {',
      '  z-index: 3;',        // ← 行 2：自身**没有** position
      '}',
      '.probe-b {',
      '  position: sticky;', // ← 紧邻的下一条才有 sticky
      '  z-index: 4;',
      '}',
    ].join('\n'), 'utf8')
    assert.equal(positionAt('__probe_fixture__/ladder-probe.css:2'), null,
      '`.probe-a` 自身没有 position 声明，positionAt 必须返回 null。' +
      '返回 ' + JSON.stringify(positionAt('__probe_fixture__/ladder-probe.css:2')) +
      ' ⇒ 它把**下一条规则**的定位错安过来了（按块收尾失效）')
    assert.equal(positionAt('__probe_fixture__/ladder-probe.css:6'), 'sticky',
      '正向：`.probe-b` 自己就是 sticky，必须读得出来（否则上面那条是恒真的）')
  } finally {
    try { rmSync(file, { force: true }); rmdirSync(dir) } catch { /* 清理失败不影响判据结论 */ }
  }

  // 真实文件上的阴性对照：那个规则块**确实**没有 position
  assert.equal(positionAt('styles.css:271'), null,
    '`.nav-push-enter-active` 没有 position 声明，必须返回 null')
  // 真实文件上的阳性对照
  // ⚠ 2026-10-08：坐标曾写死 634（SettingsSTT 顶栏），加区块后漂到 800，第四次咬人。
  // ⚠ 2026-10-29：ALLOWLIST 全面迁到选择器锚，探针也改用选择器 key
  //   （行号 key 仍兼容，上一行阴性对照 `styles.css:271` 就是行号形态）。
  assert.equal(positionAt('features/settings/SettingsSTT.vue:.top-bar'), 'sticky',
    '没读出 SettingsSTT 顶栏的 position: sticky')
  assert.equal(positionAt('features/email/does-not-exist.vue:1'), null,
    '文件不存在时必须返回 null 而不是抛错')
})
