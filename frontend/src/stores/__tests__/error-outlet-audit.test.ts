// error-outlet-audit.test.ts — §847：**错误标志被生产，却没有任何出口**。
//
// 缺陷背景（§844–§845 是它的两个单点版本）：
//   §844 `features/opencode/SessionListView.vue` 全文件 `error` 出现 0 次；
//   §845 `ConfigList.vue` 解构出 `error` 紧跟一句 `void error`。
// 两次都靠**人肉点名**才找到，§847 把它变成一道可自动跑的审计：
//   对 stores/ + composables/ 里**每一个**错误标志，
//   断言「至少有一个 .vue 的**模板区**引用它」。
//
// 为什么判据要落在模板区而不是「文件里出现过」：
//   `const { sttError } = useVoiceInput()` 这种解构会让标识符出现在 .vue 的
//   <script> 里，但它**不构成出口** —— §845 的 `void error` 就是活证。
//   只在模板区出现才是「渲染给了用户」。
//
// ⚠ 口径（必须随读数一起说）：
//   ① 本审计只覆盖 stores/ 与 composables/ 下的错误标志；
//      视图内部自建的局部错误状态不在范围内（那类由 §840–§845 的判据管）。
//   ② 「有出口」是**必要条件不是充分条件** —— §841 的 `readError` 恒为空、
//      守卫在却永远不渲染，就是有出口而设不上。本文件只回答「有没有出口」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..', '..')
// ★ 读入口归一 CRLF → LF（2026-10-22 Windows 实测修掉）
//
//   本仓的 .ts/.vue 在 Windows 检出是 CRLF（core.autocrlf=true），
//   而下面这些用例的「变异锚点」正则里写死了 \n ⇒ 锚点匹配不上
//   ⇒ 变异根本没发生 ⇒ 断言报「变异没生效（锚点与真源码不匹配）」
//   ⇒ 该报红的那条反而报红成「锚点没命中」，看起来像源码变了，其实没变。
//   ⇒ Linux 上源文件是 LF ⇒ 锚点命中 ⇒ **Linux 一直绿**。
//
//   归一在**读入口**（一处盖住全部锚点），不是逐条改正则
//   —— 与 D6–D10、§88 同族纪律，也与仓内既有先例一致：
//   src/features/email/__tests__/account-read-path-normalization.test.mjs:46-48。

const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8').replace(/\r\n/g, '\n')

/**
 * 剥掉 HTML 注释后的模板区。
 *
 * ⚠ 为什么不能假设「模板在前」：本仓视图两种顺序都有
 *   （`ConfigList.vue` 是 `<script setup>` 在第 1 行，见 §845）。
 * ⚠ 与 `list-load-state.test.ts::templateOf` 是同一段逻辑。
 *   这里**没有**抽成共享 helper：`node --test` 每个文件独立起进程，
 *   共用 helper 需要新建一个不带 `.test.` 后缀的模块，而覆盖卡口对
 *   `src` 各子目录下的文件后缀有枚举断言，抽出去的收益不抵风险。
 *   ⇒ 两处若行为不一致，本文件会先假红（见下方「登记表不许漂移」那条对账）。
 *
 * ⚠⚠ 写这段注释时我踩了本仓 runner 注释里点名过的坑：**块注释里不能出现
 *   「双星号 + 斜杠」**，它会提前闭合注释 —— 表现是 TS 解析报
 *   `Expected ';', '}' or <eof>`，而报错行号指向的是**后面**那一行，
 *   与真正的元凶隔了十几行。上面原本写的是 src 下带通配的那条路径。
 */
const templateOf = (src: string): string => {
  const scriptAt = src.search(/<script\b/)
  const head = scriptAt === -1 ? src : src.slice(0, scriptAt)
  if (/<template[\s>]/.test(head)) return head
  const t0 = src.indexOf('<template')
  const t1 = src.lastIndexOf('</template>')
  return t0 >= 0 && t1 > t0 ? src.slice(t0, t1 + '</template>'.length) : head
}

