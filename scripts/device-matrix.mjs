#!/usr/bin/env node
/**
 * device-matrix.mjs —— 在**真 Android WebView** 里跑 UI 验收矩阵。
 *
 * 为什么不用 Playwright：Playwright 量的是桌面 Chromium。已实现的几条里
 * UI-01/02 的病因与真机行为有关（硬件返回键），UI-05/12 的病因是
 * 「横向溢出 / 底栏遮挡」，必须量**真机真实布局数值**，
 * 截图肉眼判断不可复现、也不能量。
 *
 * ⚠️ **本文件实际只实现了 4 条**（UI-01 / UI-02 / UI-05a+b / UI-12a–d），
 * 外加量具自证 S1–S3。**UI-03/04/06/07/08/09/10/11 尚未实现。**
 *   ⚠️ 2026-10-06 订正：本文档的头部原本写着「UI-01/02/**06/11** 的病因全部与
 *   真机行为有关」——UI-06/11 **一行都没写**。那是照着验收矩阵表抄的，
 *   没回头核对实现。**声称的覆盖面必须逐条对回代码。**
 *   逐条状态见 docs/UI规范/10-门禁与真机验收.md §4 的状态列。
 *
 * 通道：scripts/lib/adb-cdp.mjs（在 WebView 上下文里跑 JS）。
 * ⚠️ 该模块已修过「静默连错包」的坑（本机同时装正式包与 .sttdev 旁挂包），
 * 所以必须显式传 pkg，且它的 pid 匹配失败时**抛错不回退**。
 *
 * 纪律：每条断言都配**负对照**。一个只会「一律通过」的量具不是量具。
 *
 * 用法：
 *   POCKET_SERIAL=emulator-5554 node scripts/device-matrix.mjs            # 全量
 *   POCKET_SERIAL=emulator-5554 node scripts/device-matrix.mjs ui-05      # 单项
 *   KEEP=1 ...                                                          # 跑完不收栈
 */
import { openCdp } from './lib/adb-cdp.mjs'
import { execFileSync } from 'node:child_process'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, sep, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')

const PKG = process.env.POCKET_PKG || 'com.kaixuan.opencode.pocket.sttdev'
const SERIAL = process.env.POCKET_SERIAL
const ADB = process.env.POCKET_ADB || 'adb'
const only = (process.argv[2] || '').toLowerCase()
// 求值超时：模拟器在宿主高压（load 30+）时 Runtime.evaluate 会超过默认 20s
const EVM = Number(process.env.MATRIX_EV_MS || 60000)
// 本地加密库的主口令。默认沿用 e2e 的固定值（e2e/web/helpers/auth.ts）：
// 受测包若是**全新 applicationId**（如 .matrix），本地库未初始化，
// 用它即可完成首次创建。复用已有数据的包则需要真实口令，从环境传入。
const MASTER_PW = process.env.MATRIX_MASTER_PASSWORD || 'e2e-master-pass-123'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const flat = (s) => String(s ?? '').replace(/\s+/g, ' ').trim()

const results = []
/** 记录一条判定。ok=false 表示红。 */
function record(id, title, ok, evidence) {
  results.push({ id, title, ok, evidence })
  console.log(`${ok ? '  🟢' : '  🔴'} ${id} ${title}`)
  console.log(`       ${evidence}`)
}

/**
 * 记录一条**本轮取不到结论**的判定（第三态）。
 *
 * ⚠️ 为什么必须与 record 分开（2026-10-06 实吃）：UI-06e 原来用 record 记红，
 * 理由「浮层一个都没出现」。但那**不是失败**，是这条判据在这些路由上**永远
 * 不可能被满足**：`.summary-panel` 只在录音激活时渲染（SessionLiveRecordPanel
 * 的 v-if="liveRecord.active"），`.alert-toast` 所属的 MeetingAlertToast.vue
 * 全仓**零引用**（死代码）。
 * ⇒ 一条永不可能绿的判据，红着红着就被无视 —— 那比没有判据更糟：
 * 它训练人「红是常态」，真红来了也不看。
 * ⇒ 未覆盖单独一档：既不算通过，也不混进红里。
 */
function skip(id, title, why) {
  results.push({ id, title, ok: null, evidence: why })
  console.log(`  ⬜ ${id} ${title} —— **未覆盖**`)
  console.log(`       ${why}`)
}

/** 在设备上按一次 Android 硬件返回。 */
const pressBack = () => {
  execFileSync(ADB, ['-s', SERIAL, 'shell', 'input', 'keyevent', '4'], { timeout: 15000 })
}

// ---------------------------------------------------------------------------
// UI-12 的被测元素**出处表**（2026-10-06 新增）
//
// ⚠️ 这张表是被「逐条回源码核对」逼出来的。原来是手写一串选择器：
//     ['.speakers-btn', '.meeting-mic-dock .mic', '.mic-dock .mic',
//      '.meeting-mic-dock', '.toast']
// 逐条核对 frontend/src 之后：
//   · `.meeting-mic-dock .mic` / `.mic-dock .mic` / `.meeting-mic-dock`
//     —— **源码里一个都不存在**。真实根类名就是 `.mic`
//     （features/meetings/MeetingMicDock.vue:4，`<button class="mic">`），
//     作者没给它包一层 dock 容器，量具却假设有一层。
//   · `.speakers-btn` 真实存在，但 `v-if="micOn && speakers.length"`
//     —— 设备上没有麦克风与说话人，永远不出现。
//   · `.toast` 真实存在，但在 vault / 邮箱页，不在会议详情页。
//   · **唯一在详情页常驻的 `.mic` 压根没被列进表里。**
// ⇒ 所以 UI-12 报「本页没有任何被测选择器」。**那不是缺数据，是量具指错了地方**：
//   表里 5 个候选，能命中的 2 个都带条件，而唯一能命中的那个没写。
//
// 因此这张表现在**必须带出处**，出处由 findClassOrigin() 真的去源码里找。
// 找不到就报「无出处」——让改名/重构把这条判据**变红**，而不是安静退化成无结论。
// 一个「无结论」永远不会自己暴露，只有出处丢失会。
// ---------------------------------------------------------------------------
const REPO = join(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(REPO, 'frontend', 'src')

const OCCLUSION_TARGETS = [
  {
    sel: '.mic', cls: 'mic',
    on: '会议详情页常驻（<MeetingMicDock> 无 v-if）',
    expect: 'present',
    why: '它就是底部让位修复的直接受益者：bottom: calc(var(--bottom-chrome-height) + var(--space-4))',
  },
  {
    sel: '.speakers-btn', cls: 'speakers-btn',
    on: 'v-if="micOn && speakers.length"',
    expect: 'absent',
    why: '同样用底部让位定位。设备无麦克风 ⇒ 预期不出现；**若它出现了说明 micOn 被误判成真**，那本身是缺陷',
  },
  {
    sel: '.rec-pill', cls: 'rec-pill',
    on: 'v-if="anyRecordingActive() && !onHostPage"',
    expect: 'absent',
    why: '全局录音胶囊，同用底部让位。无录音 ⇒ 预期不出现',
  },
]

let _blob = null
/** frontend/src 下所有 .vue/.css/.ts 读一遍（只读一次，之后复用）。 */
function srcBlob() {
  if (_blob) return _blob
  const files = []
  const walk = (d) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      const f = join(d, e.name)
      if (e.isDirectory()) { try { walk(f) } catch { /* 权限/竞态，跳过 */ } }
      else if (/\.(vue|css|ts)$/.test(e.name)) files.push(f)
    }
  }
  walk(SRC)
  _blob = files.map((f) => ({ f, rel: f.slice(SRC.length + 1).split(sep).join('/'), text: readFileSync(f, 'utf8') }))
  return _blob
}

/**
 * 某个类名在源码静态 `class="…"` 里到底有没有出处（每个文件只报第一处）。
 *
 * ⚠️ 必须按**词元**精确比对，不能用 `includes`：
 *   `.mic` 是 `.mic-dock` 的前缀，用子串匹配会把**不存在的** `mic-dock`
 *   判成「有出处」—— 而那恰恰是本轮要抓的病。
 *   分类判据的精度必须等于它声称要证明的那个东西的精度。
 *   （同理，Windows 上 join() 给反斜杠，必须 split(sep).join('/') 归一化，
 *    否则路径对不上又会伪装成「出处丢了」—— 与 z-index-ladder 门禁同源。）
 *
 * 只认静态 class：三个目标类名在源码里都是静态写法，
 * `:class="{ recording: recording }"` 这类动态类不影响它们的存在性判断。
 */
function findClassOrigin(cls) {
  const hits = []
  for (const { f, rel, text } of srcBlob()) {
    const lines = text.split('\n')
    for (let i = 0; i < lines.length; i++) {
      const re = /class\s*=\s*"([^"]*)"/g
      let m, found = false
      while ((m = re.exec(lines[i]))) {
        if (m[1].split(/\s+/).filter(Boolean).includes(cls)) { found = true; break }
      }
      if (found) { hits.push({ file: rel, line: i + 1, src: lines[i].trim().slice(0, 78) }); break }
    }
  }
  return hits
}

/**
 * `--selftest`：不碰设备，只验**量具自己**。
 * 三条，缺一不可：
 *   ① 出处：表里每个类名都要在源码里找到 —— 否则量具在量不存在的东西。
 *   ② 阴性对照：注入一个源码里**没有**的类名，必须被判「无出处」。
 *      少了这条，① 恒真也看不出来。
 *   ③ 精度对照：`mic` 必须找到、`mic-dock` 必须**找不到**。
 *      少了这条，一个只会做子串匹配的匹配器也能把 ① 全绿 —— 而子串匹配
 *      正是当初让那 3 个虚构类名混进表里的那种错。
 */
