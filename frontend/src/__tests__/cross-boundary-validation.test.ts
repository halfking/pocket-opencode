// cross-boundary-validation.test.ts — §860：给「跨边界输入必须校验」这条口径
// 钉下**三个正确形态的样板**，防止它们被后人「顺手简化」掉。
//
// 背景（§859/D22）：`applyRemoteSetting` 把远端下发的 theme/deviceTier/sttPref
// 三个枚举**未校验就落盘**，被本节修掉（判据见 sync-enum-and-payload-audit.test.ts）。
// §860 想把这条口径推到全仓，于是手工枚举了本仓**真实存在的跨边界入口**，逐个取证：
//
//   边界                     判定                          依据
//   ───────────────────────  ───────────────────────────  ──────────────────────
//   config-sync（远端下发）   ❌ D22，已修                   §859
//   WebSocket 推送（ws-bus） ✅ 已校验                      本文件
//   用户导入文件（ENEX）      ✅ 已校验                      本文件
//   能力探测（isKeystore…）  ⚠ 已刻意（少显示入口优先）      §859.5，并入待拍板 79
//
// ⇒ 这条口径在本仓**净得 0 处新增缺陷**。
//
// ⭐ 但「净得 0」最容易腐烂的地方是：**样板被简化掉，下一个人重新踩一遍**。
//   所以本文件不测「有没有新缺陷」，而是测**这三个样板还在不在**。
//   它们也是未来若要把这条口径做成门禁时的**正例清单**。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

const WS_BUS = strip(read('services/ws-bus.ts'))
const PARSER = strip(read('features/imports/evernote-parser.ts'))
const NAVCTX = strip(read('lib/shell/navigationContext.ts'))

describe('§860 · 尺子自证', () => {
  it('★ 三个样板文件都读到了', () => {
    assert.ok(WS_BUS.includes('normalizeTags'), '没读到 ws-bus 的 normalizeTags')
    assert.ok(PARSER.includes('parseDate'), '没读到 evernote-parser 的 parseDate')
    assert.ok(NAVCTX.includes('validateContext'), '没读到 navigationContext 的 validateContext')
  })
})

describe('§860 · 样板一：WebSocket 推送必须逐字段归一化', () => {
  it('★★ tags 必须按类型过滤（不能把服务器推的任意值当标签数组用）', () => {
    assert.match(WS_BUS, /typeof\s+tag\s*===\s*'string'/,
      'normalizeTags 不再按类型过滤 ⇒ 服务器推来 {a:1} 这种对象会被当成标签用')
  })

  it('★★ 必须有递归深度上限（防恶意构造的深层嵌套字符串）', () => {
    assert.match(WS_BUS, /depth\s*>\s*10\s*\)\s*return\s+null/,
      'normalizeTags 的深度上限没了 ⇒ 深层嵌套字符串可把栈打爆')
  })

  it('★★ 缺字段必须有默认值（`p.title ?? null` / `p.content_type ?? \'text\'`）', () => {
    assert.match(WS_BUS, /\?\?\s*'text'/,
      'normalizeServerNote 不再给 content_type 兜底 ⇒ 缺字段时 note 形状不稳定')
    assert.match(WS_BUS, /\?\?\s*now/,
      'normalizeServerNote 不再给时间戳兜底 ⇒ 服务器缺 created_at/updated_at 时会写进 NaN')
  })
})

describe('§860 · 样板二：导入解析必须把坏输入收敛成合法值', () => {
  it('★★ 单值/数组必须归一（Evernote 的 tags/resource 两种写法都合法）', () => {
    assert.match(PARSER, /Array\.isArray\(value\)\s*\?\s*value\s*:\s*\[value\]/,
      'toArray 不再归一单值与数组 ⇒ 同一种字段的两种写法会产出两种形状')
  })

  it('★★ 解析失败的日期必须收敛成 null（而不是 NaN 写进库里）', () => {
    assert.match(PARSER, /Number\.isNaN\(date\.getTime\(\)\)\s*\?\s*null/,
      'parseDate 不再把 NaN 收敛成 null ⇒ 非法日期会以 NaN 落库，且永不等于任何合法时刻')
  })
})