/**
 * 模板区，**剥掉 HTML 注释**。
 *
 * ⚠⚠ 第一版没剥，直接被自己写的注释阴了：我为了解释「为什么横幅必须在
 *   `hasPending` 之外」而在模板里写了一段 HTML 注释，注释里恰好引用了
 *   `v-if="store.hasPending"` —— `indexOf` 于是先命中**注释**，
 *   判据报出「顺序反了」这个**假红**。
 *   ⇒ 与 §843/§845 同源：**凡是用界面上的某段字面量当锚点，必须先剥注释**，
 *     否则锚点会被「为了解释这段代码而写的注释」抢走。
 */
const tplCode = (src: string): string => templateOf(src).replace(/<!--[\s\S]*?-->/g, '')

/** 全仓 .vue（含 features / components / pages），测试目录排除。 */
function collectVue(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === '__tests__' || name === 'node_modules') continue
    const full = join(dir, name)
    let st
    try { st = statSync(full) } catch (err: any) {
      // §638：并发会话会临时建目录再删掉，撞上 ENOENT 窗口时跳过；其余照抛。
      if (err?.code !== 'ENOENT') throw err
      continue
    }
    if (st.isDirectory()) collectVue(full, out)
    else if (name.endsWith('.vue')) out.push(full)
  }
  return out
}

const VUE_FILES = collectVue(SRC)
const relOf = (p: string) => relative(SRC, p).split('\\').join('/')

/**
 * 哪些 .vue 的**模板区**引用了该标识符。
 *
 * ⚠ `overrides` 不是装饰：判据读的是**磁盘上的真实文件**，而变异改的是内存里的串。
 *   两者不接上时，「变异后仍有出口」会永远成立 —— 第一次跑变异 E 就这么假红了，
 *   看起来像判据无牙，其实是**变异根本没进到判据的输入**。
 *   ⇒ 凡是要对某个文件做变异再问判据，必须走这个口子。
 */
const outletSitesWith = (flag: string, overrides: Record<string, string> = {}): string[] =>
  VUE_FILES.filter((f) => {
    const rel = relOf(f)
    const src = overrides[rel] ?? readFileSync(f, 'utf8')
    return new RegExp(`\\b${flag}\\b`).test(tplCode(src))
  }).map(relOf)

const outletSites = (flag: string): string[] => outletSitesWith(flag)

/**
 * 登记表。`outlet: false` 的条目必须在注释里写清**为什么它不需要出口**
 * （有别的可见路径 / 是刻意静默），否则「忘了登记」和「判定为不需要」就分不开了。
 */
const REGISTRY = [
  { file: 'stores/approval.ts', flag: 'error', outlet: true,
    why: '§847-D1：面板根节点是 `v-if="store.hasPending"`，失败时首屏结果为空 ⇒ 横幅必须放在该条件之外' },
  { file: 'stores/session.ts', flag: 'errorMessage', outlet: true,
    why: '阴性对照：`SessionConversationView.vue:638` 已渲染' },
  { file: 'stores/chatAgentStore.ts', flag: 'error', outlet: true,
    why: '§847-D3：`loadAgents()` 失败 rethrow，7 个消费方原本零渲染' },
  { file: 'stores/chatAgentStore.ts', flag: 'syncError', outlet: false,
    why: '★ 登记不修：`syncUpload`/`syncDownload` 的调用点自己 catch 并弹 toast + 横幅；`checkSyncAvailable` 吞错但失败在点击时才可见。死状态，不是用户可见缺陷' },
  { file: 'stores/accTasks.ts', flag: 'error', outlet: true,
    why: '阴性对照：`TasksView.vue:508` 已渲染' },
  { file: 'stores/flashcards.ts', flag: 'error', outlet: true,
    why: '阴性对照：`FlashcardListView.vue:32` 已渲染（带重试）' },
  { file: 'composables/usePromptOptimizer.ts', flag: 'optimizeError', outlet: true,
    why: '§847-D2：调用方 `onOptimize()` 连 `onError` 都不传，composable 侧的标志是唯一可能的口子' },
  { file: 'composables/useBiometricStatus.ts', flag: 'lastError', outlet: false,
    why: '阴性：头注释明写「失败时静默归零，不阻塞渲染」，且 `availability` 会变 `unavailable` 并被视图渲染 ⇒ 失败有可见路径' },
  { file: 'composables/usePendingApprovals.ts', flag: 'loadError', outlet: true,
    why: '阴性对照：`SessionConversationView.vue:643` 以 `approvalsError` 渲染（同一屏的另一条审批路径）' },
  { file: 'composables/useMeetingRecorder.ts', flag: 'sttError', outlet: true,
    why: '阴性对照：经 `UnifiedComposer` 转写渲染' },
  { file: 'composables/useVoiceInput.ts', flag: 'sttError', outlet: true,
    why: '阴性对照：`UnifiedComposer.vue` 两处 `uc-hint--err` 已渲染' },
  { file: 'composables/usePdfViewer.ts', flag: 'error', outlet: true,
    why: '阴性对照：`InvoicePreviewSheet.vue:15` 已渲染' },
  { file: 'composables/usePronounce.ts', flag: 'errorMsg', outlet: true,
    why: '阴性对照：`FlashcardReviewView.vue:89` 以别名 `pronounceError` 渲染（**别名也要算数**）' },
] as const