async function runSelectorSelftest() {
  const cases = []
  for (const t of OCCLUSION_TARGETS) {
    const hits = findClassOrigin(t.cls)
    cases.push({ ok: hits.length > 0, name: `出处·${t.sel}`, detail: hits.length ? `${hits[0].file}:${hits[0].line}  ${hits[0].src}` : '源码里找不到该类名' })
  }
  const ghost = 'matrix-ghost-class-does-not-exist'
  const ghostHits = findClassOrigin(ghost)
  cases.push({ ok: ghostHits.length === 0, name: `阴性对照·虚构类名 .${ghost} 必须判无出处`, detail: `命中 ${ghostHits.length} 处（应为 0）` })
  const mic = findClassOrigin('mic'), micDock = findClassOrigin('mic-dock')
  cases.push({
    ok: mic.length > 0 && micDock.length === 0,
    name: '精度对照·mic 有出处而 mic-dock 没有（不许子串匹配）',
    detail: `mic=${mic.length} 处（${mic[0]?.file ?? '—'}），mic-dock=${micDock.length} 处（应为 0）`,
  })
  // ---- UI-06 基准路由的**形态**必须钉住（2026-10-06）----
  // ⚠️ 背景：仓里有 **18 条**路由声明 `meta.hideAppHeader: true`，那类页面
  //   **壳层顶栏整体不渲染**、自备页内头部。本矩阵 UI-06 的基准与 D 场景都固定在
  //   `BASE_HASH = '/#/ai'`（实测：壳层顶栏 `h=48 / sticky`），而自备头部那一类
  //   实测是 `h=65 / static`（`SessionConversationView`）—— **两种形态行为不同**。
  // ⇒ 「吸顶不错位」「高度恒等于 token」这些结论**不能外推**到那 18 条路由。
  //   若有人把 BASE_HASH 换成一条 hideAppHeader 路由，UI-06b/c/d/g 会**静默地
  //   换一个被测对象**继续绿 —— 所以这里把它钉住。
  {
    const routerSrc = readFileSync(join(SRC, 'app', 'router-mobile.ts'), 'utf8')
    // 取 path: '/ai' 之后紧邻的那条 meta，避免匹配到别的路由
    const afterAi = routerSrc.split(/path:\s*'\/ai'/)[1] || ''
    const metaBody = (afterAi.split('\n').slice(0, 8).join('\n').match(/meta:\s*\{([^}]*)\}/) || [])[1] || ''
    const selfHidesHeader = /hideAppHeader:\s*true/.test(metaBody)
    cases.push({
      ok: !selfHidesHeader,
      name: 'UI-06 基准路由 #/ai 必须是「壳层顶栏」形态（meta 不含 hideAppHeader）',
      detail: selfHidesHeader
        ? '⚠️ #/ai 声明了 hideAppHeader ⇒ 壳层顶栏不渲染、页面自备头部；'
          + '而 UI-06 的结论（「高度恒等于 --topbar-height」「吸顶不错位」）'
          + '是针对**壳层顶栏**量出来的，换了被测对象它们会静默地继续绿。'
        : `meta = {${metaBody.trim().slice(0, 80)}} ⇒ 壳层顶栏渲染，UI-06 结论的适用范围正确`,
    })
    const selfCount = (routerSrc.match(/hideAppHeader:\s*true/g) || []).length
    cases.push({
      ok: selfCount > 0,
      name: '量具自证·确实存在「自备头部」形态（否则上面那条钉的是空类）',
      detail: `router-mobile.ts 里 hideAppHeader:true 的路由 ${selfCount} 条；`
        + '这些页面的顶栏**不在本矩阵覆盖范围内**（见 10-门禁与真机验收.md §4.0i-4 订正）',
    })
  }
  // ---- 解锁判定：超时时必须复查设备（2026-10-06 新增，回归靶子）----
  //
  // ⚠️ 本轮真修的量具缺陷（matrix-full10a 的 12 条红）：原来靠
  //   `why === '停在解锁页'` 字符串相等决定要不要解锁，而超时窗口里设备
  //   **其实已在解锁页** ⇒ 返回「无需解锁」⇒ 解锁从不执行 ⇒ 连锁假红。
  //
  // ⚠️⚠️ 判据为什么**调函数**而不是解析源码：我第一版把断言写成
  //   「在自身源码里找 `if (st.why …)` 块」，**连续失败三次**：
  //   ① 锚点 `indexOf('async function unlockIfNeeded')` 命中的是自测代码里
  //      **我自己写的字符串字面量** ⇒ 切出来的是自测自身（自证循环）；
  //   ② raw 与 code 两套文本混用定位/判定 ⇒ 偏移错位 ⇒ **基线就红**；
  //   ③ 按字符窗口取块 ⇒ `waitForShell` 里另一处 `.login-view .unlock-hint`
  //      落在窗口内 ⇒ **变异后仍全绿（恒真）**。
  //   **判据读自己源码时，锚点本身就是最大的坑。** 行为可调用就别去解析文本 ——
  //   所以决策被抽成 `shouldAttemptUnlock`，下面直接调它。
  {
    // ① 正常：明确报「停在解锁页」⇒ 解锁
    const normal = shouldAttemptUnlock('停在解锁页', false)
    cases.push({
      ok: normal,
      name: '解锁判定·明确报「停在解锁页」时必须继续解锁（基线行为）',
      detail: `shouldAttemptUnlock('停在解锁页', false) = ${normal}（期望 true）`,
    })
    // ② ★ 本轮真修的那一条：超时 + 复查确认在解锁页 ⇒ **仍要解锁**
    //    （旧实现在这里返回 false ⇒ 解锁从未执行 ⇒ 12 条连锁假红）
    const timeoutOnPage = shouldAttemptUnlock('超时仍未落在 #/email（实际 #/ai，跑了 40 轮）', true)
    cases.push({
      ok: timeoutOnPage,
      name: '解锁判定·超时但复查确认在解锁页 ⇒ 必须继续解锁（本轮真修的回归靶子）',
      detail: timeoutOnPage
        ? '超时的 why 文本不再单独决定「不解锁」⇒ 设备真在解锁页时会执行解锁'
        : '⚠️ 超时就一律「无需解锁」—— 而超时窗口里设备往往**已经在解锁页**'
          + '（守卫重定向落地慢）⇒ 解锁从不执行 ⇒ 造数 / UI-07 / UI-12 连锁假红'
          + '（matrix-full10a 的 12 条红就是这么来的）',
    })
    // ③ 阴性对照：超时 + 复查**不在**解锁页 ⇒ 确实不该解锁
    const timeoutOffPage = shouldAttemptUnlock('超时仍未落在 #/email（实际 #/ai）', false)
    cases.push({
      ok: timeoutOffPage === false,
      name: '阴性对照·超时且复查确认不在解锁页 ⇒ 不解锁（否则 ② 会恒真）',
      detail: `shouldAttemptUnlock(<超时>, false) = ${timeoutOffPage}（期望 false）`,
    })
    // ④ 变异对照：把决策改回旧写法（只看 why、不看复查结果）⇒ ② 必须转红
    const legacy = (why) => why === '停在解锁页'
    const legacyOnTimeoutPage = legacy('超时仍未落在 #/email（实际 #/ai，跑了 40 轮）')
    cases.push({
      ok: legacyOnTimeoutPage === false,
      name: '变异对照·旧写法（只比 why 字符串）会在超时+在解锁页时判「不解锁」（说明本轮修复的必要性）',
      detail: `旧写法在同一输入下返回 ${legacyOnTimeoutPage}（期望 false = 不解锁）；`
        + '而当前实现返回 true ⇒ 修复确实改变了这一格的行为',
    })
  }

  // ---- 陈旧构建闸门（2026-10-05 新增）----
  // 起因：矩阵默认量 .sttdev，当天构建的 APK 却是 .matrix ⇒ 整轮 31/42
  // 都在量 9 小时前的老 bundle，UI-06d 的红读数完整得像产品缺陷。
  // 「量的是哪一份」必须自己先钉住，否则读数会把**环境问题说成产品问题**。
  {
    const T0 = Date.parse('2026-10-05T08:42:09+08:00')      // APK 构建时刻
    const M = 60000
    // ① 基线：设备上的包就是刚装的那个（领先 0）
    const fresh = staleBuildVerdict(T0, T0)
    cases.push({
      ok: fresh.stale === false,
      name: '陈旧闸门·包与 APK 同一时刻 ⇒ 判为新鲜（基线行为）',
      detail: `staleBuildVerdict(T0, T0) = ${fresh.stale}（期望 false）`,
    })
    // ② ★ 本轮真吃的那一条：包比 APK 旧 9 小时 ⇒ 必须判陈旧
    //    ⚠️ 这里的算术**错过一次**：写成 `9 * 3600 * M` 得到的是 540 **小时**
    //    （3600 是「每小时的秒数」，不是「每小时的分钟数」），于是断言 lagMin===540
    //    失败。当时读数是「旧 32400 分钟」——函数判对了，是我喂错了量。
    //    记在这里是因为它和「变异没生效」是同一类：先怀疑量，再怀疑被测对象。
    const nineHours = staleBuildVerdict(T0 - 9 * 60 * M, T0)   // 9h = 9*60 分钟
    cases.push({
      ok: nineHours.stale === true && nineHours.lagMin === 540,
      name: '陈旧闸门·包比 APK 旧 9 小时 ⇒ 必须判陈旧（本轮真修的回归靶子）',
      detail: `实测 ${nineHours.stale === true ? '已拦下' : '★ 没拦'}；${nineHours.why}`,
    })
    // ③ 阴性对照：包比 APK **新** ⇒ 不得误报
    const newer = staleBuildVerdict(T0 + 30 * M, T0)
    cases.push({
      ok: newer.stale === false,
      name: '阴性对照·包比 APK 新 ⇒ 不得误报（否则 ② 只是「时间在动就红」）',
      detail: `staleBuildVerdict(T0+30min, T0) = ${newer.stale}（期望 false）；${newer.why}`,
    })
    // ④ 容差对照：差 60 **秒**（安装与构建同一次操作内）⇒ 不得误报
    //    同样错过一次：写成 `60 * M` 是 60 分钟，早于容差（2 分钟）之外，必然误报。
    const withinTol = staleBuildVerdict(T0 - 60 * 1000, T0)
    cases.push({
      ok: withinTol.stale === false,
      name: '容差对照·只差 60 秒（装包与构建同一次操作）⇒ 不得误报',
      detail: `staleBuildVerdict(T0-60s, T0) = ${withinTol.stale}（期望 false）`,
    })
    // ⑤ 失明对照：读不到任一时间戳 ⇒ **不得当成新鲜放行**
    const blind = staleBuildVerdict(null, T0)
    const blind2 = staleBuildVerdict(T0, undefined)
    cases.push({
      ok: blind.stale === null && blind2.stale === null,
      name: '失明对照·读不到时间戳 ⇒ 判「未知」而不是「新鲜」（失明与通过同形）',
      detail: `staleBuildVerdict(null, T0) = ${blind.stale}；staleBuildVerdict(T0, undefined) = ${blind2.stale}（期望都是 null）`,
    })
  }

  // ---- 路由 meta 读取（**调真函数，不复刻逻辑**）----
  // ⚠️ 2026-10-06 实吃：我第一次验它时，在临时脚本里**把 routeBottomNav 的逻辑
  //   抄了一遍**再测，测出来全对 —— 而真函数里路径写错
  //   （join(SRC,'..','app') 落到 frontend/app，真实位置是 frontend/src/app），
  //   上设备立刻 ENOENT。
  //   ⇒ **复制逻辑的测试测不到真函数的 bug。** 断言必须打在被验对象本身上。
  const bnMeetings = routeBottomNav('/meetings')
  cases.push({
    ok: bnMeetings.known === true && bnMeetings.value === false,
    name: '路由 meta·/meetings 读出 bottomNav=false（UI-05a 不得据此误判红）',
    detail: `known=${bnMeetings.known} value=${bnMeetings.value} declared=${bnMeetings.declared}`,
  })
  const bnAi = routeBottomNav('/ai')
  cases.push({
    ok: bnAi.known === true && bnAi.value === true,
    name: '路由 meta·/ai 读出 bottomNav=true',
    detail: `known=${bnAi.known} value=${bnAi.value} declared=${bnAi.declared}`,
  })
  // 阴性对照：**读不到**必须显式判为 unknown，不能默认 true
  const bnGhost = routeBottomNav('/__no_such_route__')
  cases.push({
    ok: bnGhost.known === false,
    name: '阴性对照·不存在的路由必须判 unknown（不许默认「要有底栏」）',
    detail: `known=${bnGhost.known} value=${bnGhost.value}（应为 known=false）`,
  })
  // 前提自证：至少有一条**真实**路由读出了 known=true。
  // ⚠️ 这里**刻意不再单独写一遍路径表达式**去 readFileSync ——
  //   那样会多出一份与 routeBottomNav 各自独立的路径，迟早漂移。
  //   「/ai 那条 known=true」本身就是前提：known=true 只有在**真读到了文件**时
  //   才可能成立（读不到会走 catch 返回 known=false）。
  const anyKnown = ['/ai', '/email', '/notes', '/rss', '/settings'].some((p) => routeBottomNav(p).known)
  cases.push({
    ok: anyKnown,
    name: '前提自证·至少 1 条真实路由读出了 known=true（否则上面几条可能全在 unknown 上假绿）',
    detail: anyKnown ? '真实路由可读，router-mobile.ts 确实被读到' : '所有真实路由都 known=false ⇒ 文件根本没读到',
  })

  // ---- 注入片段：登记完整性（2026-10-06 新增）----
  //
  // ⚠️ 不硬编码「应该有哪些片段」的清单 —— 那种清单自己就会漏。
  // 也不用正则猜模板起止：第一版用「反引号+换行」找收尾，而模板常写成
  //   **收尾反引号与末行同行**的形态 ⇒ 结束位置恒为 -1 ⇒ 扫到 0 个，
  //   而下面那条「全部已登记」在 0 个时是**恒真**。
  //   是「前提自证」那条把它抓出来的 —— 证据里直接写着「0 个」。
  // ⇒ 改用 @babel/parser 精确取「顶层、初始值是模板字面量」的变量。
  //
  // ⚠️ 这里**只能比名字、不能比值**：`--selftest` 在本文件前部就 early-exit，
  //   而 SNIPPETS / 各片段常量都定义在它后面 ⇒ 按值比会撞 TDZ。
  //   登记与否从**源码文本**里查 SNIPPETS.push(名字)。
  {
    const injected = []
    let why = ''
    const srcText = readFileSync(fileURLToPath(import.meta.url), 'utf8')
    try {
      const req = createRequire(join(dirname(fileURLToPath(import.meta.url)), '..', 'frontend', 'package.json'))
      const parser = req('@babel/parser')
      const ast = parser.parse(srcText, { sourceType: 'module' })
      for (const node of ast.program.body) {
        if (node.type !== 'VariableDeclaration') continue
        for (const d of node.declarations) {
          if (d.id.type !== 'Identifier' || !d.init || d.init.type !== 'TemplateLiteral') continue
          const head = (d.init.quasis[0].value.cooked || '').trim()
          // 只认**会被求值**的：IIFE / async IIFE / 对象字面量
          if (/^\((async\s+)?[({]/.test(head)) injected.push(d.id.name)
        }
      }
    } catch (e) {
      why = '解析本文件失败（解析器坏了 ⇒ 这条会变成「什么都没查」的假绿）：' +
        String(e.message).split('\n')[0].slice(0, 80)
    }
    cases.push({
      ok: !why && injected.length > 0,
      name: '前提自证·用解析器取到顶层模板片段（否则下面那条恒真）',
      detail: why || `取到 ${injected.length} 个：${injected.join(', ') || '（0 个 ⇒ 下面那条恒真）'}`,
    })
    // ⚠️ 第一版要求每个名字**独占一次** SNIPPETS.push(它)，
    //   于是「一次 push 六个」被判成未登记 —— **判据太窄**，不是产品问题。
    // ⇒ 改成解析 SNIPPETS.push(...) 的**实参表**，与写法无关。
    const registered = new Set()
    for (const m of srcText.matchAll(/SNIPPETS\.push\(([^)]*)\)/g)) {
      for (const a of m[1].split(',')) {
        const n = a.trim()
        if (/^[A-Z][A-Z0-9_]*$/.test(n)) registered.add(n)
      }
    }
    const notReg = injected.filter((n) => !registered.has(n))
    cases.push({
      ok: !why && notReg.length === 0,
      name: `片段登记·${injected.length} 个注入片段都必须 SNIPPETS.push(它)（否则绕过语法预检）`,
      detail: why || (notReg.length
        ? `未登记：${notReg.join(', ')}（已登记 ${[...registered].join(', ') || '无'}）`
        : `全部已登记（${injected.join(', ')}）`),
    })
  }

  // -------------------------------------------------------------------------
  // 解锁重试的自证（2026-10-06）
  //
  // 为什么必须在**没有设备**的时候也验：UI-05a 的「/email 未稳定」是**间歇性**的
  // （只在宿主 load 高、初始化慢时复现）。所以「这轮跑绿了」**证明不了**重试有用
  // —— 没复现的时候，绿和「根本没这段代码」长得一模一样。**症状不指向病因。**
  // ⇒ 这里用一条**假通道**把那个窗口**造出来**：点击点下去但页面纹丝不动，
  //   量具必须自己发现并重试。
  //
  // ⚠️ 判据不是「返回了 unlocked=false」（那恒真），而是**点了几次**——
  //   「点一次就放弃」和「点三次」是两种实现，只看结果分不出来。
  // -------------------------------------------------------------------------
  {
    /** 造一条只会回答三种表达式（探测 / 填口令 / 点解锁）的假通道。 */
    const fakeCdp = (opts) => {
      const st = { probes: 0, fills: 0, clickExprs: 0, clicked: 0, unlocked: false }
      return {
        st,
        ev: async (expr) => {
          // ⚠️ 三种表达式必须**互斥**地认出来；认错就会把探测当点击，
          //   于是「点了几次」这个判据量的不是点击 —— **量的东西必须先自证认对了**。
          if (expr.includes('HTMLInputElement')) { st.fills++; return true }
          if (expr.includes('.login-btn')) {
            st.clickExprs++
            if (opts.buttonDisabled) return 'disabled'
            // 「点下去」和「点下去生效」是两件事：浏览器吃不吃这一下，fake 管不了。
            // ⚠️ 早先这里只在 `clickWorks` 时才计数，于是「被静默吃掉」那个场景
            //   明明点了 3 次却报「真的点下去 0 次」—— **判据的期望值与量具语义不符**。
            st.clicked++
            if (opts.clickWorks) st.unlocked = true
            return 'clicked: 解锁'
          }
          st.probes++
          return {
            shell: true, hash: '#/email', nav: true,
            unlockHint: !st.unlocked, loginView: !st.unlocked,
            len: 200,
          }
        },
      }
    }
    // ① 被静默吃掉的那次点击：点下去、没报错、页面不动。必须**重试到 3 次**。
    const swallowed = fakeCdp({ clickWorks: false })
    const r1 = await unlockIfNeeded(swallowed, '#/email')
    cases.push({
      ok: swallowed.st.clickExprs === 3 && swallowed.st.clicked === 3 &&
        r1.unlocked === false && /点解锁 3 次仍未就绪/.test(r1.why),
      name: '解锁重试·点击被静默吃掉时必须点满 3 次（点一次就放弃 ⇒ 这段代码不存在）',
      detail: `点下去 ${swallowed.st.clicked} 次（期望 3）；unlocked=${r1.unlocked}（期望 false）；` +
        `理由含「点解锁 3 次」=${/点解锁 3 次仍未就绪/.test(r1.why)} —— 理由「${r1.why}」`,
    })
    // ② 阴性对照：第一次点击就生效时**不许**多点。多点会把「好了」变成「又点坏了」。
    const healthy = fakeCdp({ clickWorks: true })
    const r2 = await unlockIfNeeded(healthy, '#/email')
    cases.push({
      ok: healthy.st.clickExprs === 1 && healthy.st.clicked === 1 &&
        r2.unlocked === true && /第 1\/3 次点击/.test(r2.why),
      name: '解锁重试·第一次点击就生效时只点一次（盲重试的对照）',
      detail: `点了 ${healthy.st.clickExprs} 次（期望 1）；unlocked=${r2.unlocked}（期望 true）；` +
        `理由含「第 1/3 次」=${/第 1\/3 次点击/.test(r2.why)} —— 理由「${r2.why}」`,
    })
    // ③ disabled 的按钮 click() 是静默 no-op：必须读出来当指纹，而不是无脑重试。
    const disabled = fakeCdp({ clickWorks: true, buttonDisabled: true })
    const r3 = await unlockIfNeeded(disabled, '#/email')
    cases.push({
      ok: disabled.st.clickExprs === 3 && disabled.st.clicked === 0 &&
        r3.unlocked === false && /disabled/.test(r3.why),
      name: '解锁重试·按钮 disabled 时要点得出来（静默 no-op 必须被当成指纹）',
      detail: `尝试点 ${disabled.st.clickExprs} 次（期望 3）、点下去 ${disabled.st.clicked} 次（期望 0）；` +
        `理由含「disabled」=${/disabled/.test(r3.why)} —— 理由「${r3.why}」`,
    })
    // ④ 登录页**不是**解锁页。这不是洁癖：`LoginView.vue:78` 的账号登录表单
    //    **也**是 `input[type="password"]`，旧的判据正是用它当「解锁页」标志。
    //    一旦混淆，量具会把**主密码写进账号密码框**（另一个 secret），
    //    而且那一屏根本没有「解锁」按钮 —— 症状是后面那句「找不到解锁按钮」，
    //    离病因隔了两层。⇒ 必须断言「登录页上一次都不许填、一次都不许点」。
    const loginPage = {
      probes: 0, fills: 0, clickExprs: 0,
      ev: async (expr) => {
        if (expr.includes('HTMLInputElement')) { loginPage.fills++; return true }
        if (expr.includes('.login-btn')) { loginPage.clickExprs++; return 'clicked: 解锁' }
        loginPage.probes++
        return { shell: false, hash: '#/login', nav: false, unlockHint: false, loginView: true, len: 120 }
      },
    }
    const r4 = await unlockIfNeeded(loginPage, '#/email')
    cases.push({
      ok: r4.unlocked === false && /停在登录页/.test(r4.why) &&
        loginPage.fills === 0 && loginPage.clickExprs === 0,
      name: '登录页不得被当成解锁页（否则主密码会写进账号密码框）',
      detail: `填口令 ${loginPage.fills} 次（期望 0）、点按钮 ${loginPage.clickExprs} 次（期望 0）；` +
        `unlocked=${r4.unlocked}（期望 false）；理由「${r4.why}」`,
    })
    // 前提自证：假通道**真的被问过**。少了它，上面几条有可能全在「一次都没跑」上假绿。
    cases.push({
      ok: swallowed.st.probes > 0 && swallowed.st.fills > 0 && healthy.st.probes > 0,
      name: '前提自证·假通道确实被调用（否则上面三条可能全在「没跑」上假绿）',
      detail: `被吞场景：探测 ${swallowed.st.probes} 次 / 填口令 ${swallowed.st.fills} 次；` +
        `正常场景：探测 ${healthy.st.probes} 次`,
    })
  }

  // -------------------------------------------------------------------------
  // 「对照必须先于测量」的顺序自证（2026-10-06）
  //
  // 顺序本身是**承重**的。UI-01a 测的是「按返回 → 抽屉关、路由不动」，
  // 而它的前提（返回键真的被送达、且不是被守卫吞掉）由 UI-01b 判定。
  // 原实现把 UI-01a 排在对照**前面** ⇒ 宿主高压 / 页面停在解锁屏时，
  // UI-01a 记 🔴，把一条**环境事实或产品设计**写进了产品缺陷清单。
  //   实测翻车两次：先误记「按键未送达」，查实是 `AppLayout.vue:216`
  //   在 `route.query.unlock === '1'` 时**故意吞掉** back（BUG-B 修复）。
  // ⇒ 这个顺序不能靠自觉，必须有判据钉住。
  //
  // ⚠️ 本条自己就踩过「恒真」的坑：`indexOf` 找不到时返回 **-1**，
  //   而 -1 比任何行号都小 ⇒ 「对照在前」会**永远成立**。
  //   所以 `bothFound` 必须与顺序**同时**是判据的一部分。
  // -------------------------------------------------------------------------
  {
    // 重新读一遍**磁盘上的**本文件，而不是复用上面那个块里的局部变量 ——
    // 那个变量在它的 `{ }` 里出了作用域；而更重要的是：判据量的是**磁盘内容**，
    // 复用内存里的副本会让人以为它量的是文件。
    const srcText = readFileSync(fileURLToPath(import.meta.url), 'utf8')
    // ⚠️ `String.indexOf` 返回的是**字符偏移**，不是行号。
    //   第一版证据行直接把它写成「第 N 行」——两个都是数字，**肉眼分不出**，
    //   于是偏移量冒充了行号（实测 16391 / 16444，而真实行号是 2300 上下）。
    //   ⇒ 判据要报给人看的那个量，就得先把量纲换算对。
    const lineOf = (idx) => (idx < 0 ? -1 : srcText.slice(0, idx).split('\n').length)
    // ⚠️⚠️ 语料里必须**排除本判据自己**，而且**排除规则本身就是这里最��易写错的部分**。
    //   这个判据写坏了三轮，每一轮都判绿而量的是错的行：
    //     ① `indexOf("record('UI-01b'")` ⇒ 量到**判据自己那两行**（字面量就在源码里）。
    //     ② 加「其后紧跟 `,`」⇒ 量到**判据自己的注释行**（散文里也写着完整调用形态）。
    //     ⇒ 最终规则：**该位置所在行、匹配点之前只能是空白**，即它是一条独立语句，
    //       而不是「某句话里提到了它」。真调用是 `      record('UI-01a', …`。
    //   ⇒ 教训：**判据的注释也会进语料**。排除规则要用「结构」而不是「字面量」。
    const tokOf = (id) => 'record(' + "'" + id + "'"
    const callIndex = (id) => {
      const tok = tokOf(id)
      let from = 0
      for (;;) {
        const i = srcText.indexOf(tok, from)
        if (i < 0) return -1
        const lineStart = srcText.lastIndexOf('\n', i) + 1
        const before = srcText.slice(lineStart, i)
        if (before.trim() === '' && srcText[i + tok.length] === ',') return i
        from = i + tok.length
      }
    }
    const iCtrl = callIndex('UI-01b')
    const iMeas = callIndex('UI-01a')
    const bothFound = iCtrl >= 0 && iMeas >= 0
    cases.push({
      ok: bothFound && iCtrl < iMeas,
      name: '对照先于测量·UI-01b 必须早于 UI-01a（否则环境事实会被记成产品缺陷）',
      detail: bothFound
        ? `UI-01b 在第 ${lineOf(iCtrl)} 行、UI-01a 在第 ${lineOf(iMeas)} 行（期望前者更早）`
        : `源码里找不到真正的 record('UI-01b', / record('UI-01a', 调用（${iCtrl} / ${iMeas}）—— ` +
          '**这条会变成恒真**：indexOf 找不到返回 -1，而 -1 比任何行号都小',
    })
  }

  console.log('--- 量具自检：UI-12 被测元素的源码出处 + 解锁重试 + 对照顺序 ---')
  for (const c of cases) console.log(`  ${c.ok ? '通过' : '不通过'}  ${c.name}\n         ${c.detail}`)
  const bad = cases.filter((c) => !c.ok)
  console.log(`\n自检: ${cases.length - bad.length}/${cases.length} 通过`)
  if (bad.length) {
    console.error('\n❌ 量具在量不存在的东西（或匹配精度不足）。先修量具再谈 UI-12 的结论。')
    process.exit(6)
  }
  console.log('✓ 量具的被测对象都能在源码里指出出处（选择器 + 路由 meta）')
  // ⚠️ 必须在这里**真的退出**。少了这一行，自检会顺流往下跑设备流程，
  // 在没有 POCKET_SERIAL 的机器上刷一屏 `device 'undefined' not found`，
  // 看起来像自检失败 —— 而自检其实早就绿了。症状不指向病因。
  process.exit(0)
}
if ((process.argv[2] || '').toLowerCase() === '--selftest') await runSelectorSelftest()


// ---------------------------------------------------------------------------
// 量具自证：这套「遮挡」判定到底会不会报警？
//
// 做法：拿一个**已知**被底栏盖住的坐标做阳性对照。若量具对它也报「没被盖住」，
// 说明它在量一个不成立的东西，UI-12 的结论全部无效。
// 反之若它对已知遮挡报警、对已知可见不报警，它才有牙。
// ---------------------------------------------------------------------------
// ⚠️ 2026-10-04 实吃，很隐蔽：曾用
//   querySelector('.bottom-nav, nav.app-bottom, [class*="bottom-nav"]')
// 量底栏，量到 navHeight=842 / navTop=72 的东西。
// 原因：带逗号的选择器列表返回**文档顺序里第一个匹配任意一条**的元素，
// 不是「第一条选择器匹配的元素」；某个类名里**含有** "bottom-nav" 的更早元素赢了。
// 而真 .bottom-nav 是 fixed/bottom:0/height≈56px，不可能 842px。
// 「量具在量错的东西」比「量具报错」危险得多——它会安静地给出看似合理的数。
const NAV_FIND = `(() => {
  const el = document.querySelector('.bottom-nav');
  if (!el) {
    const near = [...document.querySelectorAll('*')].filter(e => /bottom.?nav/i.test(String(e.className||'')))
      .slice(0,4).map(e => String(e.className).slice(0,50));
    return { found: false, nearMiss: near };
  }
  const r = el.getBoundingClientRect();
  const s = getComputedStyle(el);
  return {
    found: true, cls: String(el.className).slice(0,50),
    top: Math.round(r.top), bottom: Math.round(r.bottom), h: Math.round(r.height),
    w: Math.round(r.width), position: s.position, display: s.display,
    looksLikeBottomBar: r.bottom >= innerHeight - 2 && r.height < innerHeight * 0.4 && r.width > innerWidth * 0.8,
  };
})()`

const OCCLUSION_PROBE = `(() => {
  const nav = document.querySelector('.bottom-nav');
  const out = { navFound: !!nav };
  if (!nav) return out;
  const nr = nav.getBoundingClientRect();
  out.navTop = Math.round(nr.top);
  out.navHeight = Math.round(nr.height);
  out.navDisplay = getComputedStyle(nav).display;
  // 在底栏**正中间**取一点，看 elementFromPoint 命中谁。
  // 这一点的 y 必然落在底栏矩形内 —— 如果命中的不是底栏本身，
  // 说明有东西盖在底栏上（反向遮挡），这是一个恒真的阳性对照。
  const x = Math.round(nr.left + nr.width / 2);
  const y = Math.round(nr.top + nr.height / 2);
  const hit = document.elementFromPoint(x, y);
  out.probe = { x, y };
  out.probeHit = hit ? (hit.tagName + '.' + String(hit.className||'').replace(/\\s+/g,' ').trim().split(' ').slice(0,2).join('.')) : null;
  out.probeHitIsNav = !!(hit && nav.contains(hit));
  return out;
})()`

/** 判定某元素是否被底栏遮住：矩形是否越过 navTop，且中心点的命中者不是它自己。 */
const OCCLUSION_CHECK = (sel) => `(() => {
  const el = document.querySelector(${JSON.stringify(sel)});
  if (!el) return { found: false, sel: ${JSON.stringify(sel)} };
  const r = el.getBoundingClientRect();
  const nav = document.querySelector('.bottom-nav');
  const navTop = nav ? nav.getBoundingClientRect().top : null;
  const cx = Math.round(r.left + r.width / 2);
  const cy = Math.round(r.top + r.height / 2);
  const hit = document.elementFromPoint(cx, cy);
  const hitDesc = hit ? (hit.tagName + '.' + String(hit.className||'').replace(/\\s+/g,' ').trim().split(' ').slice(0,2).join('.')) : null;
  return {
    found: true, sel: ${JSON.stringify(sel)},
    rect: { top: Math.round(r.top), bottom: Math.round(r.bottom), h: Math.round(r.height), w: Math.round(r.width) },
    navTop: navTop === null ? null : Math.round(navTop),
    belowNav: navTop === null ? null : r.bottom > navTop,
    center: { x: cx, y: cy },
    hitAtCenter: hitDesc,
    hitIsSelfOrChild: !!(hit && (hit === el || el.contains(hit))),
  };
})()`

/**
 * 等外壳真正渲染出来再开始量。
 * ⚠️ 2026-10-04 实吃：自证原本紧接 boot 就跑，那时 Vue 还没 mount，
 * 量到的是空壳 ⇒ 「找不到底栏」红，而同一路由稍后再量却读到 nav=block。
 * **量具自己制造了假阴性。** 所以任何测量之前必须先证明被测对象已就位。
 */
/**
 * @param expectHash 期望的落点。给了之后，**落点不等于它就不算就绪**。
 *   ⚠️ 2026-10-06 全量实跑：只靠「等更久」（minReadyPolls）不够 ——
 *     宿主 load 20+ 时 `requiresLobster` 路由的重定向要好几秒，
 *     固定轮询数只是把阈值抬高，**没有抓住要害**。
 *     要害是：**就绪这个概念必须相对于「我要去哪」来定义**。
 */
async function waitForShell(c, timeoutMs = 45000, expectHash = null) {
  const t0 = Date.now()
  // ⚠️ 2026-10-06 实吃，**这条让整轮解锁彻底失效**：原来只按 `timeoutMs` 收口。
  //   但宿主 load 130–230 时单次 `Runtime.evaluate` 要 **90s**，于是 20s 的窗口
  //   只能跑完**一次**轮询就超时退出 —— 而那一次恰好卡在求值上，
  //   于是「停在解锁页」永远检测不到，`unlockIfNeeded` 每次都返回
  //   「无需解锁 / 超时」，设备就一直停在 `#/login?...&unlock=1`。
  //   症状（造数入口不存在）离病因（解锁从没执行）隔了三层。
  //   ⇒ 必须保证**至少跑完 minPolls 次轮询**，不能只看墙钟。
  const minPolls = 3
  // 「连续几轮都成立才算就绪」—— 对付上面那个**重定向尚未落地**的窗口。
  // 取 3 是因为轮询间隔 1500ms ⇒ 至少观察 3s。
  const minReadyPolls = 3
  let polls = 0
  let readyStreak = 0
  let offTarget = null
  while (Date.now() - t0 < timeoutMs || polls < minPolls) {
    polls++
    try {
      const r = await c.ev(`(() => ({
        // ⚠️ 2026-10-06 实吃，**就绪信号选错了**：原来用 .bottom-nav 当「外壳已渲染」
        //   的证据。但 router-mobile.ts 里有 **66 条路由是 bottomNav: false**
        //   （含 /meetings 与 /meetings/:id），那些页面上底栏**本就不该存在**。
        //   ⇒ 在 /meetings 上这一项永远为 false，waitForShell 永远判不出就绪，
        //   实测空转 **39 轮**后报「超时仍未渲染出外壳」，解锁流程因此从不执行。
        //   症状（超时）离病因（信号选错）隔了三层。
        //   ⇒ 改用 .app-layout：它是 AppLayout 的根节点，**每条路由都在**
        //   （里面那条 skip-link 正是页面上「Skip to main content」文字的来源）。
        //   底栏是否存在由 S1/UI-05b 单独判，不该拿它当全局就绪信号。
        // ⚠️ 本段在 cdp.ev 的模板字符串内，**不许出现反引号**（本轮已因此
        //   连踩两次，报出来的却是 "missing ) after argument list"）。
        shell: !!document.querySelector('.app-layout'),
        hash: location.hash,
        nav: !!document.querySelector('.bottom-nav'),
        // ⚠️ 2026-10-06 实吃（UI-05a 全量跑日志实录）：
        //   「解锁第 2/3 次未点下去：解锁页上找不到解锁按钮」 —— 这条**诊断是错的**。
        //   按钮当时**好好地在**，只是文案从「解锁」变成了「认证中...」
        //   （unlock-auth.ts:30，loading 时返回该文案；绑了生物认证且密码为空时返回
        //     「认证」——同样不含「解锁」）。
        //
        //   真正的病因在**上一层判据**：旧的「解锁页」标志是 input[type="password"]，
        //   而 LoginView.vue:78 的**账号登录表单也用它**（placeholder「输入密码」），
        //   那一屏的按钮写「登录」。⇒ 旧判据把**登录页**也报成「停在解锁页」；
        //     再叠加「按文案找按钮」，就出现「说在解锁页、却找不到解锁按钮」的
        //     **自相矛盾**。症状（找不到按钮）离病因（标志选错 + 按文案猜）隔了两层。
        //
        //   ⚠️ 这轮跑出来的**最终结论反而是对的**（解锁确实成功了）—— 正因为对，
        //     它才危险：**看起来正常不等于量对了。**
        //
        //   ⇒ 解锁页的精确标志取 「.login-view .unlock-hint」（LoginView.vue:13，
        //     **只**在 v-if="needUnlock" 分支里渲染）；登录页单独报，
        //     不许与解锁页混为一谈 —— 否则会把**主密码写进账号密码框**。
        unlockHint: !!document.querySelector('.login-view .unlock-hint'),
        loginView: !!document.querySelector('.login-view'),
        len: String(document.body?.innerText || '').trim().length
      }))()`, EVM)
      if (r?.unlockHint) return { ready: false, why: '停在解锁页', polls }
      if (r?.loginView) return { ready: false, why: '停在登录页（账号登录界面，不是主密码解锁屏）', polls }
      // ⚠️ 2026-10-06 全量实跑抓到的第三个坑：**第一轮就宣布 ready**。
      //   导航到 `requiresLobster` 路由（如 `/email`）时，守卫的重定向
      //   （→ `#/login?...&unlock=1`）**未必已经落地**；这个窗口里页面
      //   仍然是上一个视图 —— `shell` 在、`len > 5` ⇒ 立刻返回「无需解锁」。
      //   后果：`unlockIfNeeded` 从没执行过解锁，量具拿着**登录页**当目标路由去量，
      //   而日志打的是「解锁：无需解锁」，**看起来一切正常**。
      //   症状（UI-05 报某路由「未渲染外壳」）离病因隔了整整一轮。
      //   ⇒ 就绪必须**连续若干轮**都成立才算，且期间一旦出现解锁页立刻报出。
      if (r?.shell && r.len > 5) {
        if (expectHash && r.hash !== expectHash) {
          // 落点还不是我要去的地方 ⇒ **不算就绪**（多半是守卫正在重定向）
          readyStreak = 0
          offTarget = r.hash
          await sleep(1500)
          continue
        }
        readyStreak += 1
        if (readyStreak >= minReadyPolls) return { ready: true, polls }
      } else {
        readyStreak = 0
      }
    } catch { /* reload 期间通道会断 */ }
    await sleep(1500)
  }
  return {
    ready: false,
    why: offTarget
      ? `超时仍未落在 ${expectHash}（实际 ${offTarget}，跑了 ${polls} 轮）`
      : `超时仍未渲染出外壳（跑了 ${polls} 轮）`,
    polls,
    offTarget,
  }
}

/**
 * 超时分支的**决策**：`waitForShell` 没报「停在解锁页」时，到底要不要继续走解锁？
 *
 * ⚠️ 2026-10-06 真修的量具缺陷（matrix-full10a 的 12 条红）：
 *   原写法是 `if (st.why !== '停在解锁页') return {unlocked:false, why: st.why || '无需解锁'}`，
 *   也就是**靠 why 字符串相等**决定解锁与否。可 `waitForShell` 的**超时分支**返回的是
 *   「超时仍未落在 #/xxx」—— 那个窗口里设备**其实已停在解锁页**（守卫重定向落地慢），
 *   于是返回「无需解锁」⇒ **解锁从未执行** ⇒ 依赖「组件已渲染」的判据
 *   （造数 `.fab`、UI-07 `.refresh-content`、UI-12 详情页）连锁假红。
 *
 * ⇒ 正确口径：**超时时也要直接问设备现在在哪**（查 `.unlock-hint`），
 *   问得到且在场就继续解锁。
 *
 * 抽成独立函数是为了**能被自测直接调用**：
 *   我第一版把判据写成「在自身源码里找 `if (st.why …)` 块」——
 *   连续三次失败（锚点命中自己写的字符串字面量 ⇒ 自证循环；
 *   raw/code 两套文本混用 ⇒ 偏移错位；按字符窗口取块 ⇒ 恒真）。
 *   **判据读自己源码时，锚点本身就是最大的坑。** 行为可调用就别去解析文本。
 *
 * @param {string|undefined} stWhy waitForShell 返回的 why
 * @param {boolean} onUnlockPage 复查结果：设备此刻是否**确实**停在解锁页
 * @returns {boolean} true = 继续走解锁流程
 */
function shouldAttemptUnlock(stWhy, onUnlockPage) {
  if (stWhy === '停在解锁页') return true
  // 超时/其他：只有**复查确认在解锁页**才继续。
  // onUnlockPage 由调用方实时探测，不从 st.why 推断。
  return onUnlockPage === true
}

/**
 * 设备上的包是不是**比磁盘上那个 APK 旧**。
 *
 * # 为什么需要这道闸（2026-10-05 实吃）
 * 矩阵默认 `PKG=com.kaixuan.opencode.pocket.sttdev`，而当天构建的 APK 用的是
 * `MOBILE_APP_ID_SUFFIX=.matrix` —— **两个不同的 applicationId**。
 * 于是「改了源码、重新构建、装了新包」这三步都做了，矩阵却仍在量
 * **9 小时前的老 bundle**。
 *
 * # 症状长得和产品缺陷一模一样
 * UI-06d 报「顶栏被压扁 3px」，读数还写得很完整（h=45 vs token 48、
 * 视口高 411px、flex-shrink=1）—— 看起来是一个**已定位到 CSS 的产品缺陷**。
 * 真因是 `AppLayout.vue:564` 的 `flex-shrink: 0` 早就在源码里，
 * 设备上的旧包还没有它。**若照着这条红去改产品，就是往已正确的代码上叠补丁。**
 *
 * # 为什么比的不是源码 mtime
 * 拿 `frontend/src` 的最新 mtime 比，会被 `__tests__`、注释、格式化改动刷成
 * 「源码比 APK 新」，天天误报。真正决定设备行为的是 **APK 本身**，
 * 而 APK 一定不早于任何参与打包的源码 ⇒ 只比 APK 是既精确又不误报的。
 *
 * @param {number|null} pkgMs 设备上该包的 lastUpdateTime（毫秒）
 * @param {number|null} apkMs 磁盘上 APK 的 mtime（毫秒）
 * @param {number} tolMs 容差。装包与构建同一次操作内会有秒级差，不留容差会假红
 * @returns {{stale: boolean|null, lagMin: number|null, why: string}}
 *   stale===null 表示**读数缺失**（取不到任一时间戳）——不是「新鲜」，
 *   调用方必须把它当失明处理，不能默认放行。
 */
export function staleBuildVerdict(pkgMs, apkMs, tolMs = 120000) {
  if (typeof pkgMs !== 'number' || typeof apkMs !== 'number') {
    return { stale: null, lagMin: null, why: '取不到包的 lastUpdateTime 或 APK 的 mtime —— 无法判断新旧，按失明处理' }
  }
  const lagMs = apkMs - pkgMs
  const lagMin = Math.round(lagMs / 60000)
  if (lagMs > tolMs) {
    return { stale: true, lagMin, why: `设备上的包比磁盘上的 APK **旧 ${lagMin} 分钟**（阈值 ${Math.round(tolMs / 60000)} 分钟）` }
  }
  return { stale: false, lagMin, why: `设备上的包不比 APK 旧（领先 ${-lagMin} 分钟，容差 ${Math.round(tolMs / 60000)} 分钟）` }
}

/** 从 adb 读该包的 lastUpdateTime；读不到返回 null（不抛——调用方按失明处理）。 */
function readPkgUpdateTime(pkg, serial) {
  try {
    const out = execFileSync(ADB, ['-s', serial, 'shell', 'dumpsys', 'package', pkg],
      { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 })
    const m = /lastUpdateTime=(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})/.exec(out)
    if (!m) return null
    const t = new Date(m[1].replace(' ', 'T') + '+08:00').getTime()
    return Number.isFinite(t) ? t : null
  } catch (e) {
    return null
  }
}