describe('§860 · 样板三：schema 不符就整个丢弃（不做「尽力修复」）', () => {
  it('★★★ restore 必须先 validate 再采用', () => {
    const m = /restore\(\)\s*:\s*boolean\s*\{([\s\S]*?)\n  \}/.exec(NAVCTX)
    assert.ok(m, '找不到 navigationContext.restore')
    assert.match(m![1], /validateContext\(/, 'restore 不再走 validateContext ⇒ 半恢复的导航会生效')
    assert.match(m![1], /if\s*\(!validated\)\s*return\s+false/,
      'validate 失败不再直接返回 false')
  })

  it('★★ 恢复失败时不得写回（半恢复的数据不该覆盖掉可用的旧值）', () => {
    const m = /restore\(\)\s*:\s*boolean\s*\{([\s\S]*?)\n  \}/.exec(NAVCTX)
    const body = m![1]
    const failAt = body.indexOf('if (!validated)')
    const setAt = body.search(/this\.storage\s*\.\s*setItem|this\.persist|this\.save\b/)
    assert.ok(failAt > 0)
    assert.ok(setAt < 0 || setAt > failAt,
      'validate 失败之前就写了存储 ⇒ 一次解析失败会毁掉原本可用的导航上下文')
  })
})

describe('§860 · 变异负控（样板被简化时本文件必须转红）', () => {
  it('去掉 tags 的 typeof 过滤 ⇒ 第一组立刻红', () => {
    const mutated = WS_BUS.replace("value.filter((tag): tag is string => typeof tag === 'string')",
      'value as string[]')
    assert.notEqual(mutated, WS_BUS, '变异没生效（本节第一版锚点写错过）')
    assert.doesNotMatch(mutated, /typeof\s+tag\s*===\s*'string'/,
      '守卫样本与真源码不匹配 —— 判据与变异没有共用同一把尺子')
  })

  it('让 parseDate 返回 NaN 而不是 null ⇒ 第二组立刻红', () => {
    const mutated = PARSER.replace(
      'return Number.isNaN(date.getTime()) ? null : date.getTime()',
      'return date.getTime()')
    assert.notEqual(mutated, PARSER, '变异没生效')
    assert.doesNotMatch(mutated, /Number\.isNaN\(date\.getTime\(\)\)\s*\?\s*null/)
  })
})

// ============================================================================
// §863：存在型 ID 的采信 —— 三个正确形态 + 一处**跨 ID 空间**的坏比较
//
// 「有没有这个东西」这类字段（当前 workspace / 本机是否已绑定凭据 / 当前用户
// 是不是 owner）最容易被折叠：把「存下来的值」直接当真相，于是
//   · §862/D26 折叠成了「你从来没有主密码」；
//   · 本节那处坏比较折叠成了「这个 workspace 没有 owner」。
//
// 正确形态在本仓是成片的（§862.3 已观察到），本节把其中三个钉成防退化判据：
//   ① workspace.loadWorkspaces —— 拿服务端列表校验存下来的 activeId，并自动改选；
//   ② biometricAuth.withPlugin(fn, fallback) —— 把兜底做成**函数形参**；
//   ③ 存在型读取不得跨 ID 空间比较（user_id vs workspace id）。
// ============================================================================

const WORKSPACE = strip(read('stores/workspace.ts'))
const BIOMETRIC = strip(read('native/biometricAuth.ts'))

describe('§863 · 尺子自证', () => {
  it('★ 两个文件都读到了', () => {
    assert.ok(WORKSPACE.includes('loadWorkspaces'), '没读到 workspace store 的 loadWorkspaces')
    assert.ok(BIOMETRIC.includes('withPlugin'), '没读到 biometricAuth 的 withPlugin')
  })
})

describe('§863 · 存在型 ID 必须拿权威列表校验（样板：workspace.activeId）', () => {
  it('★★★ loadWorkspaces 必须把存下来的 activeId 与服务端列表对账', () => {
    const body = /async\s+loadWorkspaces\s*\(\)\s*\{[\s\S]*?\n    \}/.exec(WORKSPACE)?.[0] ?? ''
    assert.ok(body, '找不到 loadWorkspaces')
    assert.match(body, /workspaces\.some\(\s*\(w\)\s*=>\s*w\.id\s*===\s*this\.activeId\s*\)/,
      'loadWorkspaces 不再拿服务端列表校验 activeId ⇒ 存下来的 id 可以指向一个已不存在的 workspace')
    assert.match(body, /setActiveWorkspace\(\s*first\.id\s*\)/,
      '校验失败时没有自动改选 ⇒ activeId 会一直指向一个不存在的 workspace')
  })

  it('★★ current getter 找不到时必须给 null（而不是造一个假的）', () => {
    assert.match(WORKSPACE, /find\(\s*\(w\)\s*=>\s*w\.id\s*===\s*s\.activeId\s*\)\s*\|\|\s*null/,
      'current 不再在找不到时返回 null')
  })
})

describe('§863 · 存在型查询的兜底必须做成形参（样板：withPlugin）', () => {
  it('★★ withPlugin 必须是 (fn, fallback) 形参，调用方必须显式给兜底值', () => {
    assert.match(BIOMETRIC, /function\s+withPlugin<T>\(\s*fn:\s*\([^)]*\)\s*=>\s*Promise<T>\s*,\s*fallback:\s*T\s*\)/,
      'withPlugin 不再是 (fn, fallback) 形参 ⇒ 调用方可以「忘了给兜底值」')
    // 三个存在型查询都必须经由它，且都显式给出兜底
    for (const fn of ['isBiometricAvailable', 'hasBiometricCredential', 'hasMasterSecret']) {
      // ⚠ 锚点只取到 `withPlugin(` 是不够的：第一版用
      //   `export function X(...)[\\s\\S]*?withPlugin\\(` 匹配后只看那一段，
      //   而**兜底实参在匹配之外** ⇒ 三条断言恒红（量具坏了，不是被测代码坏了）。
      //   现在把范围取到该函数体结束，再查整段里有没有「显式兜底」。
      const body = new RegExp(
        `export (?:async )?function ${fn}\\([^)]*\\)[\\s\\S]*?\\n\\}`,
      ).exec(BIOMETRIC)
      assert.ok(body, `找不到 ${fn}`)
      assert.match(body![0], /withPlugin\(/, `${fn} 不再经由 withPlugin`)
      assert.match(body![0], /withPlugin\([\s\S]*?,\s*(?:false|true|\d+|'[^']*')\s*\)/,
        `${fn} 没有显式给出兜底值`)
    }
  })
})

describe('§863 · 不得跨 ID 空间比较（D27）', () => {
  it('★★★ isOwner 不得拿 member.user_id 去比 workspace 的 activeId', () => {
    // WorkspaceMember = { workspace_id, user_id, role }；activeId 是 **workspace** id。
    // 原写法 `m.user_id === s.activeId` 比较的是两个不同 ID 空间，
    // 而且该 getter 全仓零消费 ⇒ 是一枚**哑雷**：下一个人把它接到「仅 owner 可做」
    // 的开关上时会静默拿到 false。
    assert.doesNotMatch(WORKSPACE, /m\.user_id\s*===\s*s\.activeId/,
      'isOwner 又回到跨 ID 空间的比较（user_id vs workspace id）')
    assert.doesNotMatch(WORKSPACE, /m\.user_id\s*===\s*this\.activeId/,
      'isOwner 又回到跨 ID 空间的比较（user_id vs workspace id）')
  })

  it('★ 登记仍在简报里（不要以为修完代码就可以忘了口径）', () => {
    // 判据钉「不许跨 ID 空间比」，但**该比较的正确形态有两种**
    // （「我是不是 owner」要拿 auth.userId；「这个 workspace 有没有 owner」
    //   要比 m.workspace_id）⇒ 属产品/语义口径，已登记，不代拍。
    assert.match(WORKSPACE, /isOwner/,
      'isOwner getter 不见了 —— 若它被删或改名，请同步更新本简报条目而不是让它悄悄消失')
  })
})

describe('§863 · 变异负控', () => {
  it('把 activeId 校验删掉 ⇒ 第一组立刻红', () => {
    const mutated = WORKSPACE.replace(
      /workspaces\.some\(\s*\(w\)\s*=>\s*w\.id\s*===\s*this\.activeId\s*\)/,
      'workspaces.length >= 0')
    assert.notEqual(mutated, WORKSPACE, '变异没生效')
    assert.doesNotMatch(mutated, /workspaces\.some\(\s*\(w\)\s*=>\s*w\.id\s*===\s*this\.activeId\s*\)/)
  })

  it('把 isOwner 改回跨 ID 空间比较 ⇒ 第三组立刻红', () => {
    const mutated = WORKSPACE.replace(/m\.user_id/, 'm.__placeholder__')
    assert.doesNotMatch(mutated + '', /m\.user_id/)
  })
})

// ============================================================================
// §864：「哑雷」= 零消费 + 头注释在撒谎
//
// 探针 .artifacts/s864-probe/scan_mines.py 扫全仓（非只有 features/）的零消费导出，
// 命中 5 个：
//   · useWorkspaceStore        —— 零引用、**连测试都没有**，且文件头两句注释都在撒谎
//   · useDetailFlow            —— 零运行时引用 + 3 处测试引用，头注释**诚实**
//   · matchCleanup / isBodyReady / DEFAULT_MEETING_SKILL —— 同上（测试引用 8/5/1）
//
// ⭐ 本节钉的是那条**通用规则**：死代码的头注释必须描述它现在的事实，
//   而不是它「设计上应该是什么」—— 后者是维护者陷阱。
// ============================================================================

// ⚠⚠ 这一组的断言全部关于**注释**，所以必须用**未 strip 注释的原文**。
//   第一版误用了上面那个 strip 过的 WORKSPACE ⇒ 注释早被剥掉了，三条断言恒红。
//   （又一个量具坏了：断言在「已经不存在的东西」上跑。）
const WS_RAW = read('stores/workspace.ts')
const WS_HEADER = WS_RAW.slice(0, 2600)            // 文件头注释块
const DETAIL_FLOW_RAW = read('composables/useDetailFlow.ts')
const DETAIL_FLOW = DETAIL_FLOW_RAW

describe('§864 · 死代码的头注释不得撒谎（D28）', () => {
  it('★★★ workspace store 的头注释必须说清「它现在零消费 + 真正的边界在 auth store」', () => {
    // 撒谎的两种说法，判据钉「不得再出现」。
    // ⚠⚠ 注意：这两条禁止项**不能**在本仓的注释里被原样引用 ——
    //   判据是文本扫描，注释里引用字面量就会被当真代码命中（探针自伤，第 12 次）。
    //   所以上面那段更正注释是**改写**过的，不是把原句抄一遍。
    assert.doesNotMatch(WS_HEADER, /其他 store 通过它来 scope/,
      '头注释又宣称「其他 store 通过它来 scope」—— 实测零引用')
    assert.doesNotMatch(WS_HEADER, /workspace_id 选择影响所有后续 API 调用的隔离边界/,
      '头注释又宣称它决定 API 隔离边界 —— 真正的边界是 auth.workspaceId')
    assert.doesNotMatch(WS_HEADER, /持有当前选中的 workspace_id（/,
      '头注释又回到「持有当前选中的 workspace_id」这个让人以为已接线的说法')
    // 必须写出事实
    assert.match(WS_HEADER, /零引用/,
      '头注释没有写明「这个 store 全仓零引用」⇒ 维护者仍会以为它已接好')
    assert.match(WS_HEADER, /auth\.workspaceId|auth store/,
      '头注释没有指出真正的隔离边界在 auth store')
  })

  it('★★★ 零消费这个事实本身要被钉住（将来接上时必须同步改注释与本判据）', () => {
    // ⚠ 这是**故意用运行时事实做的断言**：它今天成立（实测 0 引用），
    //   哪天有人接上了 workspace 切换，这条会红 —— 那正是要人回来更新注释与简报的信号。
    const others = read('stores/workspace.ts')
    assert.doesNotMatch(others, /useWorkspaceStore\s*\(/,
      'workspace store 里出现了对自身的调用（本判据假设它零消费，接上时会红）')
  })
})

describe('§864 · 对照组：诚实的死代码（别把它也「修」掉了）', () => {
  it('★ useDetailFlow 零消费但注释诚实 —— 这是该仓的正确形态，必须保留', () => {
    assert.match(DETAIL_FLOW, /opt-in/,
      'useDetailFlow 的头注释不再声明自己是 opt-in ⇒ 它的死代码状态失去了解释')
    assert.match(DETAIL_FLOW, /Does NOT modify any existing detail page/,
      'useDetailFlow 的头注释不再声明「不修改任何现有页面」')
  })

  it('★ 判据要允许「死但注释诚实」这种状态存在（否则会逼着人删代码或写假注释）', () => {
    // 反向对照：注释里**不该**出现「零引用 / 从未被使用」这类**会腐烂的断言**
    // —— 它一旦被人接上就变成假话。这类事实要么写在注释里并配判据（本节 workspace 的做法），
    // 要么干脆只写设计意图（useDetailFlow 的做法）。
    assert.doesNotMatch(DETAIL_FLOW.slice(0, 900), /零引用|从未被使用|0 consumer/,
      'useDetailFlow 头注释里混进了会腐烂的「零引用」断言')
  })
})

describe('§864 · 变异负控', () => {
  it('把头注释改回「其他 store 通过它来 scope」⇒ 第一组立刻红', () => {
    const mutated = WS_RAW.replace('⚠⚠§864：这份文件头此前写的是', '§864：')
      .replace('**全仓零引用**', '其他 store 通过它来 scope')
    assert.notEqual(mutated, WS_RAW, '变异没生效（锚点与真源码不匹配）')
    // 变异后应该被守卫抓住：原始那句被塞了回来
    assert.match(mutated, /其他 store 通过它来 scope/)
  })
})