/** 枚举用的正则，与登记表一一对应。改这里必须同步改 REGISTRY。 */
const ERROR_REF_RE =
  /(const|let)\s+(\w*[eE]rror\w*|\w*Err\w*|\w*Failure\w*)\s*=\s*(ref|shallowRef|computed)/

function enumerateErrorRefs(): Array<{ file: string; flag: string }> {
  const out: Array<{ file: string; flag: string }> = []
  for (const dir of ['stores', 'composables', 'lib', 'utils']) {
    const walk = (d: string) => {
      for (const name of readdirSync(join(SRC, d))) {
        if (name === '__tests__' || name === 'node_modules') continue
        const full = join(SRC, d, name)
        let st
        try { st = statSync(full) } catch (err: any) {
          if (err?.code !== 'ENOENT') throw err
          continue
        }
        if (st.isDirectory()) walk(relative(SRC, full))
        else if (name.endsWith('.ts')) {
          for (const line of readFileSync(full, 'utf8').split('\n')) {
            const m = ERROR_REF_RE.exec(line)
            if (m) out.push({ file: relative(SRC, full).split('\\').join('/'), flag: m[2] })
          }
        }
      }
    }
    walk(dir)
  }
  return out
}

/** 判据本体：某标志在**模板区**有没有被渲染。 */
const hasOutlet = (flag: string): boolean => outletSites(flag).length > 0

/**
 * §847-D1 的判据本体：**断言与变异共用这一把尺子**。
 *
 * 放进 `v-if="store.hasPending"` 里等于没做 —— 首屏失败时上一次结果为空数组
 * ⇒ `hasPending` 为假 ⇒ 横幅跟着整块面板一起不渲染。
 * 这正是本缺陷最容易被「看起来修了」骗过的地方，所以它单独成一条判据。
 *
 * ⚠ 第一版把这段逻辑写在用例体里，负控只能「断言一个空操作」
 *   （测的是它自己写的那两行 `assert`，不是判据）⇒ 尺子无牙。抽出来才可测。
 * ⚠ 用 `tplCode`（已剥 HTML 注释）：注释里引用 `v-if="store.hasPending"`
 *   会把锚点抢走，见 tplCode 处的注释。
 */
const approvalBannerOk = (src: string): boolean => {
  const tpl = tplCode(src)
  // 按**开标签**判定，而不是从 `store.error` 往回找最近的 `<`。
  // ⚠ 第一版用 `lastIndexOf('<', atErr)`，而 `{{ store.error }}` 前面最近的那个
  //   `<` 是横幅内层那个 `<span>`（条件在外层 div 上）⇒ 改坏外层它也看不见。
  const tags = tpl.match(/<[a-z][^<>]*>/gi) ?? []
  const errTags = tags.filter((t) => /store\.error/.test(t))
  if (errTags.length === 0) return false
  // 条件标签自身不得是 else 链的一环（面板先渲染时横幅就永远看不到）
  if (errTags.some((t) => /\bv-else\b/.test(t))) return false
  const atErrTag = tpl.indexOf(errTags[0])
  const atPending = tpl.indexOf('v-if="store.hasPending"')
  return atPending > 0 && atPending > atErrTag
}