/**
 * 走**真实**解锁流程（不是 import 内部模块——打包后那条路径不存在，
 * e2e 能用是因为它跑在 vite dev server 上）。
 * 受保护路由（requiresLobster：/email /notes /meetings /rss…）没有它会被踢回登录页。
 */
async function unlockIfNeeded(c, expectHash = null) {
  // ⚠️ 2026-10-06 实吃：宿主 load 116（本机 VMware 与模拟器抢 CPU）时
  //   `Runtime.evaluate` 会超过 60s 抛 CDP_SEND_TIMEOUT，而这个异常**穿透了**
  //   整条 unlockIfNeeded → goto → 主流程，把已经跑完并记好的 10 条结果
  //   一起带走，最后只留一个栈。
  //   ⇒ 通道抖动是**环境**事实，不该让整轮归零。这里降级成「本次没测成」，
  //   由调用方决定是重试还是记 SKIP。**症状（整轮崩）不指向病因（单次超时）。**
  const st = await waitForShell(c, 60000, expectHash)
  if (st.why !== '停在解锁页') {
    // ⚠️ 2026-10-06 真修（matrix-full10a 的 12 条红的真凶之一）：
    //   **不许靠 `why` 字符串相等**决定「要不要解锁」。`waitForShell` 的**超时分支**
    //   返回「超时仍未落在 #/xxx」，而那个窗口里设备**其实已停在解锁页**
    //   （守卫重定向落地比 60s 预算慢）⇒ 原来直接返回「无需解锁」，
    //   **解锁从未执行** ⇒ 依赖「组件已渲染」的判据（造数 `.fab`、
    //   UI-07 的 `.refresh-content`、UI-12 的详情页）连锁假红。
    // ⇒ 改成：**超时也直接问设备现在在哪**。决策抽到 `shouldAttemptUnlock`，
    //   自测直接调它（判据不去解析源码，见该函数注释）。
    let onUnlock = false
    try {
      const cur = await c.ev(`(() => ({
        unlockHint: !!document.querySelector('.login-view .unlock-hint'),
        hash: location.hash
      }))()`, EVM)
      onUnlock = !!cur?.unlockHint
    } catch (e) {
      return { unlocked: false, why: `${st.why || '超时'}（复查解锁页失败：${String(e.message).split('：')[0]}）` }
    }
    if (!shouldAttemptUnlock(st.why, onUnlock)) {
      return { unlocked: false, why: st.why || '无需解锁' }
    }
    // 复查确认停在解锁页 ⇒ **继续往下走真正的解锁流程**（不再提前返回）。
  }

  // ⚠️ 2026-10-06 实吃：**点一次不够**，而且「不够」这件事本身是**静默**的。
  //   unlock-chain.mjs 实测：解锁页刚落地就点「解锁」⇒ 之后 **14s 纹丝不动**
  //   （hash 不变、按钮还在、无任何报错）；隔一会儿再点 ⇒ **2.5s 内**就进去了。
  //   ⇒ 病因是那次点击落在**初始化尚未就绪**的窗口里被无声吃掉。
  //   症状（`/email 未稳定 1 条`）离病因（少点了一次）隔了两层，且**单跑不复现**：
  //     宿主 load 高 ⇒ 初始化慢 ⇒ 第一次点击更容易落进那个窗口。
  //   ⇒ 这里「点 → **验证** → 没生效再点」，最多 3 次；理由里带上尝试次数，
  //     否则失败时看不出是「点一次就放弃」还是「点了三次都没成」—— 这两种病因不同。
  const CLICK_TRIES = 3
  const VERIFY_MS = 45000
  let lastWhy = '(未点击)'
  for (let t = 1; t <= CLICK_TRIES; t++) {
    // 每次点击前重新确认仍在解锁页：上一轮点完可能已经跳走，也可能换了一张解锁页。
    // 不重查就点 ⇒ 会往一个已经不是解锁页的文档上点，而症状会被记成「解锁失败」。
    const here = await waitForShell(c, 20000, expectHash)
    if (here.why !== '停在解锁页') {
      if (here.ready) return { unlocked: true, why: `解锁成功（第 ${t - 1}/${CLICK_TRIES} 次点击后已落位）` }
      return { unlocked: false, why: `第 ${t} 次点击前已不在解锁页：${here.why}` }
    }
    // ⚠️ 填口令**必须按 class 定位到解锁表单内部**，不许用
    //   `input[placeholder*="主密码"], input[type="password"]` 满页找：
    //   LoginView.vue:78 的**账号登录表单也是 `input[type="password"]`**，
    //   满页找会在**登录页**把**主密码写进账号密码框**——那是另一个 secret，
    //   而且那一屏根本没有「解锁」按钮可点。症状（后面「找不到解锁按钮」）
    //   离病因（选择器没限定在解锁表单内）隔了两层。
    // 定位口径：`.login-view` 里**含 `.unlock-hint` 的那个** `.login-form`
    //   （LoginView.vue:12-13，`v-if="needUnlock"` 分支的唯一标志）。
    const filled = await c.ev(`(() => {
      const uf = [...document.querySelectorAll('.login-view .login-form')]
        .find(f => f.querySelector('.unlock-hint'));
      const i = uf && uf.querySelector('input[type="password"]');
      if (!i) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
      setter.call(i, ${JSON.stringify(MASTER_PW)});
      i.dispatchEvent(new Event('input', { bubbles: true }));
      return true;
    })()`, EVM)
    if (!filled) { lastWhy = '解锁页上找不到密码输入框'; await sleep(1200); continue }
    await sleep(400)
    // ⚠️ **不许按文案找按钮**。`unlockButtonLabel`（unlock-auth.ts:30-32）会在两种
    //   状态下返回**不含「解锁」**的文案：loading 时「认证中...」、绑了生物认证且
    //   密码为空时「认证」。而本轮全量跑日志实录的
    //   「解锁第 2/3 次未点下去：解锁页上找不到解锁按钮」就是这么来的 ——
    //   **按钮好好地在，只是刚点完正在转圈**。按文案定位把「正在忙」说成了「不存在」。
    // ⇒ 按 class 定位：`button.login-btn`（LoginView.vue:24），
    //   且**限定在解锁表单内** —— 同名按钮在登录表单里也有（:89/:99），
    //   而「退出重新登录 →」是 `.hint.hint-button`，天然被排除。
    // disabled 的按钮 `click()` 是**静默 no-op**（不派发事件），读出来当指纹而不是盲点。
    const clicked = await c.ev(`(() => {
      const uf = [...document.querySelectorAll('.login-view .login-form')]
        .find(f => f.querySelector('.unlock-hint'));
      if (!uf) return 'no-unlock-form';
      const b = uf.querySelector('button.login-btn');
      if (!b) return 'no-button';
      if (b.disabled || b.getAttribute('aria-disabled') === 'true') return 'disabled';
      const label = String(b.innerText || '').replace(/\\s+/g, ' ').trim();
      b.click();
      return 'clicked: ' + label;
    })()`, EVM)
    if (!String(clicked).startsWith('clicked')) {
      lastWhy = clicked === 'disabled'
        ? '解锁按钮 disabled（初始化未就绪，点上去是静默 no-op）'
        : (clicked === 'no-unlock-form' ? '页面上没有含 .unlock-hint 的解锁表单' : '解锁表单里找不到 button.login-btn')
      console.log(`  · 解锁第 ${t}/${CLICK_TRIES} 次未点下去：${lastWhy} —— 等一下再试`)
      await sleep(1200)
      continue
    }
    // 点下去了**还要落到目标**：解锁成功 ≠ 回到了我要去的页面。
    const after = await waitForShell(c, VERIFY_MS, expectHash)
    if (after.ready) {
      // 重试才成功时**必须打出来**：否则「跑通了」与「多点了两次才跑通」在日志上
      // 完全一样，而后者才是这条代码存在的理由。**症状（看着正常）不指向病因。**
      if (t > 1) console.log(`  · 解锁第 ${t}/${CLICK_TRIES} 次点击才生效（前 ${t - 1} 次点击被静默吃掉）`)
      const label = String(clicked).slice('clicked:'.length).trim()
      return { unlocked: true, why: `解锁成功（第 ${t}/${CLICK_TRIES} 次点击「${label}」）` }
    }
    lastWhy = after.why
    await sleep(1500)
  }
  return { unlocked: false, why: `点解锁 ${CLICK_TRIES} 次仍未就绪：${lastWhy}` }
}


/** 导航；**顺带**处理 requiresLobster 路由弹出的解锁页。
 *  ⚠️ 解锁只在**进入受保护路由时**才出现（`/#/ai` 不需要），所以
 *  「启动时判断一次要不要解锁」是不够的——那次会得到「无需解锁」的假结论，
 *  然后所有受保护路由全被踢回登录页，UI-12/UI-01/02 报「无结论」。
 */
/**
 * 某条路由**声明**的 `bottomNav` 是 true 还是 false，**从路由源码里读**。
 *
 * ⚠️ 2026-10-06 实吃，这是我自己刚引入的一个 bug 的修法：
 *   我给 UI-05a 加了「紧凑档却没有底栏 ⇒ 没渲染外壳 ⇒ 红」，
 *   理由是 `/email /notes /rss` 四条当时都返回一模一样的 40 字且 `nav=ABSENT`
 *   （壳层渲染了、路由内容没渲染）。**但 `router-mobile.ts` 里有 66 条路由是
 *   `bottomNav: false`**，其中就包括受测的 `/meetings` ⇒ 那条判据会**误判红**。
 *   一个「防虚假」的自证，因为自己假定了一个不总成立的性质，反而制造了假红。
 *
 *   ⇒ 期望值不能写死（会漂移），必须**逐条问源码**。这与 OCCLUSION_TARGETS
 *     的出处表是同一个原则：**判据的期望值要有出处。**
 */
function routeBottomNav(path) {
  const routerSrc = readFileSync(join(SRC, 'app', 'router-mobile.ts'), 'utf8')
  const i = routerSrc.indexOf(`path: '${path}'`)
  if (i < 0) return { known: false, value: null, why: `路由源码里找不到 path: '${path}'` }
  const meta = /meta:\s*\{([^}]*)\}/.exec(routerSrc.slice(i, i + 500))
  if (!meta) return { known: false, value: null, why: '该路由没写 meta' }
  const m = /bottomNav:\s*(\w+)/.exec(meta[1])
  // 未声明 = 不等于 false：AppLayout 的判据是 `route.meta.bottomNav === false` 才隐藏
  return { known: true, value: m ? m[1] === 'true' : true, declared: m ? m[1] : '(未声明⇒默认显示)' }
}

/**
 * 等这一屏**真的稳定下来**再返回。
 *
 * ⚠️ 2026-10-06 实吃，这是「量在被测对象就位之前」的最后一处：
 *   `goto` 原来固定 `sleep(2200)`。实测 `/email` 是 `requiresLobster` 路由，
 *   要先解锁本地库再列邮件，2.2s 时它只渲染出回退外壳（`Skip to main content
 *   🦞 OpenCode Pocket`）、`nav=ABSENT` ⇒ UI-05a/05b 报红。
 *   但**多等几秒再量，同一条路由完全正常**：`nav=true`、`.inbox-page` 已挂载、
 *   205 字真实内容。⇒ 那两条红是**量具量早了**，不是产品缺陷。
 *
 *   ⚠️ 判据不能用「文字够多」：空态（`/rss` 的「还没有订阅…」、`/email` 的
 *   「📧」）是**合法的稳定态**，用非空当条件会把它们永远等超时。
 *   ⇒ 判据是**稳定**：连续两次采样的正文长度 / `main` 子节点数 /
 *     `main` 首子类名 / **底栏在不在** 都不再变化。
 *
 * ⚠️ `nav` 必须参与比较（2026-10-06 实吃）：第一版 `sig()` 采了 `nav` 却
 *   **没把它写进比较条件**。于是存在这样一个窗口——`main` 已经挂上视图、
 *   正文长度也恰好没变，但路由还在切换、`showBottomNav` 还没跟上 ⇒
 *   判成「已稳定」，而这一屏的底栏其实是**上一条路由**的。
 *   实测表现为 `/email` **时好时坏**：单跑 UI-05 时绿、全量跑时红。
 *   ⇒ 「采样了却不比较」和「不采样」是同一种缺陷：**采了不用等于没采。**
 */
const SETTLE = `(async () => {
  const s = ms => new Promise(r => setTimeout(r, ms));
  const sig = () => {
    const m = document.querySelector('main');
    const kids = m ? m.children.length : 0;
    const len = String(document.body.innerText || '').trim().length;
    return { len, kids, nav: !!document.querySelector('.bottom-nav'),
             first: (m && m.firstElementChild) ? String(m.firstElementChild.className).slice(0,40) : '' };
  };
  let prev = sig();
  for (let i = 0; i < 40; i++) {
    await s(1000);
    const now = sig();
    if (now.len === prev.len && now.kids === prev.kids && now.nav === prev.nav && now.first === prev.first) {
      return { settled: true, rounds: i + 1, ...now };
    }
    prev = now;
  }
  return { settled: false, rounds: 40, ...prev };
})()`

/**
 * 导航到目标 hash，并在**确认真的落在那里**之后才返回。
 *
 * ⚠️ 2026-10-06 全量实跑抓到的污染源：原先这里只做「设 hash → 等 1200ms →
 *   查一次解锁态」，**从不核对最终落点**。而 `/email` 这类 `requiresLobster`
 *   路由会被 `routeGuards.ts` Case C 弹到 `/login?returnTo=…&unlock=1`，
 *   那次重定向**未必在 1200ms 内落地** ⇒ `unlockIfNeeded` 查的是一个空档 ⇒
 *   随后量的是**登录页**，却按 `/email` 记结论。
 *   症状：单跑 UI-05 绿、**全量跑红**（「未渲染外壳 /email」「底栏 ABSENT」），
 *   UI-03 读到的 `main[aria-label]` 竟是「登录」。
 *   这与 `seedMeeting` 当年踩的是同一个竞态，只是那里做了局部补救。
 *
 * ⇒ 期望值就是**请求的那个 hash**：落地不等于目标就重试，
 *   而且是**读出来的**，不是「变了没有」这种弱断言。
 */
/** 容错读当前 href：证据行要能说清「**去了哪**」，读取本身不能成为新的失败点。 */
const evSafeHref = async (cdp) => {
  try {
    const h = await cdp.ev('location.href', EVM)
    return typeof h === 'string' ? h.replace(/^https?:\/\/[^/]+/, '') : String(h)
  } catch (e) {
    return '(读取失败: ' + (e && e.message ? e.message : String(e)) + ')'
  }
}

const goto = async (cdp, hash, waitMs = 1200) => {
  let u = { unlocked: false, why: '未尝试' }
  let landed = null
  for (let attempt = 1; attempt <= 2; attempt += 1) {
    await cdp.ev(`location.hash = ${JSON.stringify(hash)}`, EVM)
    await sleep(waitMs)
    // 通道超时降级：重连一次再判解锁，仍不通就把理由带回去，不抛。
    try {
      u = await unlockIfNeeded(cdp, hash)
    } catch (e) {
      const why = 'CDP 超时：' + String(e.message).split('：')[0]
      try { await cdp.close() } catch { /* 已断 */ }
      cdp.reconnect = true // 提示调用方：下一条要重新开通道
      return { unlocked: false, why }
    }
    if (u.unlocked) await sleep(2500)
    landed = String(await cdp.ev('location.hash', EVM))
    if (landed === hash) break
    // 没落到目标：多半是守卫把请求弹走了。第二次带上这个事实。
    u = { ...u, why: `未落在 ${hash}（实际 ${landed}），已重试` }
  }
  // ★ 量之前必须证明「这一屏已经稳定」。返回 settle 供调用方检查/留证。
  let settle = null
  try { settle = await cdp.ev(SETTLE, EVM) } catch { settle = { settled: false, rounds: 0, why: 'SETTLE 求值失败' } }
  // ⚠️ `landed` 必须**带出去**：重试过仍没落到目标时，调用方要能判「本屏量错了」
  //   而不是继续把登录页当目标路由量下去。
  //   `onTarget=false` 时 `settle` 也一并作废 —— 对着错误的页面等稳定没有意义。
  const onTarget = landed === hash
  return {
    ...u,
    settle: onTarget ? settle : { settled: false, rounds: 0, why: `未落在 ${hash}（实际 ${landed}）` },
    requested: hash,
    landed,
    onTarget,
  }
}

// ---------------------------------------------------------------------------
// 造数：用**生产写路径**在设备上落一条真会议。
//
// ⚠️ 为什么不照搬 e2e 的 emailSeed.ts 手法（import 内部模块 + upsertEmail）：
//   那条路是 `import('/src/features/email/emails-store.ts')`，
//   **只在 vite dev server 上存在**。打包后模块被并进 chunk，
//   `/src/...` 这个 URL 在 WebView 里根本没有，动态 import 必抛 ——
//   `unlockLobster` 当初就是因为这个才改成走真实解锁流程的。
// ⇒ 设备侧唯一忠实、且不给生产代码留测试后门的造数方式，就是**点真实按钮**：
//   会议列表页 FAB → `startNewMeeting()`（MeetingListView.vue:174）
//   → `createMeeting()` 写本地库 → 跳详情。纯本地写库，不碰麦克风
//   （`captureDeviceLocation()` 是 best-effort，拿不到就静默跳过）。
//
// ⚠️ 造完**必须自证**：行数没涨说明写库没生效，此时后面所有「有数据」的
//   结论都不成立。必须**报出来并中止**，不能接着量出一片「无结论」，
//   再把它解释成「设备上本来就没有会议」—— 那是把自己的失败说成环境事实。
// ---------------------------------------------------------------------------

/** 数会议卡片的行数（先等骨架屏消失，否则量到的是占位符）。 */
const COUNT_ROWS = `(async () => {
  const sleep = (ms) => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 40; i++) {
    if (!document.querySelector('.skeleton-item, .skeleton-line')) break;
    await sleep(500);
  }
  return {
    count: document.querySelectorAll('.meeting-card').length,
    skeleton: !!document.querySelector('.skeleton-item, .skeleton-line'),
    first: String(document.querySelector('.meeting-card')?.innerText || '').replace(/\\s+/g,' ').trim().slice(0,40),
  };
})()`

/** 点开第一张会议卡，进详情页。返回是否真的进到了详情。 */
const OPEN_FIRST_MEETING = `(async () => {
  const sleep = (ms) => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 40; i++) {
    if (!document.querySelector('.skeleton-item, .skeleton-line')) break;
    await sleep(500);
  }
  const card = document.querySelector('.meeting-card');
  if (!card) return { ok: false, why: '没有 .meeting-card 行' };
  card.click();
  await sleep(2500);
  return { ok: location.hash !== '#/meetings', hash: location.hash, why: location.hash === '#/meetings' ? '点击后路由未变' : '' };
})()`

async function seedMeeting(cdp) {
  await goto(cdp, '#/meetings')
  // ⚠️ 2026-10-06 实吃，这是造数一直失败的真凶：
  //   脚本开头注入 token 后会 `location.reload()`，**reload 把内存里的解锁态冲掉了**。
  //   之后 `goto('#/meetings')` 只等 2200ms 就去查解锁页，而 requiresLobster
  //   的重定向（→ `#/login?...&unlock=1`）在这之后才发生 ⇒
  //   `unlockIfNeeded` 查的是一个**空档**，看到会议列表就判定「无需解锁」放行；
  //   等 `seedMeeting` 真正去找 `.fab` 时，页面已经变成解锁页了。
  //   症状（找不到 .fab）离病因（解锁查得太早）隔了三层。
  //   ⇒ 这里**再解一次锁**：此时重定向已经落地，才查得到。
  for (let attempt = 1; attempt <= 2; attempt++) {
    const u = await unlockIfNeeded(cdp)
    if (u.unlocked) { await sleep(3000); break }
    if (attempt === 2) console.log(`  · 解锁尝试 2 次未成功：${u.why}`)
    else await sleep(2500)
  }
  const before = await cdp.ev(COUNT_ROWS, EVM)
  if (before.count > 0) return { ok: true, seeded: false, count: before.count, why: '列表已有数据' }

  // FAB 跳详情时可能触发麦克风权限弹窗把页面挡住 —— 先把权限授掉。
  // 这是**环境准备**，不是产品改动：不给权限弹窗会把「弹窗挡路」
  // 伪装成「造数失败」。
  try { execFileSync(ADB, ['-s', SERIAL, 'shell', 'pm', 'grant', PKG, 'android.permission.RECORD_AUDIO'], { timeout: 15000 }) } catch { /* 已授过/不支持 */ }

  const clicked = await cdp.ev(`(() => {
    const fab = document.querySelector('.fab');
    if (fab) { fab.click(); return { ok: true, hash: location.hash }; }
    // ⚠️ 找不到造数入口时，**必须把这一屏的真实状态带回去**。
    //   只说「找不到 .fab」会让人以为是选择器写错；实测真因是
    //   设备停在 #/login?...&unlock=1（本地加密库未解锁），
    //   .fab 在 <template v-else> 里（MeetingListView.vue:9）压根没渲染。
    //   症状（找不到按钮）离病因（没解锁）隔了三层。
    // ⚠️ 这段注释里**不许出现反引号**：整段被包在 cdp.ev 的模板字符串里，
    //   一个反引号就会把字符串截断，报出来的却是
    //   "missing ) after argument list" —— 一个指向别处的语法错。
    //   （本轮真踩了两次，第二次还是我自己刚写完这条警告就又犯。）
    return { ok: false, why: '找不到 .fab',
             hash: location.hash,
             onUnlock: !!document.querySelector('input[type="password"], .unlock-hint'),
             text: String(document.body.innerText || '').replace(/\\s+/g,' ').trim().slice(0, 80) };
  })()`, EVM)
  if (!clicked.ok) {
    const why = clicked.onUnlock
      ? `找不到 .fab —— 这一屏是**解锁页**（hash=${clicked.hash}）：本地加密库未解锁，造数入口在 <template v-else> 里未渲染`
      : `找不到 .fab（hash=${clicked.hash}，这一屏是「${clicked.text}」）`
    return { ok: false, count: before.count, why }
  }
  await sleep(5000) // 等 createMeeting 落库 + 路由跳详情

  await goto(cdp, '#/meetings')
  const after = await cdp.ev(COUNT_ROWS, EVM)
  if (after.count <= before.count) {
    return { ok: false, count: after.count, why: `点了 .fab 但行数没涨（${before.count} → ${after.count}）：生产写路径未生效` }
  }
  return { ok: true, seeded: true, count: after.count, why: `点 .fab → createMeeting 落库，行数 ${before.count} → ${after.count}` }
}


// ===========================================================================
// 起应用并等 WebView devtools socket 就绪。
//
// 为什么必须等 socket 而不是等固定秒数：机器负载高时（本机实测 load 47+）
// 模拟器启动要 40s+，页面可能还没 load，socket 就不存在。直接 openCdp 会
// 报 APP_NOT_RUNNING，而那个错**不区分**「App 没起」与「起了但 WebView
// 还没建 socket」——两种情况的处置完全不同。前者要重启 App，后者只要等。
// ⚠️ 这正是仓里 adb-cdp.mjs 注释警告过的那类「症状不指向病因」。
const ACTIVITY = 'com.kaixuan.opencode.pocket.MainActivity'
const sh = (args, t = 20000) => {
  try { return execFileSync(ADB, ['-s', SERIAL, 'shell', ...args], { encoding: 'utf8', timeout: t }).trim() }
  catch { return '' }
}

/**
 * 预检：本脚本会把下面这些**字符串**注入 WebView 求值。字符串里的语法错
 * （本轮真踩：把可选链 `?.` 写成 `?`）在 host 上完全看不出来，只会在设备上
 * 抛 `CDP_EVAL_EXCEPTION: SyntaxError`，而那个症状长得和「通道断了」一样 ——
 * 于是我差点去查通道，而病根是自己的表达式。
 * ⇒ 发送之前先在本地 `new Function('return ' + expr)` 过一遍，语法错当场红。
 */
const INJECTED = [
  '({href: location.href, w: innerWidth, textLen: String(document.body?.innerText || "").trim().length})',
  '({href: location.href, w: innerWidth, h: innerHeight, dpr: devicePixelRatio})',
]
function preflightSnippets() {
  const bad = []
  // 片段里含反引号（本身就是模板字符串）的**整体**也要能求值：
  // 只逐条 new Function 会漏掉「外层拼接出的字符串语法坏了」这一类。
  for (const e of INJECTED) {
    try { new Function('return ' + e) } catch (err) { bad.push(`${err.message} ← ${e.slice(0, 70)}`) }
  }
  // 模板片段：包一层再求值，等价于在设备上编译的那一次。
  // ⚠️ 用 `SNIPPETS` 登记而不是直接引用标识符：`DRAWER` 是在本函数**之后**才
  //   定义的 const，直接引用会撞 TDZ（ReferenceError），预检自己先崩。
  //   ⇒ 片段定义完再统一登记、预检，见文件下方 `preflightSnippets()` 的调用点。
  for (const e of SNIPPETS) {
    if (!e) continue
    try { new Function('return ' + e) } catch (err) { bad.push(`${err.message} ← ${e.slice(0, 70)}`) }
  }
  if (bad.length) {
    console.error('❌ 注入片段本地语法预检失败（不必上设备，设备上只会伪装成「通道断」）：')
    for (const b of bad) console.error('   ' + b)
    process.exit(4)
  }
  return true
}
/** 待预检的模板片段。**每新增一个注入片段都要登记**，否则它绕过了预检。 */
const SNIPPETS = []
const sockets = () => (sh(['cat', '/proc/net/unix']).match(/webview_devtools_remote_(\d+)/g) || [])
const appPid = () => (sh(['pidof', PKG]).match(/\d+/) || [null])[0]

/**
 * UI-01a 抽屉探针。
 *
 * 字段全部**按源码里的真实形态**取，不按文案猜：
 *   · `.menu-btn`         — `AppLayout.vue:27`（`v-if="showMenuButton && !canGoBack"`）
 *   · `.bottom-sheet-overlay` — `SettingsMenuDrawer` 渲染的就是 `BottomSheet`
 *     （`placement="left"`），它 `<Teleport to="body">` ⇒ 必须在 body 下数。
 * ⚠️ 不许用「有没有出现某个文字」当判据：设备 UI 实际是英文（UI-13 已吃过）。
 */
const DRAWER = `(() => {
  const b = document.querySelector('.menu-btn');
  const r = b ? b.getBoundingClientRect() : null;
  return {
    href: location.hash,
    menuBtn: !!b,
    expanded: b ? String(b.getAttribute('aria-expanded')) : null,
    mRect: r ? Math.round(r.left) + ',' + Math.round(r.top) + ' ' + Math.round(r.width) + 'x' + Math.round(r.height) : null,
    sheet: !!document.querySelector('.bottom-sheet-overlay'),
    bodySheets: document.querySelectorAll('body > .bottom-sheet-overlay').length,
  };
})()`

/**
 * 登记：**全部**会被注入 WebView 求值的顶层模板片段。
 *
 * ⚠️ 2026-10-06 补的。前五个是本文件早就有的片段（DRAWER 之前），
 *   它们**从来没进过语法预检** —— 预检的价值全在「上设备之前就红」，
 *   漏登记的片段会以「语法错在设备上伪装成通道断」的形式逃走。
 *   一次登记全部，比逐个补更不容易再漏。
 *
 * ⚠️ 放在**所有片段定义之后**、预检调用之前：此时 6 个都已初始化，
 *   少写一个名字就是 `undefined` ⇒ 预检会直接抛 ReferenceError 当场红，
 *   而不是静默放过（这正是「登记漏了」最该有的失败形态）。
 */
SNIPPETS.push(NAV_FIND, OCCLUSION_PROBE, SETTLE, COUNT_ROWS, OPEN_FIRST_MEETING, DRAWER)
preflightSnippets()

async function ensureRunning({ relaunch = true, waitMs = 150000 } = {}) {
  /**
   * ⚠️ 2026-10-05 实吃，**这条 150s 全耗在一个永远不会出现的 socket 上**：
   *   刚 `adb install -r` 完，`PACKAGE_REPLACED` 会触发本应用的
   *   `EmailFetchReceiver` 广播 —— system_server 主动 `Start proc`，**没有 Activity**。
   *   于是 `pidof` 有值，而旧代码 `if (!pid && relaunch) { am start }`
   *   **整段被跳过** ⇒ 无 Activity ⇒ 无 WebView ⇒ 永远没有
   *   `webview_devtools_remote_<pid>`。
   *   150s 后报「页面未就绪，最后状态：(无)」—— 而 `(无)` 恰恰是它的指纹：
   *   `lastNote` 只在**进了轮询体**之后才会被赋值，`(无)` 说明每一轮都卡在
   *   「socket 还没出现」那个 `continue` 上，一次都没走到求值。
   *
   *   ⇒ 「进程在」**不等于**「Activity 在」，更不等于「WebView 在」。
   *   `am start` 对 singleTask Activity 是幂等的（已在前台就只是提到前台），
   *   所以**无条件**发一次，别用 pid 决定要不要发。
   */
  const start = () => {
    try { execFileSync(ADB, ['-s', SERIAL, 'shell', 'am', 'start', '-n', `${PKG}/${ACTIVITY}`], { timeout: 30000 }) }
    catch { /* 已在前台时 am start 也会 exit!=0，不代表失败 */ }
  }
  let pid = appPid()
  if (relaunch) {
    if (!pid) console.log(`  · ${PKG} 没在跑 → 拉起`)
    start()
    const t1 = Date.now()
    while (Date.now() - t1 < 60000 && !pid) { await sleep(2000); pid = appPid() }
    if (!pid) {
      console.log('  · 60s 内没出现 → 再拉一次')
      start()
      const t2 = Date.now()
      while (Date.now() - t2 < 60000 && !pid) { await sleep(2000); pid = appPid() }
    }
  }
  if (!pid) { console.error('❌ 起不来 App'); return null }
  console.log(`  · App pid=${pid}，等 WebView **页面**就绪（socket 存在 ≠ 页面已加载）…`)

  // ⚠️ 2026-10-04 实吃：**每次轮询都重开一次 CDP 是错的** —— 宿主高压时
  // 一次 openCdp 要几十秒，于是 90s 的窗口全耗在重连上，明明 socket 早就
  // 出现了却报超时。正确做法：**开一次，之后一直在同一条连接上轮询**。
  let handle = null
  const t0 = Date.now()
  let lastNote = ''
  while (Date.now() - t0 < waitMs) {
    if (!handle) {
      const have = sockets().some((x) => x.endsWith(`_${pid}`))
      if (!have) {
        // ⚠️ 这一支原来**不写** lastNote，于是整个循环跑完 lastNote 仍是 '(无)'，
        //   而 '(无)' 恰恰意味着「一次都没进到求值」——最需要解释的状态反而没有留痕。
        //   ⇒ 这里必须记，并且把「本 WebView 该有的 socket 名」写出来，
        //   好让人一眼看出是「进程/Activity 没起」还是「起来了但没连上」。
        lastNote = `无 webview_devtools_remote_${pid}（进程在 ≠ Activity 在；` +
          `当前有 ${sockets().length} 个 devtools socket）`
        await sleep(2000); continue
      }
      try { handle = await openCdp({ pkg: PKG }) }
      catch (e) { lastNote = 'CDP 未就绪：' + String(e.message).split('\n')[0].slice(0, 60); await sleep(2000); continue }
    }
    try {
      // ⚠️ 这里踩过：曾写成 `document.body?'innerText'||''`（把可选链 `?.`
      // 的点丢了），那是 **语法错误**，CDP 抛 CDP_EVAL_EXCEPTION: SyntaxError，
      // 症状看起来像「通道断」而实则是我自己的表达式坏了 —— 两种病处置完全不同。
      const r = await handle.ev('({href: location.href, w: innerWidth, textLen: String(document.body?.innerText || "").trim().length})', 45000)
      const loaded = typeof r?.href === 'string' && /^https?:\/\//.test(r.href) && r.w > 0
      if (loaded) {
        console.log(`  · 页面就绪 ${r.href}（${r.w}px，用了 ${Math.round((Date.now() - t0) / 1000)}s）`)
        return handle
      }
      lastNote = `socket 在但页面未就绪（href=${r?.href} w=${r?.w}）`
    } catch (e) {
      // reload / 崩溃会让通道断，丢掉重来（而不是卡死）
      try { await handle.close() } catch { /* 已断 */ }
      handle = null
      lastNote = '通道断，重连中：' + String(e.message).split('\n')[0].slice(0, 50)
    }
    await sleep(2500)
  }
  if (handle) { try { await handle.close() } catch { /* 已断 */ } }
  console.error(`❌ ${waitMs / 1000}s 内页面未就绪。最后状态：${lastNote || '(无)'}（App pid=${appPid()}）`)
  return null
}

let cdp = await ensureRunning()
if (!cdp) process.exit(3)

/**
 * force-stop 之后**必须整条重开通道**。
 *
 * ⚠️ 2026-10-06 实吃：UI-06c（系统字号 1.3）判据恒红，理由写着「重启后页面
 * 未就绪」—— 病根不是页面没起来，而是 `adb-cdp.mjs` 的 `send` **不自动重连**：
 * WebView 进程被 force-stop 杀掉后 devtools socket 随之消失，手上那条 handle
 * 已经死了，后面每一次 `ev` 都只是超时。
 * 而「超时」这个症状长得和「App 没起来 / 页面没就绪」一模一样 ——
 * 我差点去调字体设置，实际要做的只是重开一条连接。
 * ⇒ 症状不指向病因：进程重启类操作之后，**先重开通道，再谈就绪**。
 */
async function reconnectCdp(why) {
  console.log(`  · 重开 CDP 通道（${why}）`)
  try { if (cdp) await cdp.close() } catch { /* 已断 */ }
  cdp = null
  const h = await ensureRunning()
  if (!h) { console.error('  ❌ 重开通道失败：页面仍未就绪'); return false }
  cdp = h
  return true
}