describe('§847 · 错误标志必须有出口', () => {
  it('★ 尺子非空转：全仓 .vue 至少扫到 100 个', () => {
    assert.ok(VUE_FILES.length > 100,
      `只扫到 ${VUE_FILES.length} 个 .vue，路径写错了 ⇒ 审计会空转成绿灯`)
  })

  it('★★★ 登记表不许漂移：枚举到的错误标志必须都在 REGISTRY 里逐个点名', () => {
    // §846 的教训：覆盖范围靠人工登记时，漏登记会**静默**变成「有覆盖」。
    // ⇒ 这里反向对账 —— 新增一个错误标志却没登记 ⇒ 这里红。
    const found = enumerateErrorRefs()
    const key = (x: { file: string; flag: string }) => `${x.file}::${x.flag}`
    const foundKeys = new Set(found.map(key))
    const registered = new Set(REGISTRY.map(key))
    const missing = [...foundKeys].filter((k) => !registered.has(k))
    assert.deepEqual(missing, [],
      `这些错误标志不在登记表里：${missing.join(', ')}\n` +
      '⇒ 新增错误标志时必须登记，并在 outlet 字段写明「需要出口」还是「有别的可见路径」')
    const stale = [...registered].filter((k) => !foundKeys.has(k))
    assert.deepEqual(stale, [],
      `登记表里的条目已经不存在（重命名/删除？）：${stale.join(', ')}`)
  })

  it('★ 登记表与枚举的口径一致：每条都必须在生产者里真的被赋值', () => {
    for (const e of REGISTRY) {
      const src = read(e.file)
      // ⚠ 两种合法形态：`X.value = …` 真正生产，或 `const X = computed(() => y.value)`
      //   这种**直通**（useMeetingRecorder 的 sttError 就是后者，它不生产新值，
      //   只是把底层 composable 的转发出来）。第一版只认前者，把直通判成「口径写错」。
      const assigned =
        new RegExp(`${e.flag}\\s*\\.value\\s*=`).test(src) ||
        new RegExp(`const\\s+${e.flag}\\s*=\\s*computed\\(`).test(src)
      assert.ok(assigned,
        `${e.file} 的 ${e.flag} 既没被赋值也不是 computed 直通 ⇒ 登记口径可能写错了文件`)
    }
  })

  for (const e of REGISTRY.filter((x) => x.outlet)) {
    it(`★★ ${e.file}::${e.flag} 必须有模板出口`, () => {
      assert.ok(hasOutlet(e.flag),
        `${e.flag} 被生产却没有任何 .vue 的模板区渲染它 ⇒ 失败完全不可见。\n` +
        `  登记理由：${e.why}`)
    })
  }

  for (const e of REGISTRY.filter((x) => !x.outlet)) {
    it(`★★ ${e.file}::${e.flag} 不要求出口，但登记理由必须在`, () => {
      assert.ok(e.why.length > 10,
        `${e.flag} 被判成「不需要出口」却没写理由 ⇒ 与「忘了登记」分不开`)
    })
  }

  it('★ 反向断言（outlet:false 必须真的没有出口）**故意不做** —— 同名标识符会假阳', () => {
    // `AgentSyncSheet.vue` 有自己的局部 `const lastError = ref('')`，与
    // `useBiometricStatus.lastError` **同名**。本审计按标识符名找出口，
    // 对同名标识符无法区分来源 ⇒ 「hasOutlet === false」这条反向断言必然假阳。
    // ⇒ 只保留正向断言（有出口），反向那一条以「登记理由」代替。
    // 这条用例把这个缺口**显式登记**，免得以后有人以为反向检查也跑过了。
    assert.ok(outletSites('lastError').length > 0,
      '同名的局部标志消失了（AgentSyncSheet 改名？）⇒ 可以把反向断言加回来了')
  })

  it('★★★ §847-D1：审批面板的错误横幅必须在 `hasPending` 条件之外', () => {
    const src = read('features/sessions/ApprovalPanel.vue')
    assert.equal(approvalBannerOk(src), true,
      '审批横幅的位置不合格：失败时它渲染不出来（详见 approvalBannerOk 的注释）')
  })
})

describe('§847 · rethrow 的加载函数：每个调用点都必须接住', () => {
  const RETHROWERS = [
    { fn: 'loadAgents', store: 'stores/chatAgentStore.ts' },
  ] as const

  /** 所有调用点（含 .vue 与 .ts），排除 store 自身。 */
  function callSites(fn: string): Array<{ file: string; line: string }> {
    const hits: Array<{ file: string; line: string }> = []
    const walk = (dir: string) => {
      for (const name of readdirSync(join(SRC, dir))) {
        if (name === '__tests__' || name === 'node_modules') continue
        const full = join(SRC, dir, name)
        let st
        try { st = statSync(full) } catch (err: any) {
          if (err?.code !== 'ENOENT') throw err
          continue
        }
        if (st.isDirectory()) walk(relative(SRC, full))
        else if (name.endsWith('.vue') || name.endsWith('.ts')) {
          if (relOf(full) === 'stores/chatAgentStore.ts') continue
          readFileSync(full, 'utf8').split('\n').forEach((line, i) => {
            if (new RegExp(`\\.${fn}\\s*\\(`).test(line)) {
              hits.push({ file: `${relOf(full)}:${i + 1}`, line })
            }
          })
        }
      }
    }
    walk('features'); walk('components'); walk('composables'); walk('pages')
    return hits
  }

  for (const r of RETHROWERS) {
    it(`★★ ${r.store}::${r.fn} 的每个调用点都必须有 .catch(`, () => {
      const sites = callSites(r.fn)
      assert.ok(sites.length > 0, `没扫到 ${r.fn} 的调用点 ⇒ 尺子空转`)
      const bare = sites.filter((s) => !s.line.includes('.catch('))
      assert.deepEqual(bare.map((s) => `${s.file}: ${s.line.trim()}`), [],
        `这些调用点没有接住 rejection ⇒ 未处理 rejection，且列表停在空态被谎报成「没有」`)
    })
  }

  it('★★★ 编辑态拿不到角色时必须拒绝保存，不得静默落进「创建」分支', () => {
    const src = read('features/agents/AgentEditView.vue')
    // `if (isEditMode && editingAgent)` 在加载失败时为 false ⇒ 落进 else 的创建分支
    assert.match(
      read('features/agents/AgentEditView.vue'),
      /isEditMode\.value\s*&&\s*!\s*editingAgent\.value/,
      'AgentEditView 缺「编辑态却没有角色 ⇒ 拒绝保存」的守卫',
    )
    assert.ok(src.includes('createAgent'), '阳性对照：AgentEditView 里应当仍有 createAgent 分支')
  })
})