try {
  // ---- 鉴权：矩阵必须在**已登录外壳**上跑 --------------------------------
  // 停在登录页时，量到的是登录页不是应用外壳 —— 布局结论全部无效。
  // 这不是「跳过」，是必须先解决的前置条件。
  const TOKEN = process.env.MATRIX_TOKEN
  if (TOKEN) {
    await cdp.ev(`(() => {
      localStorage.setItem('pocket_token', ${JSON.stringify(TOKEN)});
      localStorage.setItem('pocket_user', JSON.stringify({ id: 'device-matrix', name: 'device-matrix', role: 'tenant_admin' }));
      localStorage.setItem('pocket_workspace_id', 'dev-ws');
      return 1;
    })()`, EVM)
    await cdp.ev('location.hash = "#/ai"', EVM)
    await sleep(600)
    await cdp.ev('location.reload()', EVM)
    console.log('  · 已注入 token 并 reload')
    // reload 后要重新等页面就绪，否则下面量的是 about:blank
    const t2 = Date.now()
    while (Date.now() - t2 < 60000) {
      await sleep(2500)
      try {
        const r = await cdp.ev('({href: location.href, w: innerWidth})', EVM)
        if (String(r?.href || '').startsWith('http') && r.w > 0) { console.log(`  · reload 后就绪 ${r.href}`); break }
      } catch { /* reload 期间通道会断，属正常 */ }
    }
    // ⚠️ 2026-10-06 全量实跑抓到的**污染源**，这里必须主动补：
    //   `location.reload()` 会把**内存里的本地加密库解锁态冲掉**
    //   （`routeGuards.ts` 的 `isLobsterReady()` 随之变 false）。
    //   boot 时看不出问题 —— 应用停在 `#/ai`，`/ai` **不** requiresLobster，
    //   所以日志打的是「解锁：无需解锁」，**看起来一切正常**。
    //   真正的后果出现在后面：UI-05 导航到 `/email`（`requiresLobster: true`）
    //   时被 Case C 弹到 `/login?returnTo=/email&unlock=1`，
    //   那一屏渲染的是 `login-view`、没有底栏 ⇒ UI-05a/05b 报红。
    //   症状（某条路由「未渲染外壳」）离病因（三行之前的 reload）隔了整轮。
    //   ⇒ 这里显式解锁一次，让后续所有用例从**已解锁**这个已知状态起步。
    //     单跑某条用例时往往「碰巧」已解锁，所以问题只在全量跑时暴露。
    const u0 = await unlockIfNeeded(cdp)
    if (u0.unlocked) {
      console.log('  · reload 后已重新解锁本地加密库（否则 requiresLobster 路由会被守卫弹到解锁页）')
    } else {
      console.log(`  · ⚠️ reload 后未能确认解锁：${u0.why}`)
    }
  }

  const unlockRes = await unlockIfNeeded(cdp)
  if (!unlockRes.unlocked) console.log(`  · 解锁：${unlockRes.why}`)

  const shell = await waitForShell(cdp)
  if (!shell.ready) {
    console.error(`❌ 外壳未就绪（${shell.why}）——在未渲染的页面上量布局只会得到恒真结果，本矩阵中止。`)
    process.exitCode = 5
  }

  const boot = await cdp.ev('({href: location.href, w: innerWidth, h: innerHeight, dpr: devicePixelRatio})', EVM)
  console.log(`\n=== 设备量具自证 ===`)
  console.log(`  包     ${PKG}`)
  console.log(`  序列号 ${SERIAL}`)

  // ---- 陈旧构建闸门：先说清楚「量的是哪一份」，再谈读数 ----
  // 2026-10-05 实吃：矩阵默认量 .sttdev，而当天构建的 APK 是 .matrix ⇒
  // 整整一轮 31/42 都在量 9 小时前的老 bundle。UI-06d 的「顶栏被压扁 3px」
  // 读数完整得像产品缺陷，真因只是 AppLayout.vue:564 的 flex-shrink:0 不在旧包里。
  // ⇒ 照着那条红去改产品，等于往**已经正确**的代码上叠补丁。
  const APK = join(ROOT, 'frontend', 'android', 'app', 'build', 'outputs', 'apk', 'debug', 'app-debug.apk')
  let apkMs = null
  try { apkMs = statSync(APK).mtimeMs } catch (e) { /* 没有 APK ⇒ 交给下面的 null 判定 */ }
  const sv = staleBuildVerdict(readPkgUpdateTime(PKG, SERIAL), apkMs)
  console.log(`  构建   ${sv.why}`)
  if (sv.stale === true) {
    console.error(`\n❌ **设备上跑的是陈旧构建** —— 下面所有读数都不能用来判断当前源码。`)
    console.error(`   ${sv.why}`)
    console.error(`   先 adb install -r ${APK}，或把 POCKET_PKG 指向刚装的那个包。`)
    console.error(`   （MATRIX_ALLOW_STALE=1 可强行跑，但那批读数只能算历史记录，不能算证据。）`)
    if (process.env.MATRIX_ALLOW_STALE !== '1') process.exitCode = 7
  } else if (sv.stale === null) {
    console.error(`   ⚠️ ${sv.why}`)
  }

  console.log(`  路由   ${boot.href}`)
  console.log(`  视口   ${boot.w}×${boot.h} CSS px  @dpr ${boot.dpr}`)
  const vwDp = Math.round(boot.w)
  console.log(`  折算   约 ${vwDp}dp 宽 → 落在 ${vwDp < 560 ? 'compact' : vwDp < 840 ? 'medium' : 'expanded'} 档`)
  console.log('')

  if (boot.href.includes('#/login')) {
    console.error('❌ 还停在登录页，本矩阵必须在**已登录**外壳上跑，否则量的是登录页不是应用外壳。')
    console.error('   先注入 token 再重跑。')
    process.exitCode = 2
  } else {

    // ---- 量具自证：先证明「遮挡」这个量具有牙 ----
    if (!only || only === 'self-test') {
      console.log('--- 量具自证 ---')
      const probe = await cdp.ev(OCCLUSION_PROBE, EVM)
      record('S1', '找到底栏元素（精确类名 .bottom-nav）', probe.navFound,
        `navFound=${probe.navFound} navTop=${probe.navTop} navHeight=${probe.navHeight} display=${probe.navDisplay}`)
      // ★ 几何自证：真底栏是 position:fixed; bottom:0; height:var(--bottom-chrome-height)
      //   （BottomNav.vue:125），必须「贴底 + 高度 < 40% 视口 + 宽度 > 80% 视口」。
      //   缺这条时量具会拿一个 842px 高的容器当底栏，随后所有「遮挡」结论作废。
      const navInfo = await cdp.ev(NAV_FIND, EVM)
      record('S1b', '底栏几何自证（贴底且高度合理）', navInfo.looksLikeBottomBar === true,
        `cls=${navInfo.cls} top=${navInfo.top} bottom=${navInfo.bottom} h=${navInfo.h} pos=${navInfo.position} 像底栏=${navInfo.looksLikeBottomBar}${navInfo.nearMiss ? '；近似候选=' + JSON.stringify(navInfo.nearMiss) : ''}`)
      record('S2', '阳性对照：底栏中心点的命中者必须是底栏自己',
        probe.probeHitIsNav === true,
        `probe=(${probe.probe?.x},${probe.probe?.y}) 命中=${probe.probeHit} 属于底栏=${probe.probeHitIsNav}`)
      // 负对照：随便取一个视口角落之外的点，命中者不该是底栏
      const neg = await cdp.ev(`(() => {
        const nav = document.querySelector('.bottom-nav');
        const hit = document.elementFromPoint(Math.round(innerWidth/2), 30);
        return { hit: hit ? hit.tagName + '.' + String(hit.className||'').split(' ')[0] : null,
                 isNav: !!(hit && nav && nav.contains(hit)) };
      })()`, EVM)
      record('S3', '负对照：视口顶部中心的命中者不该是底栏',
        neg.isNav === false,
        `命中=${neg.hit} 属于底栏=${neg.isNav}（若为 true 则「S2 全绿」是假阳性）`)
      console.log('')
    }

    // ---- UI-05：横向溢出 + 底栏档位 ----
    if (!only || only === 'ui-05') {
      console.log('--- UI-05 横向溢出 / 底栏档位 ---')
      const routes = ['/ai', '/email', '/meetings', '/notes', '/rss', '/settings']
      let worst = null
      const blankRoutes = []
      const shellMissing = []
      const unsettled = []
      const samples = []
      for (const r of routes) {
        const g = await goto(cdp, '#' + r)
        const st = g.settle
        // ⚠️ 先看**落点**，再看稳不稳：`goto` 重试后仍没落到目标时，
        //   量的是**别的页面**（多半是 `#/login?...&unlock=1`）。
        //   只报「未稳定」会让人以为是产品抖，而成因是守卫把路由弹走了。
        if (g.onTarget === false) {
          unsettled.push(r)
          console.log(`    ${r.padEnd(10)} ⚠️ **没落在目标**（请求 ${g.requested}，实际 ${g.landed}）—— 量到的是别的页面，这条不成立`)
          continue
        }
        if (!st || !st.settled) {
          unsettled.push(r)
          console.log(`    ${r.padEnd(10)} ⚠️ 40s 内未稳定（len=${st?.len ?? '?'} kids=${st?.kids ?? '?'}）—— 这条测量不成立`)
          continue
        }
        console.log(`    ${r.padEnd(10)} 稳定于第 ${st.rounds} 轮（len=${st.len} main 首子=${st.first || '—'}）`)
        const m = await cdp.ev(`(() => {
          const de = document.documentElement;
          const nav = document.querySelector('.bottom-nav');
          return {
            scrollWidth: de.scrollWidth, clientWidth: de.clientWidth,
            innerWidth: innerWidth,
            bodyScrollWidth: document.body.scrollWidth,
            navDisplay: nav ? getComputedStyle(nav).display + ((() => { const b = nav.getBoundingClientRect(); return (b.bottom >= innerHeight - 2 && b.height < innerHeight * 0.4) ? '' : ' [几何不符]'; })()) : 'ABSENT',
            text: String(document.body.innerText||'').slice(0,40).replace(/\\s+/g,' '),
            textLen: String(document.body.innerText||'').trim().length,
          };
        })()`, EVM)
        const overflow = Math.max(m.scrollWidth, m.bodyScrollWidth) - m.clientWidth
        const line = `${r.padEnd(10)} scrollW=${m.scrollWidth} clientW=${m.clientWidth} 溢出=${overflow}px nav=${m.navDisplay} | ${m.text}`
        console.log(`    ${line}`)
        // ★ 前提自证：未渲染的页面上 scrollWidth === clientWidth **必然**成立，
        //   所以「溢出 0」在空页面上是恒真的。必须先证明这一屏真的渲染出了东西。
        if (m.textLen < 5) { blankRoutes.push(r); console.log(`      ⚠️ 文本仅 ${m.textLen} 字 —— 未渲染，这条测量不成立`); }
        // ⚠️ 2026-10-06 实吃，**上一版的自证不够**：`textLen >= 5` 挡不住
        //   「回退外壳」。实测 /email /meetings /notes /rss 四条全部返回
        //   **一模一样的** 40 字（`Skip to main content 🦞 OpenCode Pocket`）且
        //   `nav=ABSENT` —— 它们停在解锁/未初始化态，**没在渲染自己的内容**。
        //   但壳层有文字、且宽度天然等于视口，所以「溢出 0」照样成立 ⇒
        //   上一版把 4 条没渲染的路由算进了「6/6 全绿」。
        // ⚠️⚠️ 而「紧凑档必须有底栏」这个**更严的**版本第一版又写错了：
        //   它对所有路由一刀切，而 **66 条路由声明 `bottomNav: false`**（含 /meetings）
        //   ⇒ 会把合法的无底栏页判成「没渲染外壳」。**防虚假的自证自己制造了假红。**
        //   ⇒ 必须逐条问源码「这条路由**声明**要不要底栏」（见 routeBottomNav）。
        const bn = routeBottomNav(r)
        const navAbsent = m.navDisplay === 'ABSENT'
        if (vwDp < 560 && bn.known && bn.value && navAbsent) {
          shellMissing.push(r)
          console.log(`      ⚠️ 声明 bottomNav=true 的紧凑路由却没有底栏 ⇒ 这一屏没渲染出应用外壳，这条测量不成立`)
        } else if (vwDp < 560 && bn.known && !bn.value && navAbsent) {
          console.log(`      · 该路由声明 bottomNav: ${bn.declared} ⇒ 底栏本就不该有，不算缺外壳`)
        } else if (!bn.known) {
          console.log(`      ⚠️ ${bn.why} ⇒ 无法判这条路由该不该有底栏`)
        }
        if (worst === null || overflow > worst.overflow) worst = { r, overflow, ...m, bn }
        samples.push({ r, navDisplay: m.navDisplay, overflow, bn })
      }
      record('UI-05a', `所有受测路由横向溢出 ≤1px 且都真的渲染出了外壳（最差：${worst?.r} = ${worst?.overflow}px）`,
        !!worst && worst.overflow <= 1 && blankRoutes.length === 0 && shellMissing.length === 0 && unsettled.length === 0,
        `受测 ${routes.length} 条，有效 ${samples.length} 条；最差路由 ${worst?.r} 溢出 ${worst?.overflow}px（clientW=${worst?.clientWidth}）；` +
        `未稳定 ${unsettled.length} 条${unsettled.length ? '：' + unsettled.join(',') : ''}；` +
        `空白路由 ${blankRoutes.length} 条${blankRoutes.length ? '：' + blankRoutes.join(',') : ''}；` +
        `未渲染外壳 ${shellMissing.length} 条${shellMissing.length ? '：' + shellMissing.join(',') : ''}`)
      // ⚠️ 2026-10-06 实吃：**这一条我原先写了一个不存在的期望值。**
      //   原来按 `vwDp >= 560` 断言「底栏应隐藏」——但 `AppLayout.vue:327-339`
      //   的 `showBottomNav` **完全没有宽度条件**：它只看 `route.meta.bottomNav`
      //   是否 false，外加一个「平板 + sessions + 选了 detail」的特例。
      //   ⇒ 产品在 600/960px 也会显示底栏，而我的判据会把「显示 flex」判成错。
      //   **一个凭空想的期望值，在它最该生效的宽度上会稳定误报红。**
      //   ⇒ 期望值改为**照抄源码**：`meta.bottomNav !== false` 就显示，与宽度无关。
      //     顺带把「平板 + sessions + selected」那个特例也如实排除。
      const navRoutes = samples.filter((s) => s.bn?.known && s.bn.value)
      const navMissing = navRoutes.filter((s) => s.navDisplay === 'ABSENT' || s.navDisplay === 'none')
      const navWrong = navRoutes.filter((s) => s.navDisplay !== 'ABSENT' && s.navDisplay !== 'none' && !/flex/.test(s.navDisplay))
      const navlessDeclared = samples.filter((s) => s.bn?.known && !s.bn.value)
      record('UI-05b', `声明要有底栏的路由在 ${vwDp}dp 全部显示且为 flex（期望来自 AppLayout.showBottomNav，与宽度无关）`,
        navMissing.length === 0 && navWrong.length === 0,
        `声明 bottomNav:true 的 ${navRoutes.length} 条（源码规则：meta.bottomNav!==false 即显示，**无宽度条件**）；` +
        `缺失/被隐藏 ${navMissing.length} 条${navMissing.length ? '：' + navMissing.map((s) => `${s.r}=${s.navDisplay}`).join(',') : ''}；` +
        `显示但非 flex ${navWrong.length} 条${navWrong.length ? '：' + navWrong.map((s) => `${s.r}=${s.navDisplay}`).join(',') : ''}；` +
        `另有 ${navlessDeclared.length} 条声明 bottomNav:false（如 ${navlessDeclared.map((s) => s.r).join(',') || '—'}）不参与本条`)
      console.log('')
    }

    // ---- 断点边界扫描（2026-10-06 新增）----
    // 为什么要扫宽度：UI 矩阵的 UI-05 声称覆盖 320/390/600/960/1280，
    // 而真机实跑**只有 411dp 一个宽度**。断点阶梯（380/560/840/1280）
    // 在真 Android WebView 上到底成不成立，从来没人验过 ——
    // 浏览器 e2e 量的是桌面 Chromium，media query 的求值环境并不完全相同。
    //
    // 手法：`Emulation.setDeviceMetricsOverride` 逐个视口覆盖，**不碰设备本身**
    // （`adb shell wm size` 会改动整机状态且难还原）。跑完必须 clear 复原。
    //
    // ⚠️ 扫的是**边界的前一像素与后一像素**，不是随便取几个宽度。
    //   只在 320/600/960 采样，一个把断点写成 561px 的实现照样全绿。
    if (!only || only === 'ui-bp') {
      console.log('--- 断点边界扫描（真 Android WebView · CDP 视口覆盖）---')
      // 期望值全部从 SSOT 读，不写死
      const bpCss = readFileSync(join(SRC, 'styles', 'breakpoints.css'), 'utf8')
      const bp = (k) => Number((new RegExp(`--${k}:\\s*(\\d+)px`).exec(bpCss) || [])[1])
      const NARROW_MAX = bp('bp-narrow-max'), MEDIUM_MIN = bp('bp-medium-min')
      const EXPANDED_MIN = bp('bp-expanded-min'), WIDE_MIN = bp('bp-wide-min')
      console.log(`  SSOT（breakpoints.css）：narrow≤${NARROW_MAX} / medium≥${MEDIUM_MIN} / expanded≥${EXPANDED_MIN} / wide≥${WIDE_MIN}`)
      if (!NARROW_MAX || !MEDIUM_MIN || !EXPANDED_MIN || !WIDE_MIN) {
        record('UI-BP0', '断点 SSOT 可从 breakpoints.css 读出', false, '读不到 --bp-* 变量，本组判据不成立')
      } else {
        // 探针：注入一个同时挂上 .bp-* 工具类的元素，量它的 computed display。
        // ⚠️ 必须说清：这验的是**媒体查询在真机 WebView 里的求值**，
        //   **不是**「应用用了这些工具类」——`breakpoints.css` 的注释要求
        //   使用方改用 .bp-*，但实测 8 个类名在 .vue 里**一个都没被用到**。
        //   探针是合成元素，结论只对「media query 本身」成立。
        const PROBE = `(() => {
          const old = document.getElementById('__bp_probe__');
          if (old) old.remove();
          const mk = (cls) => { const d = document.createElement('div');
            d.className = cls; d.style.cssText='position:absolute;left:-9999px;top:0;width:1px;height:1px';
            document.body.appendChild(d);
            return getComputedStyle(d).display; };
          const r = { w: innerWidth,
            hideOnCompact: mk('bp-hide-on-compact'), showOnCompact: mk('bp-show-on-compact'),
            showOnExpanded: mk('bp-show-on-expanded'), hideOnExpanded: mk('bp-hide-on-expanded'),
            showOnWide: mk('bp-show-on-wide'), hideOnWide: mk('bp-hide-on-wide'),
            hideOnNarrow: mk('bp-hide-on-narrow'), showOnNarrow: mk('bp-show-on-narrow') };
          r.overflow = Math.max(document.documentElement.scrollWidth, document.body.scrollWidth)
                     - document.documentElement.clientWidth;
          r.nav = (() => { const n = document.querySelector('.bottom-nav');
            return n ? getComputedStyle(n).display : 'ABSENT'; })();
          return r;
        })()`
        const pairs = [
          // narrow 是唯一的 max-width 断点，必须同时量 N-1 / N / N+1
          // ——「N-1 命中、N 不命中」这个形态，用只量 N 和 N+1 是看不出来的
          { name: 'narrow-1', edge: NARROW_MAX, at: NARROW_MAX - 1 },
          { name: 'narrow', edge: NARROW_MAX, at: NARROW_MAX },
          { name: 'narrow+1', edge: NARROW_MAX, at: NARROW_MAX + 1 },
          { name: 'medium', edge: MEDIUM_MIN, at: MEDIUM_MIN },
          { name: 'medium+1', edge: MEDIUM_MIN, at: MEDIUM_MIN + 1 },
          { name: 'expanded', edge: EXPANDED_MIN, at: EXPANDED_MIN },
          { name: 'expanded+1', edge: EXPANDED_MIN, at: EXPANDED_MIN + 1 },
          { name: 'wide', edge: WIDE_MIN, at: WIDE_MIN },
          { name: 'wide+1', edge: WIDE_MIN, at: WIDE_MIN + 1 },
        ]
        const results = []
        for (const p of pairs) {
          // 高度取一个平板常见值；关键是宽度
          await cdp.send('Emulation.setDeviceMetricsOverride',
            { width: p.at, height: 900, deviceScaleFactor: 2, mobile: true }, EVM)
          await sleep(1500)
          const m = await cdp.ev(PROBE, EVM)
          results.push({ ...p, m })
          console.log(`    ${p.name.padEnd(12)} ${String(p.at).padStart(5)}px → innerWidth=${m.w} ` +
            `narrow:${m.hideOnNarrow}/${m.showOnNarrow} compact:${m.hideOnCompact}/${m.showOnCompact} ` +
            `expanded:${m.showOnExpanded}/${m.hideOnExpanded} wide:${m.showOnWide}/${m.hideOnWide} ` +
            `溢出=${m.overflow}px nav=${m.nav}`)
        }
        await cdp.send('Emulation.clearDeviceMetricsOverride', {}, EVM)
        await sleep(1000)

        // 断点 n 的判据：**期望值必须按 CSS 规则的方向来，不能一律按 none。**
        // ⚠️ 我第一版把四条全写成「到达边界 ⇒ 该类为 none」，于是 560/840/1280
        //   三条**期望反了**、把正确实现判成红。
        //   正确方向（对照 breakpoints.css 逐条读出来的）：
        //     `@media (min-width: N)` → 到达时 **show-on-X 显示、hide-on-X 隐藏**
        //     `@media (max-width: N)` → 到达时 **hide-on-narrow 隐藏、show-on-narrow 显示**
        //   ⇒ 三条 min-width 用「show 类应显示」，一条 max-width 用「show 类应显示」，
        //     但**依据不同**：min-width 查 show 变 block，max-width 查 hide 变 none。
        const checkEdge = (edge, at) => {
          const r = results.find((x) => x.at === at)
          if (!r) return null
          const map = {
            [NARROW_MAX]: ['showOnNarrow', 'block', 'max-width:N 到达时 show 类应显示'],
            [MEDIUM_MIN]: ['hideOnCompact', 'block', 'min-width:N 到达时 hide 类应复位'],
            [EXPANDED_MIN]: ['showOnExpanded', 'block', 'min-width:N 到达时 show 类应显示'],
            [WIDE_MIN]: ['showOnWide', 'block', 'min-width:N 到达时 show 类应显示'],
          }
          const [k, expect, why] = map[edge]
          return { edge, at, ok: r.m[k] === expect, got: r.m[k], expect, why, k }
        }
        const edges = [NARROW_MAX, MEDIUM_MIN, EXPANDED_MIN, WIDE_MIN]
        const edgeRows = edges.map((e) => checkEdge(e, e)).filter(Boolean)
        const edgeBad = edgeRows.filter((r) => !r.ok)
        record('UI-BP1', `四个断点在真机上于 SSOT 边界**到达即生效**（${edges.join(' / ')}）`, edgeBad.length === 0,
          edgeRows.map((r) => `${r.at}px→${r.k}=${r.got}（期望 ${r.expect}）`).join('；') +
          (edgeBad.length ? `；**${edgeBad.length} 处未生效**：${edgeBad.map((r) => `${r.at}px ${r.k} 得到 ${r.got}`).join('，')}` : '；全部正确'))

        // UI-BP1b：narrow 那条边界的**配对**（N 与 N-1 命中、N+1 失效）。
        // ⚠️ 2026-10-06 记一条走过的弯路：这条的标题原来叫「max-width 那条边界」，
        //   因为当时 narrow 用的是 `@media (max-width: 380px)`。真机实测发现
        //   **本设备上 `max-width: N` 系统性等价于 `< N`**。我第一版修法是把字面量
        //   减 1（380→379），重建 APK 复量才发现**命中集合从 {≤379} 变成 {≤378}，
        //   整体下移一格、并没有对齐**。
        // ⇒ 「改了没变差」不等于「改对了」；**边界类修改必须重建后在设备上复量**。
        // ⇒ 正确修法是改用等价的 `min-width: 381px`（本设备 min-width 闭区间且正确），
        //   narrow 集合回到 {≤380}，与 token 与 `isNarrow` 三者一致。
        const nAt = results.find((x) => x.at === NARROW_MAX)
        const nAfter = results.find((x) => x.at === NARROW_MAX + 1)
        const nBefore = results.find((x) => x.at === NARROW_MAX - 1)
        const narrowOk = !!nAt && nAt.m.showOnNarrow === 'block'
          && nBefore?.m.showOnNarrow === 'block' && nAfter?.m.showOnNarrow === 'none'
        record('UI-BP1b', `narrow 边界（${NARROW_MAX}）满足「N-1/N 命中、N+1 失效」`,
          narrowOk,
          `${NARROW_MAX - 1}px→${nBefore?.m.showOnNarrow ?? '?'}、${NARROW_MAX}px→${nAt?.m.showOnNarrow}（期望 block）、` +
          `${NARROW_MAX + 1}px→${nAfter?.m.showOnNarrow}（期望 none）` +
          (narrowOk
            ? ` ⇒ narrow 集合 = {≤ ${NARROW_MAX}}，与 --bp-narrow-max 与 isNarrow(width <= ${NARROW_MAX}) 一致`
            : `；⚠️ narrow 集合不等于 {≤ ${NARROW_MAX}} ⇒ CSS 与 TS 的档位判定落在了不同像素上`))

        const overflowBad = results.filter((r) => r.m.overflow > 1)
        record('UI-BP2', '八个边界宽度下横向溢出均 ≤1px', overflowBad.length === 0,
          `实测 ${results.map((r) => `${r.at}px=${r.m.overflow}`).join('，')}`)

        // 边界处的 0/1 差异也顺带记下：真实在 innerWidth 上生效了吗
        const wBad = results.filter((r) => r.m.w !== r.at)
        record('UI-BP3', '视口覆盖真的生效（innerWidth 等于请求值）', wBad.length === 0,
          `请求与实测不符 ${wBad.length} 处${wBad.length ? '：' + wBad.map((r) => `请求 ${r.at} 实得 ${r.m.w}`).join('，') : ''}`)
        console.log('')
      }
    }

    // ---- UI-06：旋转 / 分屏 / 字体放大后吸顶不错位（2026-10-06 新增）----
    // 这条的病因天然在真机：横竖屏切换、系统字号、分屏都会改变布局，
    // 而桌面 Chromium 上按窗口 resize 模拟不出系统字号那一层。
    //   · 旋转 / 分屏：用 CDP 视口覆盖（与 UI-BP 同一手法，不碰设备）
    //   · 字体放大：**必须动系统设置**（adb settings put system font_scale），
    //     跑完**必须还原**——它是整机状态，留在 1.3 会影响后续所有测量。
    if (!only || only === 'ui-06') {
      console.log('--- UI-06 旋转 / 分屏 / 字体放大 ---')
      const MEASURE = `(() => {
        const de = document.documentElement;
        const bar = document.querySelector('.top-bar');
        const br = bar ? bar.getBoundingClientRect() : null;
        const bs = bar ? getComputedStyle(bar) : null;
        const h1 = document.querySelector('.top-bar h1.title');
        // ⚠️ 2026-10-06 修的量具缺陷：原先只探
        //   'main p, main li, main h2, main h3, main .subtitle'，
        //   页面为空（#/notes 无数据）时返回 null，于是「前提自证」判红，
        //   读起来像「字号没生效」，实际是**探针没找到元素**。
        //   ⇒ 改成在 main 里按标签优先级找**第一个有实际文本**的元素，
        //   并把 main 的文本长度一并带回来，好区分「页面真没字」与「选择器太窄」。
        let probeText = null;
        const sels = ['p', 'li', 'h2', 'h3', 'h4', 'span', 'a', 'button', 'label', 'td'];
        for (const sel of sels) {
          const els = document.querySelectorAll('main ' + sel);
          for (const e of els) {
            if (String(e.textContent || '').trim().length > 1) { probeText = e; break; }
          }
          if (probeText) break;
        }
        const mainTextLen = String((document.querySelector('main') || {}).textContent || '').trim().length;
        const layout = document.querySelector('.app-layout');
        const content = document.querySelector('.content');
        // ⚠️ 期望值不许我凭空写。顶栏的「正确顶端位置」不是 0：安全区的唯一
        // 来源是 body 的 padding-top（styles.css:88 → --app-safe-top），本设备
        // 实测 24px，顶栏理应贴在**安全区内容区**顶端 ⇒ 期望值从这里读，不写死。
        const safeTop = parseFloat(getComputedStyle(document.body).paddingTop) || 0;
        // 顶栏是定高 chrome；父容器是 flex column（AppLayout.vue:509），而
        // .top-bar 没有声明 flex-shrink ⇒ 默认 1，在矮视口下**可以被压缩**。
        // 记下 shrink 与两端高度，用来判「有没有被压扁」而不是只看 top 对不对。
        const ovs = Array.prototype.slice.call(document.querySelectorAll('.summary-panel, .alert-toast'), 0, 3);
        const ovList = ovs.map(function (o) {
          const r = o.getBoundingClientRect();
          const cs = getComputedStyle(o);
          const ovl = br ? Math.max(0, Math.min(r.bottom, br.bottom) - Math.max(r.top, br.top)) : null;
          return { cls: String(o.className).slice(0, 40), top: Math.round(r.top), h: Math.round(r.height),
                   cssTop: cs.top, pos: cs.position, z: cs.zIndex, overlapBar: ovl === null ? null : Math.round(ovl) };
        });
        return {
          w: innerWidth, h: innerHeight, dpr: devicePixelRatio,
          overflow: Math.max(de.scrollWidth, document.body.scrollWidth) - de.clientWidth,
          // 吸顶是否还在顶端、是否横跨整个视口
          barTop: br ? Math.round(br.top) : null,
          barW: br ? Math.round(br.width) : null,
          barH: br ? Math.round(br.height) : null,
          barBottom: br ? Math.round(br.bottom) : null,
          barPos: bs ? bs.position : null,
          barShrink: bs ? bs.flexShrink : null,
          safeTop: safeTop,
          layoutH: layout ? layout.offsetHeight : null,
          contentShrink: content ? getComputedStyle(content).flexShrink : null,
          barFontPx: h1 ? parseFloat(getComputedStyle(h1).fontSize) : null,
          textFontPx: probeText ? parseFloat(getComputedStyle(probeText).fontSize) : null,
          mainTextLen: mainTextLen,
          overlays: ovList,
          nav: (() => { const n = document.querySelector('.bottom-nav');
            return n ? getComputedStyle(n).display : 'ABSENT'; })(),
        };
      })()`

      const scenarios = [
        { name: 'A 竖屏基线', w: 411, h: 914, font: null },
        { name: 'B 横屏（旋转）', w: 914, h: 411, font: null },
        { name: 'C 分屏（上半屏）', w: 411, h: 500, font: null },
      ]
      // ⚠️ 2026-10-06 修：**基线与被测场景必须同一个路由**。
      // 原来场景 A 量的是矩阵自证时所在的路由（#/ai），而场景 D 硬编码
      // `#/notes` —— 于是 UI-06f 在拿**两个不同页面**的字号作比较。
      // #/notes 无数据时正文探针返回 null，看起来像「字号没生效」，
      // 实际上顶栏标题已经 16 → 20.8px（正好 ×1.3）了。
      // ⇒ 基线与 D 都固定到 `BASE_HASH`，比较才成立。
      const BASE_HASH = '/#/ai'
      await goto(cdp, BASE_HASH)
      await sleep(1200)
      const rows = []
      for (const s of scenarios) {
        await cdp.send('Emulation.setDeviceMetricsOverride',
          { width: s.w, height: s.h, deviceScaleFactor: 2, mobile: true }, EVM)
        await sleep(2200)
        const m = await cdp.ev(MEASURE, EVM)
        rows.push({ ...s, m })
        console.log(`  ${s.name.padEnd(16)} ${m.w}×${m.h}  溢出=${m.overflow}px  安全区顶=${m.safeTop}px  top-bar top=${m.barTop} 底=${m.barBottom} w=${m.barW} h=${m.barH} pos=${m.barPos} shrink=${m.barShrink}  标题=${m.barFontPx}px 正文=${m.textFontPx}px nav=${m.nav}`)
      }
      await cdp.send('Emulation.clearDeviceMetricsOverride', {}, EVM)
      await sleep(1000)

      // ---- 场景 D：系统字体放大（动整机设置，跑完必须还原）----
      let fontScaleRestored = true
      let dRow = null
      let dFail = ''
      const orig = sh(['settings', 'get', 'system', 'font_scale']) || '1.0'
      try {
        sh(['settings', 'put', 'system', 'font_scale', '1.3'])
        await sleep(1200)
        // 系统字号要重启 WebView 才生效。⚠️ force-stop 会连 WebView 进程一起
        // 杀掉 ⇒ **手上的 CDP 通道必然失效**，必须整条重开（见 reconnectCdp）。
        // 旧代码只 restart App 然后继续用旧 handle，于是每条 ev 都超时，
        // 被记成「页面未就绪」—— 那是**量具坏了**，不是场景没成立。
        try { execFileSync(ADB, ['-s', SERIAL, 'shell', 'am', 'force-stop', PKG], { timeout: 20000 }) } catch { /* ignore */ }
        await sleep(2500)
        const back = await reconnectCdp('系统字号变更后 force-stop，WebView 进程已换')
        if (!back) {
          dFail = 'force-stop 后 CDP 通道重开失败'
        } else {
          // 重启后自证还在**已登录外壳**上：token 落 localStorage（持久化），
          // 但不能假设 —— 量到登录页的话下面所有布局结论都无效。
          if (TOKEN) {
            await cdp.ev(`(() => {
              localStorage.setItem('pocket_token', ${JSON.stringify(TOKEN)});
              localStorage.setItem('pocket_user', ${JSON.stringify({ id: 'device-matrix', name: 'device-matrix', role: 'tenant_admin' })});
              localStorage.setItem('pocket_workspace_id', 'dev-ws');
              return 1;
            })()`, EVM)
          }
          await cdp.ev('location.hash = "#/ai"', EVM)
          await sleep(500)
          await cdp.ev('location.reload()', EVM)
          await sleep(3000)
          const shellD = await waitForShell(cdp)
          if (!shellD.ready) {
            dFail = '重启后外壳未就绪：' + shellD.why
          } else {
            await goto(cdp, BASE_HASH)
            dRow = { name: 'D 字体放大 1.3', m: await cdp.ev(MEASURE, EVM), relaunched: true }
            console.log(`  ${dRow.name.padEnd(16)} ${dRow.m.w}×${dRow.m.h}  溢出=${dRow.m.overflow}px  安全区顶=${dRow.m.safeTop}px  top-bar top=${dRow.m.barTop} 底=${dRow.m.barBottom} w=${dRow.m.barW} h=${dRow.m.barH} pos=${dRow.m.barPos} shrink=${dRow.m.barShrink}  标题=${dRow.m.barFontPx}px 正文=${dRow.m.textFontPx}px`)
          }
        }
      } catch (e) {
        dFail = '场景 D 抛错：' + String(e.message).slice(0, 70)
        console.log(`  D 字体放大：${dFail}`)
      } finally {
        sh(['settings', 'put', 'system', 'font_scale', orig])
        const now = sh(['settings', 'get', 'system', 'font_scale'])
        fontScaleRestored = now === orig
        console.log(`  · font_scale 已还原为 ${now}（原值 ${orig}）${fontScaleRestored ? '' : ' ⚠️ 还原失败'}`)
        try { execFileSync(ADB, ['-s', SERIAL, 'shell', 'am', 'force-stop', PKG], { timeout: 20000 }) } catch { /* ignore */ }
        await sleep(1500)
        // 还原同样换了 WebView 进程 ⇒ 同样要重开通道，否则**后续所有场景**
        // 都建立在一条死连接上（那会伪装成「后面几条莫名其妙全红」）。
        await reconnectCdp('font_scale 还原后 force-stop')
      }

      // ---- 判定 ----
      const badOverflow = rows.filter((r) => r.m.overflow > 1)
      record('UI-06a', '旋转 / 分屏后横向溢出仍 ≤1px', badOverflow.length === 0,
        `实测 ${rows.map((r) => `${r.name}=${r.m.overflow}px`).join('，')}`)

      // ⚠️ 2026-10-06 自我推翻：原来这里写的是 `barTop > 2 ⇒ 错位`。
      // 那个 2 是我**凭空想的**，实测三场景 top 全是 24 —— 而 24 不是错位，
      // 是安全区：顶栏的「正确顶端位置」本来就等于 body 的 padding-top
      // （styles.css:88 → --app-safe-top，AppLayout 顶栏自己不加，
      // 源码注释明写「再加会双重下移」）。⇒ 期望值必须从应用自己的 SSOT 读。
      //
      // 换成三条**有出处**的判据：
      //   ① 顶栏贴在安全区内容区顶端：|barTop − bodyPaddingTop| ≤ 1
      //   ② 顶栏横跨整个视口：|barW − innerWidth| ≤ 2
      //   ③ 吸顶语义没丢：position ∈ {sticky, fixed}
      // 再加一条**跨场景一致性**：三个场景的安全区内偏移必须完全相同 ——
      // 「旋转后顶栏悄悄往下挪 1px」正是这条要抓的，它不会被单场景判据发现。
      const topbarH = Number(/--topbar-height:\s*(\d+)px/.exec(
        readFileSync(join(SRC, 'styles', 'tokens.css'), 'utf8'))?.[1] || 0)
      const barWhy = (r) => {
        const why = []
        if (Math.abs((r.m.barTop ?? -999) - r.m.safeTop) > 1) why.push(`未贴安全区顶端（top=${r.m.barTop} 安全区=${r.m.safeTop}）`)
        if (Math.abs((r.m.barW ?? -999) - r.m.w) > 2) why.push(`未铺满视口（w=${r.m.barW}/${r.m.w}）`)
        if (r.m.barPos !== 'sticky' && r.m.barPos !== 'fixed') why.push(`吸顶语义丢失（position=${r.m.barPos}）`)
        return why
      }
      const barBad = rows.filter((r) => barWhy(r).length > 0)
      record('UI-06b', `吸顶顶栏在旋转 / 分屏后贴在安全区顶端且铺满视口（期望 top 读自 body padding-top=${rows[0].m.safeTop}px，非写死）`,
        barBad.length === 0 && topbarH > 0,
        rows.map((r) => `${r.name}: top=${r.m.barTop}/安全区${r.m.safeTop} w=${r.m.barW}/${r.m.w} pos=${r.m.barPos} h=${r.m.barH}`).join('；') +
        (barBad.length ? `；**${barBad.length} 个场景错位**：${barBad.map((r) => `${r.name}[${barWhy(r).join('/')}]`).join('，')}` : ''))

      const offs = rows.map((r) => (r.m.barTop ?? 0) - r.m.safeTop)
      const sameOff = offs.every((o) => Math.abs(o - offs[0]) <= 1)
      record('UI-06c', '吸顶内偏移跨场景一致（旋转 / 分屏不会把顶栏悄悄挪位）', sameOff,
        `安全区内偏移 ${offs.map((o, i) => `${rows[i].name}=${o}`).join('，')}px`)

      // 顶栏是**定高 chrome**（token --topbar-height，声明在 .top-bar 上），
      // 父容器 .app-layout 是 flex column（AppLayout.vue:509），而 .top-bar
      // 没有声明 flex-shrink ⇒ 默认 1。CSS Flexbox §9.7 允许 flex item 的
      // 用尺寸偏离声明尺寸，于是矮视口下它会被压扁。
      // ⇒ 这条判「有没有被压扁」，判据 = 实测高度 == token 声明高度。
      // ⚠️ 若它红了，那是**产品缺陷**（顶栏高度随视口高度变化），不是判据错：
      // token 是静态值，而 top-bar 的语义就是固定高度的吸顶条。
      const squashed = rows.filter((r) => r.m.barH !== topbarH)
      record('UI-06d', `顶栏不被 flex 压缩，实测高度恒等于 token --topbar-height=${topbarH}px`,
        topbarH > 0 && squashed.length === 0,
        rows.map((r) => `${r.name}: h=${r.m.barH}px（token ${topbarH}px，flex-shrink=${r.m.barShrink}，视口高 ${r.m.h}px）`).join('；') +
        (squashed.length ? `；⚠️ **被压扁 ${squashed.map((r) => `${r.name} 差 ${r.m.barH - topbarH}px`).join('，')}** —— .app-layout 是 flex column 而 .top-bar 未声明 flex-shrink` : ''))

      // 下游影响：那两个浮层用 `top: calc(var(--topbar-height) + 8px)` 相对
      // **视口**定位，而顶栏实际底边在 safeTop + barH。两者相等才不重叠。
      // z-index 也读了：--z-sticky(50) < --z-fab(60) ⇒ 重叠时是**画在上面**，
      // 不是被顶栏盖住，所以这条要看真值，不能靠推理。
      const ovRows = rows.flatMap((r) => (r.m.overlays || []).map((o) => ({ ...o, scen: r.name })))
      if (ovRows.length) {
        const ovBad = ovRows.filter((o) => (o.overlapBar ?? 0) > 1)
        record('UI-06e', '顶栏下方的固定浮层不与顶栏重叠', ovBad.length === 0,
          ovRows.map((o) => `${o.scen} ${o.cls}: top=${o.top} css.top=${o.cssTop} z=${o.z} 与顶栏重叠 ${o.overlapBar}px`).join('；'))
      } else {
        // ---- 覆盖尝试：走会话详情页把 .summary-panel 真的渲染出来（2026-10-05 补） ----
        //
        // ⚠️ 原来这条记 ⬜ 的理由是「这两个类在这些路由上不可能出现」，
        // **那句话说错了**（docs 10 §4.0i-3 已订正）：`.summary-panel` 全仓有**两个**实现。
        //   ① LiveSummaryPanel —— 确实只在录音激活时挂载（`liveRecord.active`）；
        //   ② SessionSummaryRail —— 挂在 `SessionConversationView` 上，经 `#/sessions/:id`
        //      到达，**不需要录音激活**；`.alert-toast` 那半句仍成立（组件零引用）。
        // ⇒ 所以先真去把它渲染出来。**链上每一环都自证**：哪一环断了，理由就指名那一环，
        //   不再笼统写「不可能出现」—— 那种话把「没试过」说成了「试过且不可能」。
        const trail = []
        let reason = null
        let ovConv = null
        try {
          await goto(cdp, '#/sessions')
          await sleep(2000)
          const nRows = await cdp.ev('document.querySelectorAll(".session-card").length', EVM)
          trail.push(`#/sessions 行数=${nRows}`)
          if (!nRows) {
            reason = '「#/sessions」0 行 ⇒ 没有会话可点进详情页（后端无会话数据）'
          } else {
            const clicked = await cdp.ev(
              '(() => { const c = document.querySelector(".session-card"); if (c) c.click(); return !!c })()', EVM)
            await sleep(3000)
            const onConv = await cdp.ev('location.hash.startsWith("#/sessions/")', EVM)
            // ⚠️ 必须记「**去了哪**」，不能只记一个 false。
            //    第一版只记 `落点会话详情=false`，于是输出里看不出它其实跳去了
            //    `#/login?returnTo=...&unlock=1` —— 一个**被 auth 门拦住**的具体落点，
            //    却被读成「点不动」。**「没到达」必须说清「到了哪」，否则定位信息全丢。**
            const landed = await evSafeHref(cdp)
            trail.push(`点首行=${clicked} 落点会话详情=${onConv} 实际落点=${landed}`)
            if (!clicked || !onConv) {
              const gated = /unlock=1/.test(landed) || /#\/login/.test(landed)
              // ⚠️ 被**解锁门**拦下 ≠ 到不了。矩阵自带 `unlockIfNeeded`（真实流程、
              //    带 MASTER_PW、clickTries 重试），受保护路由本来就需要它 ——
              //    所以这里**必须真的走一遍解锁**，而不是记一句「拿不到」就算了。
              // ⚠️ 2026-10-06 实吃，这一条把整条覆盖链废掉了两轮：
              //   原来写的是 `/\/returnTo=(\/sessions\/[^?&]+)/` ——
              //     ① **斜杠紧贴 returnTo**，而实际 URL 是 `?returnTo=` ⇒ 永远 null；
              //     ② 即便改成 `?`，`[^?&]+` 也会在 `?instance_id` 处截断，
              //        **丢掉决定「连哪个实例」的查询参数**。
              //   症状长得和「前提不存在 / 拿不到 returnTo」一模一样 ⇒ 被读成 ⬜。
              //   探针 `/tmp/opdev/returnto-probe.mjs` 实测：旧写法 null，
              //   下列写法拿到完整目标并保住 instance_id。
              //   取 `[^&]+` 而不是 `[^?&]+`：returnTo **内部**的 `&` 是编码过的
              //   `%26`，末尾那个裸 `&unlock=1` 才是 login 页自己的参数 ——
              //   也就是说 `&` 在这里恰好是**唯一正确的分隔符**。
              const want = (() => {
                const m = /returnTo=([^&]+)/.exec(landed)
                if (!m) return ''
                try { return decodeURIComponent(m[1]) } catch { return m[1] }
              })()
              if (gated && want) {
                trail.push(`解锁门 → 目标 ${want}`)
                const u = await unlockIfNeeded(cdp, '#' + want)
                trail.push(`解锁：${u.unlocked ? '成功' : '失败'}（${u.why}）`)
                if (u.unlocked) {
                  const rail2 = await cdp.ev('!!document.querySelector(".summary-rail")', EVM)
                  trail.push(`解锁后 .summary-rail=${rail2}`)
                  if (!rail2) {
                    reason = '解锁成功、进了会话详情页，但 .summary-rail 不存在'
                      + ' ⇒ SessionSummaryRail 未挂载（它需该会话 store.messages.length > 0）'
                  } else {
                    await cdp.ev('document.querySelector(".summary-rail").click()', EVM)
                    await sleep(1500)
                    const m2 = await cdp.ev(MEASURE, EVM)
                    ovConv = m2.overlays || []
                    trail.push(`点开后浮层=${ovConv.length}`)
                    if (!ovConv.length) reason = '点开收起条后 .summary-panel 仍未出现在 DOM 里'
                  }
                } else {
                  reason = `被解锁门拦下且**真实解锁流程失败**（${u.why}）`
                }
              } else if (gated) {
                reason = `被解锁门拦下（落点 ${landed}）但解析不出 returnTo 目标 ⇒ 无法定向解锁`
              } else {
                reason = `点会话行没进入 #/sessions/:id，实际落点 ${landed}（未识别为解锁门）`
              }
            } else {
              const rail = await cdp.ev('!!document.querySelector(".summary-rail")', EVM)
              trail.push(`收起条 .summary-rail=${rail}`)
              if (!rail) {
                reason = '会话详情页没有 .summary-rail ⇒ SessionSummaryRail 未挂载'
                  + '（它 v-if 在 SessionConversationView 内，需该会话 store.messages.length > 0）'
              } else {
                await cdp.ev('document.querySelector(".summary-rail").click()', EVM)
                await sleep(1500)
                const m = await cdp.ev(MEASURE, EVM)
                ovConv = m.overlays || []
                trail.push(`点开后浮层=${ovConv.length}`)
                if (!ovConv.length) reason = '点了收起条但 .summary-panel / .alert-toast 仍未出现在 DOM 里'
              }
            }
          }
        } catch (err) {
          reason = `覆盖尝试抛错：${err && err.message ? err.message : String(err)}`
        }

        if (ovConv && ovConv.length) {
          const bad = ovConv.filter((o) => (o.overlapBar ?? 0) > 1)
          record('UI-06e', '顶栏下方的固定浮层不与顶栏重叠', bad.length === 0,
            `经会话详情页渲染：${trail.join(' → ')}。` +
            ovConv.map((o) => `${o.cls}: top=${o.top} css.top=${o.cssTop} pos=${o.pos} z=${o.z} 与顶栏重叠 ${o.overlapBar}px`).join('；') +
            (bad.length ? ' ⚠️ **存在重叠**' : ' ⇒ 不重叠'))
        } else {
          // 链断在哪一环，就报哪一环；不再断言「永远不可能出现」
          skip('UI-06e', '顶栏下方的固定浮层不与顶栏重叠',
            `覆盖链：${trail.join(' → ')}。断点：**${reason}**。` +
            `⚠️ 订正 2026-10-05：原理由写「.summary-panel 只在录音激活时挂载、这些路由上不可能出现」——` +
            `**只对一半**：它有两个实现，` +
            `② SessionSummaryRail 挂在 SessionConversationView（#/sessions/:id）上，不需要录音激活。` +
            `.alert-toast 那半句仍成立（MeetingAlertToast.vue 剥注释后外部引用 0）。` +
            `⚠️ 另一处订正：原理由里的静态算术「top=calc(48px+8px)=56px ⇒ 必重叠 16px」是**修复前**的旧式子；` +
            `现码是 calc(var(--topbar-height) + var(--app-safe-top) + var(--space-2))` +
            `= ${topbarH} + ${rows[0].m.safeTop} + 8 = ${topbarH + rows[0].m.safeTop + 8}px，` +
            `而顶栏底边 = ${rows[0].m.safeTop} + ${topbarH} = ${rows[0].m.safeTop + topbarH}px ` +
            `⇒ 当前代码**恰好留出 8px 余量、不重叠**。静态门禁 topbar-chrome 契约 C 守这条。`)
        }
      }

      if (dRow) {
        const base = rows[0].m
        // ⚠️ 主证改用**顶栏标题**（`.top-bar h1.title`）：它在 UI-06b/c/d 里
        // 已被证明每个场景都存在，所以「读不到」一定是量具坏了而不是页面变了。
        // 正文探针仍取，但只作补充 —— 页面无数据时它是 null，
        // 那时要报的是「该页无正文可比」，不是「字号没生效」。
        const baseTitle = base.barFontPx
        const dTitle = dRow.m.barFontPx
        const grewTitle = baseTitle && dTitle && dTitle > baseTitle
        const grewBody = base.textFontPx && dRow.m.textFontPx && dRow.m.textFontPx > base.textFontPx
        const ratio = grewTitle ? (dTitle / baseTitle) : null
        record('UI-06f', '系统字号 1.3 真的作用到了 WebView（前提自证：顶栏标题实测变大）', !!grewTitle,
          `顶栏标题 ${baseTitle}px → ${dTitle}px` + (ratio ? `（×${ratio.toFixed(3)}）` : '') +
          `；正文 ${base.textFontPx ?? '（基线页无正文可比）'}px → ${dRow.m.textFontPx ?? '（本页 main 无正文）'}px` +
          `，main 文本长度 ${base.mainTextLen} → ${dRow.m.mainTextLen}` +
          (grewTitle
            ? (grewBody
              ? '（主证与正文探针一致）'
              : ` ⚠️ 主证成立但正文探针未变大（${base.textFontPx} → ${dRow.m.textFontPx}）—— 说明「该页这批元素」没跟着放大，不推翻主证，但需另查是谁定的字号`)
            : ' ⚠️ 标题都没变大 ⇒ 这次场景没测到「字号放大」，下面的结论不成立'))
        const dWhy = barWhy(dRow)
        record('UI-06g', '字体放大后吸顶顶栏仍贴在安全区顶端、铺满视口、且高度未被压扁',
          grewTitle && dWhy.length === 0 && dRow.m.barH === topbarH && dRow.m.overflow <= 1,
          `top=${dRow.m.barTop}/安全区${dRow.m.safeTop} w=${dRow.m.barW}/${dRow.m.w} h=${dRow.m.barH}/${topbarH} pos=${dRow.m.barPos} 溢出=${dRow.m.overflow}px` +
          (dWhy.length ? `；${dWhy.join('/')}` : '') +
          (!grewTitle ? '；⚠️ 字号放大未生效，本条结论不成立' : ''))
      } else {
        record('UI-06f', '系统字号 1.3 场景', false, `${dFail || '未知原因'} —— 本场景**未取得结论**（不是通过）`)
        record('UI-06g', '系统字号 1.3 场景', false, `${dFail || '未知原因'} —— 本场景**未取得结论**（不是通过）`)
      }
      record('UI-06h', '系统设置 font_scale 已还原（它是整机状态，污染后续所有测量）', fontScaleRestored,
        `原值 ${orig}，跑完实测 ${sh(['settings', 'get', 'system', 'font_scale'])}`)
      console.log('')
    }

    // ---- UI-07：下拉刷新（未达阈值不请求 / 达阈值恰好一次 / 失败保留旧行）----
    // 2026-10-06 新增。落点选 `#/sessions`：它是仓内用 `PullToRefresh` 的
    // 两个可达路由之一（另一个是 `#/tasks`），且 `handleRefresh → loadSessions()
    // → api.getAllSessions` 真的走网络 ⇒ 「有没有发请求」有真实可观测量。
    //
    // ⚠️ 契约必须照源码写，不能照直觉写（pull-gesture.ts:70/151）：
    //   触发 = shouldTrigger(offset, threshold)                  // offset >= 60
    //        || shouldTriggerByFling(offset, threshold, velocity) // offset >= 24 && v >= 0.45
    //   ⇒ **快速甩动在 24~60px 之间也会触发**。负例必须**同时**压住两条
    //     （位移小 + 速度低），否则会把「设计如此」报成缺陷。
    if (!only || only === 'ui-07') {
      console.log('--- UI-07 下拉刷新三条件 ---')
      const PTR = { threshold: 60, flingMinOffset: 24, flingVelocity: 0.45, maxPullRatio: 1.6 }

      // 页内 fetch 计数器。**必须在 finally 里还原**——它是页面全局状态，
      // 留着会让后续所有场景的「有没有发请求」结论全部失真（同 font_scale 的纪律）。
      const INSTALL = `(() => {
        if (window.__matrixFetch) return { already: true };
        const orig = window.fetch;
        const rec = { n: 0, urls: [], failNext: 0 };
        window.__matrixFetch = rec;
        window.fetch = function (input) {
          const u = typeof input === 'string' ? input : (input && input.url) || String(input);
          rec.n += 1; rec.urls.push(u);
          if (rec.failNext > 0) { rec.failNext -= 1; return Promise.reject(new TypeError('matrix-injected-failure')); }
          return orig.apply(this, arguments);
        };
        return { already: false };
      })()`
      const RESTORE = `(() => {
        if (!window.__matrixOrigFetch) return { restored: false, why: '计数器不存在' };
        window.fetch = window.__matrixOrigFetch;
        const n = window.__matrixFetch ? window.__matrixFetch.n : -1;
        delete window.__matrixFetch; delete window.__matrixOrigFetch;
        return { restored: true, counted: n };
      })()`
      // 安装时把原函数存到另一个键上：RESTORE 不能依赖 window.__matrixFetch
      // 还活着（万一计数器被别的代码清掉，就再也还原不回去了）。
      const INSTALL2 = INSTALL.replace(
        'window.__matrixFetch = rec;',
        'window.__matrixOrigFetch = orig; window.__matrixFetch = rec;')

      const SNAP = `(() => {
        const c = document.querySelector('.pull-to-refresh');
        const sc = c ? (c.querySelector('.refresh-content') || c) : null;
        const txt = document.querySelector('.refresh-text');
        const ind = document.querySelector('.refresh-indicator');
        return {
          hasPtr: !!c,
          indTransform: ind ? getComputedStyle(ind).transform : null,
          contentTransform: (() => { const m = c ? c.querySelector('.refresh-content') : null;
            return m ? getComputedStyle(m).transform : null; })(),
          // ⚠️ 触摸起点必须**从元素实际位置算**，不能取「视口高 × 某个比例」。
          //   第一版用 env.h*0.35 = 320px，而 sessions 容器实测
          //   rect = {top:195, height:108} ⇒ 320 落在**容器下方**，
          //   touchstart 根本没打到组件上。
          //   症状与「绑定没接上」完全一样（transform 不动）——
          //   又一次「设备/量具问题长得像产品缺陷」。
          //   ⚠️ 本段在注入用的模板字符串里，**不许出现反引号**（写了会把模板提前闭合，
          //   报出来的是 node 侧的 SyntaxError，指向注释里的字符，看着像文件坏了）。
          contentRect: (() => { const m = c ? c.querySelector('.refresh-content') : null;
            if (!m) return null; const r = m.getBoundingClientRect();
            return { top: Math.round(r.top), h: Math.round(r.height), w: Math.round(r.width) }; })(),
          scrollTop: sc ? Math.round(sc.scrollTop) : null,
          hint: txt ? String(txt.textContent || '').trim() : null,
          ready: !!(ind && ind.classList.contains('refresh-indicator--ready')),
          spinning: !!(document.querySelector('.refresh-icon--spinning')),
          rows: document.querySelectorAll('.session-card').length,
          requests: window.__matrixFetch ? window.__matrixFetch.n : null,
        };
      })()`

      let ptrRestored = true
      let ptrNote = ''
      try {
        await goto(cdp, '#/sessions')
        await sleep(1500)
        await cdp.ev(INSTALL2, EVM)

        // ---- 前提自证（不成立就别往下量）----
        const s0 = await cdp.ev(SNAP, EVM)
        const env = await cdp.ev(`({ w: innerWidth, h: innerHeight })`, EVM)
        // ⚠️ 起点取**容器实测位置**，不是视口的比例（理由见 SNAP 里的 contentRect）。
        //   并把它打出来：取点算错时，症状与「绑定没接上」一模一样。
        const rect = s0.contentRect || { top: 0, h: env.h }
        const cx = Math.round(env.w / 2)
        const cy0 = Math.max(rect.top + 6, Math.round(rect.top + rect.h / 2))
        console.log(`  · 触摸起点 (${cx}, ${cy0})，容器 rect={top:${rect.top}, h:${rect.h}}，` +
          `scrollTop=${s0.scrollTop}（handleTouchStart 要求 scrollTop===0）`)

        // ① 计数器本身能用吗？**注入一个必定失败的探针请求**，
        //    看计数有没有涨 —— 不然「未请求」是恒真的（计数器坏了也读成 0）。
        await cdp.ev(`(() => { const r = window.__matrixFetch; r.n = 0; r.failNext = 1;
          return fetch('matrix://probe/never-resolved').catch(() => 1); })()`, EVM)
        const probeN = await cdp.ev('(window.__matrixFetch ? window.__matrixFetch.n : null)', EVM)
        const counterWorks = probeN === 1
        record('UI-07a', '【量具自证】页内 fetch 计数器确实能计到（否则「没请求」是恒真的）',
          counterWorks && s0.hasPtr,
          `PullToRefresh 容器 ${s0.hasPtr ? '存在' : '**不存在**'}；注入探针请求后计数 = ${probeN}（应为 1）。` +
          (counterWorks ? '' : ' ⚠️ 计数器失效 ⇒ 本组「未请求」类结论全部无效'))

        if (!s0.hasPtr) {
          skip('UI-07b', '未达阈值不请求', '页面上没有 .pull-to-refresh —— 落点不可达')
          skip('UI-07c', '达阈值恰好请求一次', '页面上没有 .pull-to-refresh —— 落点不可达')
          skip('UI-07d', '刷新失败保留旧行', '页面上没有 .pull-to-refresh —— 落点不可达')
        } else {
          /** 用一串 touch 点走一次下拉手势。steps 的时间间隔决定速度（px/ms）。 */
          const pull = async (rawPx, stepMs) => {
            const steps = 6
            const rest = await cdp.ev(SNAP, EVM)
            await cdp.send('Input.dispatchTouchEvent', {
              type: 'touchStart',
              touchPoints: [{ x: cx, y: cy0, id: 1, radiusX: 4, radiusY: 4, force: 1 }],
            }, EVM)
            let held = null
            for (let i = 1; i <= steps; i += 1) {
              const y = cy0 + Math.round((rawPx * i) / steps)
              await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: cx, y, id: 1, radiusX: 4, radiusY: 4, force: 1 }] }, EVM)
              await sleep(stepMs)
              if (i === steps) held = await cdp.ev(SNAP, EVM)   // 还没松手，先取一次
            }
            await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] }, EVM)
            return { rest, held }
          }

          // ⚠️ raw → pullDistance 是**橡皮筋压缩**后的，不是 1:1，
          //   而且 `PullToRefresh.vue` 里的 ref 是私有的、量不到，只能实测标定。
          //   本轮用一次性探针在设备上标定（不是照源码推的）：
          //       raw  40 → offset  25.8   hint=下拉同步邮件
          //       raw  80 → offset  44.6   hint=下拉同步邮件
          //       raw 120 → offset  58.4   hint=下拉同步邮件   ← 差 1.6px 没到 60！
          //       raw 200 → offset  75.9   hint=松开立即同步   ← 取这个
          //       raw 450 → offset  93.1   hint=松开立即同步
          //   ⇒ 第一版取 raw 95（offset≈50），**永远到不了阈值**，
          //     判据 UI-07d 于是报红，读起来像「达阈值不触发」的产品缺陷 ——
          //     实际是我没标定、期望值是拍的。
          const RAW_FIRE = 200
          const RAW_HOLD = 35

          // ---- 前提自证：手势**真的送达组件**了吗？----
          // ⚠️ 2026-10-06 实吃：这一条是补上的，而且补得很晚。
          // 首版 UI-07b（未达阈值不请求）判了 🟢，但那是个**空过**：
          // 静止态与「手势没生效」时 `.refresh-text` 的文案**完全一样**
          // （都是「下拉同步邮件」），所以「没发请求」在两种情况下都成立。
          // 真因是 `PullToRefresh.vue` 的三个 handleTouch* **根本没绑到模板上** ——
          // 整套下拉刷新手势从未生效过。
          // ⇒ 判据必须先证明「指示器/内容层真的被推动了」，
          //    否则下面两条都是恒真的（量具自己的 fail-open，与产品缺陷同形）。
          const p0 = await pull(RAW_FIRE, 40)
          await sleep(3000)
          const gestureLanded = p0.held.contentTransform !== p0.rest.contentTransform
          record('UI-07b', '【量具自证】下拉手势真的送达了组件（否则下面两条都是恒真的）', gestureLanded,
            `按住时 .refresh-content 的 transform：静止 ${p0.rest.contentTransform} → 拉后 ${p0.held.contentTransform}` +
            (gestureLanded ? ' ✅ 元素被推动了' : ' ❌ **纹丝不动** ⇒ 手势没送达，下面「未请求」类是空过'))

          // ---- 条件 1：未达阈值 ⇒ 不请求 ----
          // ⚠️ 必须**同时**压住契约里的两条触发路径（pull-gesture.ts:70/151）：
          //   ① offset < 60；② 「offset ≥ 24 且 速度 ≥ 0.45」的甩动。
          // raw 35px 只能压住 ①（35 > 24），所以 ② 靠**速度**压：
          // 6 步 × 90ms = 630ms 走 RAW_HOLD(35)px ⇒ 平均约 0.056 px/ms，远低于 0.45。
          //   实测 raw 35 → offset≈22.7 < 24(=threshold×0.4) ⇒ 甩动那条也天然不成立。
          // ⚠️ 手势没送达时这条也是恒真的 —— 上面 UI-07b 已先证伪/证实送达。
          await cdp.ev('(window.__matrixFetch.n = 0, 1)', EVM)
          const g1 = await pull(RAW_HOLD, 90)
          const held1 = g1.held
          await sleep(2500)
          const after1 = await cdp.ev(SNAP, EVM)
          record('UI-07c', '未达阈值松手：不发请求、指示器回到未就绪',
            counterWorks && gestureLanded && held1.hint === '下拉同步邮件' && !held1.ready
              && after1.requests === 0 && !after1.spinning,
            `按住时 hint="${held1.hint}" ready=${held1.ready}（期望「下拉同步邮件」/false ⇒ offset < ${PTR.threshold}）；` +
            `松手后 ${held1.requests} → ${after1.requests} 次请求（期望 0），spinning=${after1.spinning}` +
            (after1.requests > 0 ? ' ⚠️ 未达阈值却发了请求' : ''))

          // ---- 条件 2：达阈值 ⇒ 恰好一次 ----
          // 位移取 RAW_FIRE（设备实测标定值，见上面那张表），不拍脑袋。
          await cdp.ev('(window.__matrixFetch.n = 0, 1)', EVM)
          const held2 = (await pull(RAW_FIRE, 40)).held
          const during = await cdp.ev(SNAP, EVM)
          await sleep(4000)
          const after2 = await cdp.ev(SNAP, EVM)
          record('UI-07d', '达阈值松手：恰好 1 次请求、指示器经历 refreshing 后复位',
            counterWorks && gestureLanded && held2.hint === '松开立即同步' && held2.ready
              && after2.requests === 1 && !after2.spinning && after2.hint === '下拉同步邮件',
            `按住时 hint="${held2.hint}" ready=${held2.ready}（期望「松开立即同步」/true ⇒ offset ≥ ${PTR.threshold}）；` +
            `刷新中 hint="${during.hint}" spinning=${during.spinning}；` +
            `松手后共 ${after2.requests} 次请求（期望恰好 1），复位后 hint="${after2.hint}" spinning=${after2.spinning}` +
            (after2.requests !== 1 ? ` ⚠️ 实际 ${after2.requests} 次` : ''))

          // ---- 条件 3：失败保留旧行 ----
          // 让下一次请求必失败（计数器 failNext=1），再拉一次。
          // 判据看**行数**：PullToRefresh 失败只 console.error（PullToRefresh.vue:278），
          // 组件本身不清列表；真正的风险是 store 层在失败时把 sessions 清空。
          const rowsBefore = after2.rows
          if (rowsBefore === 0) {
            skip('UI-07e', '刷新失败保留旧行', `#/sessions 当前 0 行 ⇒ 没有「旧行」可保留，本条未取得结论（不是通过）`)
          } else {
            await cdp.ev('(window.__matrixFetch.n = 0, window.__matrixFetch.failNext = 1, 1)', EVM)
            const held3 = (await pull(RAW_FIRE, 40)).held
            await sleep(4500)
            const after3 = await cdp.ev(SNAP, EVM)
            record('UI-07e', '刷新失败后旧行仍在（不清空列表）',
              counterWorks && gestureLanded && held3.hint === '松开立即同步' && after3.requests === 1 && after3.rows >= rowsBefore,
              `失败前 ${rowsBefore} 行 → 失败后 ${after3.rows} 行；` +
              `（期间确实发起了 ${after3.requests} 次请求且第 1 次被注入为失败）；指示器已复位 hint="${after3.hint}" spinning=${after3.spinning}` +
              (after3.rows < rowsBefore ? ' ⚠️ **旧行被清掉了**' : ''))
          }
        }
      } catch (e) {
        ptrNote = '场景抛错：' + String(e.message).slice(0, 80)
        for (const id of ['UI-07b', 'UI-07c', 'UI-07d', 'UI-07e']) skip(id, '下拉刷新', ptrNote)
      } finally {
        // ⚠️ 必须还原：计数器是**页面全局状态**，留着会污染后续所有场景。
        const r = await cdp.ev(RESTORE, EVM).catch(() => ({ restored: false, why: '通道不通' }))
        ptrRestored = !!r?.restored
        const still = await cdp.ev('(typeof window.__matrixFetch)', EVM).catch(() => '?')
        console.log(`  · fetch 计数器${ptrRestored ? '已还原' : ' ⚠️ 还原失败'}（window.__matrixFetch 现在是 ${still}）`)
        if (ptrNote) console.log(`  · ${ptrNote}`)
      }
      record('UI-07f', '页内 fetch 计数器已还原（它是页面全局状态，污染后续所有场景）', ptrRestored,
        ptrNote ? ptrNote : '已把 window.fetch 换回原函数并删除计数器')
      console.log('')
    }

    // ---- UI-03：页面标题与顶栏一致（2026-10-06 新增）----
    // 背景（来自 AppLayout.vue:184-192 的注释）：改造前顶栏读 `route.meta.title`、
    // 页面读自己的 DOM，是两条独立路径，**异步实体名到达时两边会短暂不一致，
    // 旧页面的晚到响应还能覆盖新页面标题**。现在两边共用 `titles.resolve()`。
    // ⇒ 本条的真正回归靶子不是「两边相等」（那已被重构消灭），
    //   而是**往返导航后标题是否陈旧**：A → B → A，标题必须依次是 A、B、A。
    if (!only || only === 'ui-03') {
      console.log('--- UI-03 标题一致性 / 往返不陈旧 ---')
      const READ_TITLE = `(() => {
        const h1 = document.querySelector('.top-bar h1.title');
        const main = document.querySelector('main');
        return {
          bar: h1 ? String(h1.textContent || '').trim() : null,
          mainLabel: main ? String(main.getAttribute('aria-label') || '').trim() : null,
          docTitle: String(document.title || '').trim(),
        };
      })()`

      // 起点：会议详情（标题来自具体实体，不是路由 meta 的静态串）
      // ⚠️ 2026-10-06 实吃：这里**第一版又量早了**。
      //   `OPEN_FIRST_MEETING` 自带 2.5s，我又加了 2s，总共 4.5s 后读到的
      //   标题仍是**列表页**的「会议」，而详情自己的标题是「会议详情」。
      //   首版断言「往返两次读数必须相等」于是报红，证据写着
      //   「旧页面的晚到响应覆盖了新页面标题」——**但那不是事实**：
      //   第二次读到的是「会议详情」而不是「设置」，**根本没有陈旧覆盖**，
      //   只是第一次读数落在标题收敛之前。
      //   ⇒ 与 UI-05 的 SETTLE 同一个教训：**先等收敛，再断言收敛后的值。**
      //   而「早值 → 收敛值」的变化本身就是「异步实体名到达后同步更新」的证据，
      //   所以两次都读、都要留证。
      const readDetail = async () => {
        await goto(cdp, '#/meetings')
        await cdp.ev(OPEN_FIRST_MEETING, EVM)
        const early = await cdp.ev(READ_TITLE, EVM)
        // 等这一屏稳定（与 UI-05 用同一个 SETTLE），再读一次
        const settled = await cdp.ev(SETTLE, EVM)
        const late = await cdp.ev(READ_TITLE, EVM)
        return { early, late, rounds: settled?.rounds ?? 0 }
      }
      const pass1 = await readDetail()
      const tA = pass1.late

      // UI-03a：顶栏标题存在且不是兜底值
      // ⚠️ 兜底值必须**从源码取**（AppLayout.vue 的 title computed 末尾那个字符串），
      //   写死的话产品改名之后这条会变成永远为真。
      const appLayoutSrc = readFileSync(join(SRC, 'app', 'AppLayout.vue'), 'utf8')
      const titleBlock = /const title = computed[\s\S]{0,800}/.exec(appLayoutSrc)?.[0] || ''
      const fallback = /\|\|\s*'([^']+)'/.exec(titleBlock)?.[1] || ''
      record('UI-03a', '顶栏标题存在且不是兜底值', !!tA.bar && !!fallback && tA.bar !== fallback && tA.bar.length > 0,
        `顶栏 h1.title="${tA.bar}"（收敛后，第 ${pass1.rounds} 轮稳定）；从 AppLayout.vue 的 title computed 读到的兜底值="${fallback}"；main[aria-label]="${tA.mainLabel}"`)

      // UI-03b：顶栏与 main 的 aria-label 同源（弱检查，抓渲染脱节）
      record('UI-03b', '顶栏标题与 main 的 aria-label 一致（同一解析结果的两次渲染）', tA.bar === tA.mainLabel,
        `h1.title="${tA.bar}" vs main[aria-label]="${tA.mainLabel}"`)

      // UI-03c：往返导航不陈旧 —— 真正的回归靶子。**比的是收敛后的值。**
      await goto(cdp, '#/settings')
      const tB = await cdp.ev(READ_TITLE, EVM)
      const pass2 = await readDetail()
      const tA2 = pass2.late
      console.log(`  · 首访详情：早值"${pass1.early.bar}" → 收敛值"${tA.bar}"（第 ${pass1.rounds} 轮稳定）`)
      console.log(`  · 往返：详情"${tA.bar}" → 设置"${tB.bar}" → 详情早值"${pass2.early.bar}" → 收敛值"${tA2.bar}"`)
      record('UI-03c', 'A→B→A 往返后标题不陈旧（两次访问详情的收敛标题必须相同，且中途必须真的变过）',
        tA2.bar === tA.bar && !!tA.bar && !!tB.bar && tB.bar !== tA.bar,
        `首访收敛="${tA.bar}"，二访收敛="${tA2.bar}"（期望相同）；中间设置页="${tB.bar}"；` +
        `早值分别为 "${pass1.early.bar}" / "${pass2.early.bar}"；` +
        `三次采到 ${new Set([tA.bar, tB.bar, tA2.bar]).size} 个不同值（期望 ≥2）`)

      // UI-03d：**防「冻住」**。这是 UI-03c 的牙薄弱处，必须单独补：
      //   若 `title` computed 失去全部响应式依赖（历史上就是漏了
      //   `void shellRuntime.store.version`），它会**永远停在首次求值**。
      //   此时 A / B / A 三次读到的是**同一个冻结值** ⇒ UI-03c 的
      //   `tA2.bar === tA.bar` **照样成立、照样报绿**。
      //   ⇒ 必须另加一条：这条往返里标题**必须真的变化过**。
      //   缺这条，判据在它最该抓的那个缺陷上是无牙的。
      record('UI-03d', '往返过程中标题确实随路由变化（防「标题被冻住」这条隐蔽缺陷）',
        new Set([tA.bar, tB.bar, tA2.bar]).size >= 2,
        `详情"${tA.bar}" / 设置"${tB.bar}" / 详情"${tA2.bar}" ⇒ ${new Set([tA.bar, tB.bar, tA2.bar]).size} 个不同值（期望 ≥2）。` +
        `若为 1，说明 title computed 失去了响应式依赖、永远停在首次求值`)
      console.log('')
    }

    // ---- UI-13：硬件返回键**先关覆盖层**而不是导航路由（2026-10-06 新增）----
    // 契约来源：UI规范 06 §2「覆盖层优先」+ AppLayout.vue:222 的注释
    // （「避免…关了弹窗又跳路由」）。
    //
    // ⚠️ 这条在接线前**必然红**，而且红得很隐蔽：`BackDispatcher` 的
    // 「覆盖层优先」逻辑有 12 条单测、全部正确，但 `registerOverlay()`
    // **全仓零调用**；而 `Dialog` / `BottomSheet` 都 `<Teleport to="body">`
    // ⇒ 硬件返回键会导航路由，弹窗挂在 body 上**留在屏幕里**。
    // 「测试全绿 + 行为是坏的」就是这一族的签名。
    if (!only || only === 'ui-13') {
      console.log('--- UI-13 硬件返回键先关覆盖层 ---')
      const DLG = `(() => ({
        href: location.hash,
        dialog: (() => { const d = document.querySelector('.dialog-overlay');
          return d ? { open: true, title: String((d.querySelector('.dialog-title')||{}).textContent||'').trim() } : null; })(),
        sheet: !!document.querySelector('.bottom-sheet-overlay'),
        bodyDialogs: document.querySelectorAll('body > .dialog-overlay').length,
      }))()`

      // 落点：设置页的「退出」按钮 → 走 `confirm()` → Dialog。
      // ⚠️ 只**开**弹窗然后按返回，绝不点确认键 —— 万一修复没生效、
      // 返回键把路由换掉，这条会记红，但**不会**把用户登出。
      await goto(cdp, '#/settings')
      await sleep(1200)
      const s1 = await cdp.ev(DLG, EVM)
      const clicked = await cdp.ev(`(() => {
        // ⚠️ 按 **class** 定位，不按文案 —— 第一版写的是 /退出|登出|logout/i，
        // 而这台设备上 UI 实际是英文 "Log Out"（词中间有空格，正则也不匹配），
        // 于是点不到、弹窗不开、整条判据空过。
        // ⇒ 定位一律走 class（实测本页 button.action-btn.danger 恰好 1 个），
        //    文案只拿来回显做证据，不参与匹配。
        const all = Array.from(document.querySelectorAll('button.action-btn.danger'));
        if (all.length !== 1) return { ok: false, why: 'danger 按钮数=' + all.length + '（期望恰好 1）' };
        const t = all[0];
        const text = String(t.textContent || '').trim();
        t.click();
        return { ok: true, text: text };
      })()`, EVM)
      await sleep(1500)
      const s2 = await cdp.ev(DLG, EVM)
      const opened = !!s2.dialog
      record('UI-13a', '【量具自证】设置页能开出一个真实覆盖层弹窗（否则下面全是空过）',
        opened && !!clicked?.ok,
        `点「${clicked?.text || '?'}」后：dialog=${opened ? `存在（标题「${s2.dialog.title}」）` : '**没出现**'}，` +
        `挂在 body 下的数量=${s2.bodyDialogs}。` + (opened ? '' : ' ⚠️ 没开出来 ⇒ 下面两条无意义'))

      if (!opened) {
        skip('UI-13b', '硬件返回键先关覆盖层、不导航路由', '弹窗没开出来，本条未取得结论（不是通过）')
        skip('UI-13c', '无弹窗时返回键确实能导航（13b 的对照）', '弹窗没开出来，本条未取得结论（不是通过）')
      } else {
        pressBack()
        await sleep(2500)
        const s3 = await cdp.ev(DLG, EVM)
        record('UI-13b', '弹窗开着时按硬件返回键：弹窗关闭、路由不动',
          !s3.dialog && s3.href === s2.href,
          `按返回前 href=${s2.href} dialog=有；按返回后 href=${s3.href} dialog=${s3.dialog ? '**仍在**' : '已关'}` +
          (s3.href !== s2.href ? ' ⚠️ 路由被换掉了 ⇒ 返回键没先关弹窗（Teleport 弹窗会留在屏幕上）' : '') +
          '  ※ 本条的「路由未变」要靠 UI-13c 才有意义（见那里）')
        // ⚠️ 2026-10-06 改写版。第一版只重复查了一遍 `!s3.dialog`（13b 已查），
        //   标题写着「再按一次返回应当能导航」，代码里却从没再按一次 ——
        //   于是 13b 的「按返回后 href 未变」是一句**可以恒真**的话。
        // 第二版补了「再按一次」，却**继承了矩阵跑到 UI-13 时的导航栈**：
        //   那一轮它红，理由写的是「返回键根本没接进路由」。
        //   实测**不是**：另起两轮对照（唯一变量是开没开弹窗），两轮都回到了 `#/ai`。
        //   真正的原因是前提不成立 —— 当时 `#/settings` 的 `ctx.cursor` 已是 0，
        //   返回键走的是 `fallbacks` 的 replace，落点**恰好就是它自己**。
        //   ⇒ 这条判据必须**自己把前提搭出来**，不能继承上游状态。
        //   而且期望值要**读出来**：先明确把栈搭成 ai → settings，
        //   于是「返回后应当是 #/ai」是推出来的，不是「变了没有」这种弱断言。
        const pre1 = await goto(cdp, '#/ai')
        const h1 = (await cdp.ev('location.hash', EVM))
        await goto(cdp, '#/settings')
        const h2 = (await cdp.ev('location.hash', EVM))
        const stackOk = h1 === '#/ai' && h2 === '#/settings'
        if (!stackOk) {
          skip('UI-13c', '弹窗关闭后不留下「幽灵拦截」', `两深栈没搭起来（读回 ${h1} → ${h2}，期望 #/ai → #/settings），本条未取得结论（不是通过）`)
        } else {
          pressBack()
          await sleep(2500)
          const s4 = await cdp.ev(DLG, EVM)
          record('UI-13c', '弹窗关闭后不留下「幽灵拦截」：无弹窗时按返回确实能导航',
            s4.href === '#/ai' && !s4.dialog,
            `自建两深栈 #/ai → #/settings（两处 hash 都读回确认）；按返回后 href=${s4.href}（期望 #/ai）` +
            ` dialog=${s4.dialog ? '**又开了一个**' : '无'}` +
            (s4.href !== '#/ai'
              ? ' ⚠️ 没回到 #/ai ⇒ 要么返回键不活跃（13b 的「未变」不可信），要么弹窗关闭后有幽灵拦截把这一次吞了'
              : ' ⇒ 返回键活跃且无幽灵拦截 ⇒ UI-13b 的「路由未变」是真量出来的，不是默认成立'))
          void pre1
        }
      }
      console.log('')
    }

    // ---- UI-12：底栏遮挡（这条直接验我这两轮的底部让位修复）----
    if (!only || only === 'ui-12') {
      console.log('--- UI-12 底栏遮挡 ---')
      // ⚠️ 2026-10-06 实吃，**这条之前一直是坏的，而且坏法很像缺数据**：
      //   它导航到 `#/meetings`（**列表页**），量的却是详情页才有的选择器，
      //   于是永远报「本页没有任何被测选择器」。
      //   原注释写「会议列表页没有详情页的按钮属正常；需先进详情页再测」——
      //   它把**自己没进详情页**这件事，写成了「环境本来如此」。
      //   ⇒ 先造数、再进详情，最后才量。
      const seed = await seedMeeting(cdp)
      console.log(`  · 造数：${seed.why}`)
      if (!seed.ok) {
        record('UI-12', '底栏遮挡（需详情页数据）', false,
          `造数未成功 ⇒ 详情页量不了：${seed.why}。这不是「无数据所以无结论」，是本条判据没跑起来。`)
        console.log('')
      } else {
        const opened = await cdp.ev(OPEN_FIRST_MEETING, EVM)
        if (!opened.ok) {
          record('UI-12', '底栏遮挡（需详情页）', false, `点不进详情页：${opened.why}（当前 hash=${opened.hash}）`)
        } else {
          console.log(`  · 已进详情页 ${opened.hash}`)
          // 量之前**再自证一次被测对象在位**：详情页没渲染出来时，
          // 下面每一条都会报「未出现」，而那正是「无结论」的同一种长相。
          const detailReady = await cdp.ev(`(() => {
            const de = document.documentElement;
            return { len: String(document.body.innerText||'').trim().length,
                     overflow: Math.max(de.scrollWidth, document.body.scrollWidth) - de.clientWidth };
          })()`, EVM)
          console.log(`  · 详情页自证：文本 ${detailReady.len} 字，横向溢出 ${detailReady.overflow}px`)

          let present = 0, absent = 0
          let lastNavTop = undefined, lastRect = null
          for (const t of OCCLUSION_TARGETS) {
            const r = await cdp.ev(OCCLUSION_CHECK(t.sel), EVM)
            if (!r.found) {
              if (t.expect === 'absent') {
                absent++
                record('UI-12b', `${t.sel} 预期不出现（${t.on}）`, true, '未出现，与预期一致')
              } else {
                record('UI-12a', `${t.sel} 应在详情页且不被底栏压住`, false,
                  `**出处核对通过但运行时找不到** ⇒ 详情页没渲染出这个元素，或 v-if 条件被误判为真。${t.why}`)
              }
              continue
            }
            if (t.expect === 'absent') {
              record('UI-12b', `${t.sel} 预期不出现（${t.on}）`, false,
                `出现了！条件是 \`${t.on}\`，设备上不该为真 ⇒ 该条件被误判`)
              continue
            }
            present++
            lastNavTop = r.navTop
            lastRect = r.rect
            const origin = findClassOrigin(t.cls)
            const where = origin.length ? `出处 ${origin[0].file}:${origin[0].line}` : '出处未知'
            console.log(`    ${t.sel.padEnd(16)} rect=${JSON.stringify(r.rect)} navTop=${r.navTop} belowNav=${r.belowNav} 中心命中=${r.hitAtCenter}  ${where}`)
            record('UI-12a', `${t.sel} 完整可点、不被底栏压住`, !(r.belowNav || r.hitIsSelfOrChild === false),
              r.belowNav || r.hitIsSelfOrChild === false
                ? `bottom=${r.rect.bottom} > navTop=${r.navTop}；中心点命中 ${r.hitAtCenter}（不是它自己）。${where}`
                : `bottom=${r.rect.bottom} ≤ navTop=${r.navTop}；中心点命中 ${r.hitAtCenter}。${where}；${t.on}`)
          }
          // ★ 收口：必须至少有一个**该出现**的元素真的被量到。
          //   「全部预期不出现」和「全部没量到」在记录条数上长得一样，
          //   但前者是有效结论、后者是量具空转。
          record('UI-12c', '至少量到 1 个常驻的底部元素（否则本轮 UI-12 等于空转）', present >= 1,
            `常驻元素量到 ${present} 个、预期缺席确认 ${absent} 个；详情页文本 ${detailReady.len} 字`)

          // ★★ 收口之收口（2026-10-06 加，**这条是本轮最重要的一次修正**）：
          //   上面 UI-12a 的 🟢 是**空的**。实测 navTop=**null** —— 因为
          //   `/meetings/:id` 的 meta 是 `bottomNav: false`（router-mobile.ts:323），
          //   这一页**根本没有底栏**。于是 OCCLUSION_CHECK 里
          //   `belowNav: navTop === null ? null : …` 返回 null（falsy），
          //   落进「不压住」那一支 —— 而它不是因为量到了让位正确才绿的，
          //   是因为**没有可比的对象**。
          //   ⇒ 「不被底栏压住」在无底栏页上是**恒真命题**。
          //   必须把它单独拎出来判红/判绿，否则这条判据会在最需要它的时候
          //   给出一个看起来很正常的绿。
          const navSeen = lastNavTop
          const gapToBottom = lastRect ? (boot.h - lastRect.bottom) : null
          record('UI-12d', '底部让位被真实验证过（本页有底栏可比），而非因无底栏恒真', navSeen !== null,
            navSeen === null
              ? `本页**没有底栏**（navTop=null）⇒「不被底栏压住」无从谈起，上面 UI-12a 的绿是恒真的。` +
                `实测 .mic 底边距视口底 ${gapToBottom}px（视口高 ${boot.h}）—— 这个让位量是为一条不存在的底栏预留的`
              : `navTop=${navSeen}，底边距视口底 ${gapToBottom}px，让位量有真实比对对象`)
        }
      }
      console.log('')
    }

    // ---- UI-01/02：Android 硬件返回 ----
    if (!only || only === 'ui-01') {
      console.log('--- UI-01/02 Android 硬件返回 ---')
      // ⚠️ 2026-10-04 实吃：`querySelector('.list-item, [class*="item"]')`
      // 命中的是**骨架屏** `skeleton-item`（它也匹配 `[class*="item"]`），
      // row.click() 打在占位符上路由不变，后面全报「无结论」。
      // ⇒ 2026-10-06 起两件事一起解决：用 `.meeting-card` 精确选择器，
      //   并且**先造数**（seedMeeting 内含等骨架屏消失）。
      const seed = await seedMeeting(cdp)
      console.log(`  · 造数：${seed.why}`)
      if (!seed.ok) {
        record('UI-01', '详情页按硬件返回回到列表', false, `造数未成功，无法进入详情页：${seed.why}`)
        record('UI-02', '连按返回逐级回退（每次只退一层）', false, `造数未成功，无法构造两层导航：${seed.why}`)
        console.log('')
      } else {
        const beforeNav = await cdp.ev('location.hash', EVM)
        const opened = await cdp.ev(OPEN_FIRST_MEETING, EVM)
        console.log(`  · 点列表行：${JSON.stringify(opened)} ${beforeNav} → ${opened.hash || '(未变)'}`)
        if (!opened.ok) {
          record('UI-01', '详情页按硬件返回回到列表', false, `点不进详情页：${opened.why}（当前 hash=${opened.hash}）`)
          record('UI-02', '连按返回逐级回退（每次只退一层）', false, `点不进详情页，构造不出两层导航：${opened.why}`)
        } else {
          // ★★ 按键**送达自证**（2026-10-06 加，判别动作实测得出）：
          //   首轮实测 UI-01/02 🔴（按 BACK 后 hash 纹丝不动）。判别办法是
          //   在页面里直接调 `history.back()`：
          //     · 页面内 history.back()：#/meetings/<id> → #/meetings ✅ 路由栈是好的
          //     · adb input keyevent 4：完全无反应，App 也没被关掉，logcat 零命中
          //   ⇒ **按键根本没送到 WebView**。AppLayout.vue:208 的
          //   `CapApp.addListener('backButton')` 从未触发。
          //   ⇒ 那是**环境/量具**问题，不是产品缺陷。
          //   但不判别就报红，等于把自己的失败说成产品缺陷 —— 所以这里
          //   把判别做进判据：先证明按键送到了，红才有资格归给产品。
          const control = await cdp.ev(`(async () => {
            const s = ms => new Promise(r => setTimeout(r, ms));
            const h0 = location.hash;
            history.back(); await s(2500);
            const mid = location.hash;          // ★ 必须单独留档：下面要 forward 复位，
            const moved = h0 !== mid;            //   直接返回 location.hash 会把「退到了哪」抹掉，
            if (moved) { history.forward(); await s(2000); }
            return { h0, moved, mid, h1: location.hash };
          })()`, EVM)
          if (!control.moved) {
            record('UI-01', '详情页按硬件返回回到列表', false,
              `**按键未判别**：页面内 history.back() 也退不动（${control.h0} → ${control.mid}）⇒ 路由栈本身有问题，本条判据不成立`)
            record('UI-02', '连按返回逐级回退（每次只退一层）', false, '同上：路由栈不可退，判据不成立')
          } else {
            pressBack()
            await sleep(2500)
          const afterBack1 = await cdp.ev('location.hash', EVM)
          const pidAlive = sh(['pidof', PKG])
          const delivered = afterBack1 !== opened.hash
          if (!delivered && pidAlive) {
            // 路由栈能退（刚自证过），按键却不生效 ⇒ 按键没送达，不是产品行为
            record('UI-01', '详情页按硬件返回回到列表', false,
              `**按键未送达，非产品缺陷**：页面内 history.back() 能退（${control.h0} → ${control.mid} ⇒ 路由栈正常），` +
              `但 adb keyevent 4 后 hash 仍是 ${afterBack1}、App 也没被关掉（pid=${pidAlive}）` +
              `⇒ backButton 监听器从未触发，是环境没把硬件返回键送进 WebView。` +
              `要判这条只能真机手动按，或先解决模拟器的按键注入。`)
            record('UI-02', '连按返回逐级回退（每次只退一层）', false, '同上：按键未送达，本条不成立')
            console.log('')
          } else {
            record('UI-01', '详情页按硬件返回回到列表', afterBack1 === beforeNav,
              `${opened.hash} --BACK--> ${afterBack1}（期望 ${beforeNav}）｜按键已证实送达（hash 变化 / App 退出）`)
            // UI-02：连按两次，返回应逐级回退，**不得一次穿多层**
            //
            // ⚠️ 2026-10-06 实测踩到的**测试污染**：
            //   上面那个 `control` 为了自证路由栈，会在页面里做一次
            //   `history.back()` 再 `history.forward()` 复位。这两步**在同一条
            //   history 栈上留下了痕迹**，所以紧接着测 UI-02 时栈已经不干净。
            //   首轮实测拿到 `详情 → 列表 → 详情`（第二次 BACK 反而"前进"回了详情），
            //   看起来像个真实缺陷，**但很可能只是我自己的探针造成的**。
            //   ⇒ UI-02 必须**从已知干净的栈**重新起步：先 `location.replace`
            //     回根路由（replace 不产生新的 history 条目），再逐级走。
            const steps = []
            await cdp.ev(`location.replace('#/ai')`, EVM)
            await sleep(1500)
            steps.push(await cdp.ev('location.hash', EVM))
            await goto(cdp, '#/meetings')
            steps.push(await cdp.ev('location.hash', EVM))
            await cdp.ev(OPEN_FIRST_MEETING, EVM)
            steps.push(await cdp.ev('location.hash', EVM))
            pressBack(); await sleep(2000); steps.push(await cdp.ev('location.hash', EVM))
            pressBack(); await sleep(2000); steps.push(await cdp.ev('location.hash', EVM))
            console.log(`  · 从干净栈构造两层后连按两次返回：${steps.join('  →  ')}`)
            // 判据：两次 BACK 后必须**逐级**回退，且**不得回到刚才离开的那个详情页**
            //（那叫"反向穿透"，是比"穿多层"更糟的形态）。
            const detailH = steps[2]
            const b1 = steps[3], b2 = steps[4]
            const monotonic = b1 !== detailH && b2 !== b1
            const notReentered = b1 !== detailH && b2 !== detailH
            record('UI-02', '连按返回逐级回退（每次只退一层，且不反向穿透回详情）',
              monotonic && notReentered,
              `五次采样 ${steps.join(' → ')}；逐级=${monotonic} 未反向穿透=${notReentered}（b1=${b1} 不得等于详情 ${detailH}；b2=${b2}）`)

            // ---- UI-02b：返回**在飞行中**叠按（单飞契约）----
            //
            // ⚠️ 上面那条 UI-02 是「等 2000ms 再按」，**测不到单飞**：
            //   前一次早落地了。`BackDispatcher.back()` 的 `if (this.inFlight) return
            //   this.inFlight`（backDispatcher.ts:106）只在前一次**还在飞行**时才生效。
            //
            // ⚠️ 为什么用**顶栏返回钮**而不是硬件键：`pressBack()` 每次一个
            //   `execFileSync`，串行 200–500ms，**无法保证叠按落在飞行窗口内**
            //   ⇒ 用它测单飞，测的是「按键有没有丢」，不是契约。
            //   返回钮与硬件键走**同一个 `dispatchBack`**，单飞在那一层，
            //   所以页面内同一 tick 连点 N 次能**确定性地**制造重叠。
            //
            // ★ 失败形态很戏剧化（这也是本条判据的价值）：
            //   单飞坏了 ⇒ 3 次各退一层 ⇒ 从详情退到**根路由**；
            //   而根路由 `window.history.state?.back == null` ⇒
            //   `submitSystemBack` 会 `CapApp.exitApp()`（AppLayout.vue:244-245）
            //   ⇒ **App 直接被关掉**。所以除了比 hash，还必须看 pid 还在不在。
            await cdp.ev(`location.replace('#/ai')`, EVM)
            await sleep(1500)
            await goto(cdp, '#/meetings')
            const listH = await cdp.ev('location.hash', EVM)
            await cdp.ev(OPEN_FIRST_MEETING, EVM)
            const deepH = await cdp.ev('location.hash', EVM)
            // 同一 tick 内连点 3 次 —— 此刻它们**必然**重叠在同一次 dispatch 里
            const burst = await cdp.ev(`(() => {
              const b = document.querySelector('.back-btn');
              if (!b) return { ok: false, why: '本页没有 .back-btn（顶栏返回钮没渲染）' };
              let n = 0;
              for (let i = 0; i < 3; i += 1) { if (!b.disabled) { b.click(); n += 1 } }
              return { ok: true, clicked: n, from: location.hash };
            })()`, EVM)
            await sleep(3500)
            const afterBurst = await cdp.ev('location.hash', EVM)
            const pidAfterBurst = sh(['pidof', PKG])
            // 前提自证：按钮真的存在、真的点下去 3 次。
            // 少了它「只退一层」可能是「只点下去一次」的假绿。
            const burstPremise = burst.ok && burst.clicked === 3 && deepH !== listH
            if (!burstPremise) {
              skip('UI-02b', '返回在飞行中叠按：只关一层（BackDispatcher 单飞）',
                `前提不成立：${burst.why || `只点下去 ${burst.clicked} 次（期望 3）`}` +
                `；或栈没搭起来（${listH} → ${deepH}）⇒ 本轮取不到结论`)
            } else {
              const appAlive = !!pidAfterBurst
              record('UI-02b', '返回在飞行中叠按：只关一层（BackDispatcher 单飞）',
                afterBurst === listH && appAlive,
                `同一 tick 点 ${burst.clicked} 次（起点详情 ${deepH}）→ 落点 ${afterBurst}（期望只退一层到 ${listH}）` +
                `；App ${appAlive ? '仍在运行' : '**已被关掉**（pid 没了）'}` +
                (afterBurst !== listH
                  ? ` ⚠️ 退了 ${afterBurst === deepH ? '0' : '≥2'} 层` +
                    (appAlive ? '' : ' ⇒ 单飞失效后落到根路由，触发了 exitApp')
                  : ' ⇒ 3 次叠按被并成 1 次，单飞在设备上确实生效'))
              // ⚠️ 边界要写清：这条验的是**共享仲裁器**的单飞，**不是**硬件键的
              //   按键投递。硬件键能否叠到飞行窗口内，adb 给不出保证（见上）。
              //   ⇒ 想要「硬件键飞行中叠按」的证据，只能在真机上人工快速连按。
            }
          }
          console.log('')
        }
      }
    }
    }

    // ---- UI-01a：抽屉开 → 硬件返回 → 关抽屉且不跳路由 ----
    //
    // ⚠️ 2026-10-06 新增。UI-01 原先只覆盖「详情→列表」，
    //   文档里「抽屉开→关抽屉」一直挂着「量具未实现」。
    //   产品落点是有的：`AppLayout.vue:205-212` 返回链的第一分支就是它。
    //
    // 前提**自己搭**：菜单按钮是 `v-if="showMenuButton && !canGoBack"`，
    // 而 `showMenuButton = route.meta.menu !== false`
    // ⇒ 必须在**游标为 0**（没有可回退前驱）的路由上量，否则按钮根本不渲染。
    if (!only || only === 'ui-01') {
      console.log('--- UI-01a 抽屉与硬件返回 ---')
      // ⚠️ 2026-10-06 **归因订正**（设备实测推翻了我自己写的那句）：
      //   我原来把「按返回没反应」一律写成「返回键未送达 WebView，环境事实」。
      //   实测：返回键**送达了**，是**产品故意吞掉**的 ——
      //   `AppLayout.vue:216`：`if (route.query.unlock === '1') return`
      //   （BUG-B 修复 2026-09-22：解锁未完成时吞掉 back，不许绕过主密码验证）。
      //   指纹是页面停在 `#/login?returnTo=/meetings&unlock=1`，
      //   `.login-view .unlock-hint` 在场（`LoginView.vue:13`，只在 needUnlock 分支）。
      //   ⇒ **把「产品设计」写成「环境没送达」会把一条设计当成故障去修。**
      //     症状（返回键无反应）离病因（守卫刻意 return）只隔一行。
      //
      // ⚠️ 对照判据必须**自建**两深栈：在当前位置直接按返回，落点可能恰好是它自己
      //   （同 UI-13c 踩过：那时 `ctx.cursor` 已是 0，走 fallbacks 的 replace
      //    落回原处 ⇒ 「返回键是活的」与「路由没变」在这个位置**同形**）。
      await cdp.ev(`location.replace('#/ai')`, EVM)
      await sleep(1500)
      // 先离开解锁屏再量：`goto` 自带解锁处理。在解锁屏上量「返回键能否导航」
      // 是**没有意义**的——那一屏的预期行为就是不导航。
      const nav = await goto(cdp, '#/meetings')
      const c1 = await cdp.ev('location.hash', EVM)
      const onUnlockScreen = !!(await cdp.ev(`!!document.querySelector('.login-view .unlock-hint')`, EVM))
      pressBack()
      await sleep(2500)
      const c2 = await cdp.ev('location.hash', EVM)
      const keyDelivered = c1 === '#/meetings' && c2 === '#/ai' && !onUnlockScreen

      record('UI-01b', '【量具对照】无抽屉时按返回确实能导航（UI-01a 的「未变」才可信）',
        keyDelivered,
        `自建栈 #/ai → ${c1}；按返回后 ${c2}（期望 #/ai）` +
        (keyDelivered
          ? ' ⇒ 返回键已证实送达，下面 UI-01a 的「未变」是真量出来的'
          : onUnlockScreen
            ? ' ⚠️ 本轮仍停在**解锁屏**（`.login-view .unlock-hint` 在场，hash=' + c1 + '）：' +
              '`AppLayout.vue:216` 在 `route.query.unlock==="1"` 时**故意 return**（BUG-B：解锁未完成不许绕过主密码）。' +
              '⇒ 「返回键不导航」是**产品设计**，不是缺陷、也不是按键没送达。' +
              (nav && !nav.unlocked ? `解锁未成功：${nav.why}` : '')
            : ' ⚠️ 已不在解锁屏，但返回键**未送达** WebView ⇒ 环境事实，非产品缺陷'))

      // 前提自己搭：菜单按钮是 `v-if="showMenuButton && !canGoBack"`，
      // 而 `showMenuButton = route.meta.menu !== false`
      // ⇒ 必须在**游标为 0**（没有可回退前驱）的路由上量，否则按钮根本不渲染。
      await cdp.ev(`location.replace('#/ai')`, EVM)
      await sleep(2000)
      const u0 = await cdp.ev(DRAWER, EVM)
      if (onUnlockScreen) {
        skip('UI-01a', '抽屉开 → 硬件返回 → 关抽屉且不跳路由',
          `前提不成立：页面仍在解锁屏（hash=${c1}）。那一屏上返回键被 AppLayout.vue:216 故意吞掉，` +
          `量出来的「抽屉关不掉」与「按键被设计吞掉」在观测上完全同形 ⇒ 记未覆盖，不记红。`)
      } else if (!u0.menuBtn) {
        skip('UI-01a', '抽屉开 → 硬件返回 → 关抽屉且不跳路由',
          `根路由上没有 .menu-btn（hash=${u0.href}）⇒ 前提不成立` +
          `（AppLayout 的按钮条件是 showMenuButton && !canGoBack）`)
      } else {
        record('UI-01a-0', '【量具自证】根路由上的菜单按钮存在（否则下面这条空过）',
          true,
          `hash=${u0.href}，.menu-btn 的 aria-expanded=${u0.expanded}、rect={${u0.mRect}}`)
        await cdp.ev(`document.querySelector('.menu-btn').click()`, EVM)
        await sleep(1800)
        const u1 = await cdp.ev(DRAWER, EVM)
        if (!u1.sheet) {
          skip('UI-01a', '抽屉开 → 硬件返回 → 关抽屉且不跳路由',
            `点了 .menu-btn 但抽屉没出现（aria-expanded=${u1.expanded}）`)
        } else {
          pressBack()
          await sleep(2500)
          const u3 = await cdp.ev(DRAWER, EVM)
          record('UI-01a', '抽屉开着时按硬件返回：抽屉关闭、路由不动',
            !u3.sheet && u3.href === u1.href,
            `按返回前 href=${u1.href} drawer=开（body 下 ${u1.bodySheets} 个 sheet）；` +
            `按后 href=${u3.href} drawer=${u3.sheet ? '**仍在**' : '已关'}` +
            (u3.href !== u1.href ? ' ⚠️ 路由被换掉了' : '') +
            `（前提：UI-01b 已证实返回键送达）`)
        }
      }
      console.log('')
    }
  }
} catch (e) {
  // ⚠️ 2026-10-06 实吃：宿主 load 116 时 `/settings` 这一条让
  //   Runtime.evaluate 超过 60s，异常穿透整条主流程，**把已经跑完并
  //   记好的 10 条结果一起带走**，屏幕上只剩一个栈。
  //   ⇒ 一次通道抖动不该让整轮归零。这里降级成一条红，并说清它是环境事实、
  //   且本轮**未取得结论**（不是产品缺陷，也不是通过）。
  record('ENV', '矩阵在通道抖动中中断（环境高压，非产品缺陷）', false,
    `${String(e.message).split('\n')[0].slice(0, 110)}｜中断前已取得 ${results.length} 条结果，全部保留在下方汇总里`)
} finally {
  await cdp.close()
}

const red = results.filter((r) => r.ok === false)
const skipped = results.filter((r) => r.ok === null)
const passed = results.filter((r) => r.ok === true)
console.log(`\n=== 汇总：${passed.length}/${results.length} 通过` +
  (skipped.length ? `，${skipped.length} 未覆盖（既非通过也非失败）` : '') + ' ===')
if (skipped.length) {
  console.log('⬜ 未覆盖（本轮取不到结论）：')
  for (const r of skipped) console.log(`  ⬜ ${r.id} ${r.title}\n       ${r.evidence}`)
}
if (red.length) {
  console.log('红：')
  for (const r of red) console.log(`  🔴 ${r.id} ${r.title}\n       ${r.evidence}`)
}
process.exitCode = red.length ? 1 : 0