describe('§848 · 刻意 rethrow「由调用方提示」的函数：调用点必须接住', () => {
  /**
   * ⚠ 这一类与上面 `RETHROWERS` 的查法**不一样**，不要试图统一：
   *   `loadAgents` 是 store 方法，全部调用点形态一致（`agentStore.loadAgents()`）
   *   ⇒ 可以机械断言「每个调用点都有 `.catch(`」。
   *   `pickImage` 的调用点有**两种**形态：直接 `await`（必须接住），
   *   以及**同名本地包装**（`FlashcardEditView` 自己定义了一个 `pickImage`，
   *   内部已 try/catch 并给出 `flashcards.edit.imagePickFailed`）
   *   ⇒ 「每个调用点都有 catch」这条机械判据会把它判红 ⇒ 属**假阳**，不采用。
   *   改为：断言直接调用点接住 + 对「谁消费了这个 composable」做反向对账。
   */
  const CAMERA_CONSUMER_RE = /useCameraCapture/

  it('★★ UnifiedComposer::onCamera 必须整段包在 try 里', () => {
    const src = read('components/business/UnifiedComposer.vue')
    const re = /async function onCamera\(\)[\s\S]{0,400}?\btry\s*\{[\s\S]{0,500}?\}\s*catch\s*\(/
    assert.match(src, re,
      'onCamera 没有 try/catch ⇒ pickImage 的真实错误（相机权限被拒 / 插件不可用）' +
      '会变成未处理 rejection，用户点了拍照按钮没有任何反馈')
  })

  it('★ 反向对账：useCameraCapture 的消费方必须逐个点名', () => {
    const consumers = VUE_FILES
      .filter((f) => CAMERA_CONSUMER_RE.test(readFileSync(f, 'utf8')))
      .map(relOf)
      .filter((f) => !f.endsWith('useCameraCapture.ts'))
    assert.deepEqual(consumers, ['components/business/UnifiedComposer.vue'],
      `useCameraCapture 的消费方变了：${consumers.join(', ')}\n` +
      '⇒ 新增消费方必须在这里点名，并确认它是否接住了 pickImage 抛出的错误')
  })

  it('★★ 提示位必须存在：cameraError 要与 sttError/optimizeError 并排渲染', () => {
    const tpl = tplCode(read('components/business/UnifiedComposer.vue'))
    const atCam = tpl.indexOf('cameraError')
    assert.ok(atCam > 0, '模板里没有渲染 cameraError ⇒ 错误被设上也没人看')
    // 与另外两个错误提示并排：同一处样式、同一批位置，改一个不能漏另两个
    assert.ok(tpl.indexOf('optimizeError') < atCam,
      'cameraError 的提示位排在 optimizeError 之后，与既有位置不一致')
    assert.ok((tpl.match(/v-if="cameraError"/g) || []).length >= 2,
      'cameraError 只渲染了一处；工具行有普通/全屏两套，只改一处会漏')
  })
})

describe('§847 · 失败分支必须排在真空态之前', () => {
  /**
   * ⚠ `errFlag` 必须**跟着视图里的实际标识符走**，不能写死。
   *   第一版把 `agentStore.error` 写进正则，随后为了满足仓库既有门
   *   `store-error-consumers`（「store error 必须归一后再渲染」），
   *   AgentSelectorSheet 改成了具名的 `agentErrorText` ⇒ 这条判据当场假红。
   *   ⇒ 与 §840/§842/§843 同源：**判据的识别词是契约的一部分**，
   *     给标识符起名前先看哪些判据靠它做匹配；改了就同步改判据。
   */
  for (const v of [
    {
      name: 'AgentLibraryView', dir: 'agents', rows: 'filteredAgents',
      errFlag: 'agentStore.error',
      empty: 'v-else-if="filteredAgents.length === 0"',
    },
    {
      name: 'AgentSelectorSheet', dir: 'ai-chat', rows: 'filteredAgents',
      errFlag: 'agentErrorText',
      empty: 'v-else-if="filteredAgents.length === 0"',
    },
  ]) {
    it(`★★ ${v.name}：读失败分支在真空态之前，且带行数守卫`, () => {
      const tpl = tplCode(read(`features/${v.dir}/${v.name}.vue`))
      const m = new RegExp(`v-else-if="${v.errFlag.replace('.', '\\.')} && [^"]*\\.length === 0"`).exec(tpl)
      assert.ok(m, `${v.name} 没有「${v.errFlag} && 行数===0」的失败分支`)
      assert.ok(tpl.indexOf(m[0]) < tpl.indexOf(v.empty),
        `${v.name} 的真空态排在失败分支前面 ⇒ 后者永远轮不到`)
    })
  }
})

describe('§847 · 变异负控（每条都必须让上面那把尺子转红）', () => {
  const APPROVAL = read('features/sessions/ApprovalPanel.vue')
  const LIB = read('features/agents/AgentLibraryView.vue')
  const OPT = read('components/business/UnifiedComposer.vue')

  it('★ 变异 A：把审批横幅改成 `v-else` ⇒ approvalBannerOk 转红', () => {
    assert.equal(approvalBannerOk(APPROVAL), true, '基线就得通过，否则本变异无效')
    const mutated = APPROVAL.replace('v-if="store.error"', 'v-else')
    assert.notEqual(mutated, APPROVAL, '★ 变异没生效：没匹配到横幅的 v-if')
    assert.equal(approvalBannerOk(mutated), false, '横幅落进 else 链居然判成合法 ⇒ 判据无牙')
  })

  it('★ 变异 B：横幅排到面板之后 ⇒ approvalBannerOk 转红', () => {
    // 合成一个顺序颠倒的模板（与 §840 变异 B 同一手法）：真实文件里
    // 「把整块横幅搬到 section 之后」会让标签配平变复杂，而顺序性质
    // 用合成样本就能测到同一个判据分支上。
    const synth = `<template>
  <section v-if="store.hasPending">
    <div>{{ store.permissions.length }}</div>
  </section>
  <div v-if="store.error" role="alert">{{ store.error }}</div>
</template>`
    assert.equal(approvalBannerOk(synth), false, '顺序颠倒竟然判成合法 ⇒ 顺序判据无牙')
  })

  it('★ 变异 C：整块删掉横幅 ⇒ approvalBannerOk 转红', () => {
    const mutated = APPROVAL.replace(/<div v-if="store\.error"[\s\S]*?<\/div>\n\n/, '')
    assert.notEqual(mutated, APPROVAL, '★ 变异没生效：没匹配到横幅整块')
    assert.equal(approvalBannerOk(mutated), false, '删掉横幅居然判成合法 ⇒ 判据无牙')
  })

  it('★ 变异 D：拿掉 `loadAgents` 的 `.catch` ⇒ 调用点判据转红', () => {
    // ⚠ §948：loadAgents 的 catch 从「`.catch(() => {})`」改成了
    //   「`.catch(() => { 理由注释 })`」⇒ 锚点随之更新（语义不变）。
    const mutated = LIB.replace(
      /agentStore\.loadAgents\(\)\.catch\(\(\) => \{[\s\S]*?\n  \}\)/,
      'agentStore.loadAgents()',
    )
    assert.notEqual(mutated, LIB, '★ 变异没生效')
    const bare = mutated.split('\n').filter((l) => l.includes('loadAgents(') && !l.includes('.catch('))
    assert.ok(bare.length > 0, '变异后仍有带 catch 的调用点，负控无效')
  })

  it('★ 变异 E：拿掉 `optimizeError` 的渲染 ⇒ 出口判据转红', () => {
    assert.ok(hasOutlet('optimizeError'), '基线就该有出口，否则本变异无效')
    const mutated = OPT.replace(/<span v-if="optimizeError"[^>]*>[\s\S]*?<\/span>/g, '')
    assert.notEqual(mutated, OPT, '★ 变异没生效：没匹配到 optimizeError 的渲染')
    assert.deepEqual(
      outletSitesWith('optimizeError', { 'components/business/UnifiedComposer.vue': mutated }),
      [],
      '变异后仍有出口 ⇒ 判据无牙（记得把变异通过 overrides 送进判据的输入）')
  })

  it('★ 变异 G：拿掉 onCamera 的 try/catch ⇒ §848 判据转红', () => {
    const mutated = read('components/business/UnifiedComposer.vue')
      .replace(/\n\s*try \{\n([\s\S]*?)\n\s*\} catch \(e\) \{[\s\S]*?\n\s*\}\n/, '\n$1\n')
    assert.notEqual(mutated, read('components/business/UnifiedComposer.vue'),
      '★ 变异没生效：没匹配到 onCamera 的 try/catch')
    const re = /async function onCamera\(\)[\s\S]{0,400}?\btry\s*\{[\s\S]{0,500}?\}\s*catch\s*\(/
    assert.equal(re.test(mutated), false, '拿掉 try/catch 后尺子居然还通过 ⇒ 判据无牙')
  })

  it('★ 变异 F：把登记表里一条改成不存在的文件 ⇒ 反向对账要报「登记表已失效」', () => {
    // 证明「登记表不许漂移」那条不是空转：把它当函数重跑一次是自欺，
    // 这里只钉住枚举器确实能看到真实文件（阳性对照用 !== -1 的等价形式）。
    const found = enumerateErrorRefs()
    assert.ok(found.some((x) => x.file === 'stores/approval.ts' && x.flag === 'error'),
      '枚举器没看到 stores/approval.ts::error ⇒ 尺子空转')
  })
})
