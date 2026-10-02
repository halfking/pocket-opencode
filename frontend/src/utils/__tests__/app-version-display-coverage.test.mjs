// app-version-display-coverage.test.mjs — 版本号显示点的**不变量**判据。
//
// 为什么不用「一个显示点一条断言」（既有 app-version-identity.test.mjs 就是那么写的）：
//   这一类缺陷我连踩两次。
//     第一次 LoginView：模板里写死 <p>v1.2.0-mobile</p>，连变量都不是；
//     第二次 MoreHubView：页脚注读 APP_VERSION 常量。
//   两次的共同点是：判据守的是**已经知道的地方**。每找到一个显示点就补一条断言，
//   于是第五个畅通无阻 —— 补得再勤也只是在追我自己的记忆。
//
// 这里反过来守不变量：
//   A. 任何 .vue 都不得渲染裸版本号字面量（盖住 LoginView 那种形状，
//      它不出现在任何 import 里，「谁 import 了常量」那类判据根本看不见它）；
//   B. 任何 .vue 都不得把 APP_VERSION 常量直接喂给用户 —— 首帧占位允许，
//      但必须在白名单里**写明理由**，白名单条目失效也要报（防止它烂成万能通行证）；
//   C. 已知显示点必须真的接在 resolveAppVersion 上；反过来，出现清单外的
//      新消费方也要报 —— 逼人做一次决定，而不是默默多一个数据源。
//
// 判据跟着需求变化转红是**对的**。要改的是断言的语义，不是删断言放过。
//
// 全部扫描都走 source-scan.mjs 剥掉注释：不剥的话，下一个人只要在注释里
// 提一句版本号或常量名，结论就会被散文翻过去（第一版就是这么红的）。

import assert from 'node:assert/strict'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { vueFilesUnder, readVueCode, relPosix, findVersionLiterals, computedArgs } from '../../__tests__/source-scan.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const SRC = join(here, '..', '..')

/** 已知会把版本号渲染给用户的文件。正向确认它们接在真相源上。 */
const DISPLAY_SITES = [
  'features/settings/SettingsView.vue',
  'components/UpdateChecker.vue',
  'components/base/SettingsMenuDrawer.vue',
  'features/auth/LoginView.vue',
  'features/more/MoreHubView.vue',
]

/**
 * 允许直接引用 APP_VERSION 常量的文件，**每条都必须写清为什么**。
 * 少了理由就等于开了个不带说明的后门。
 */
const ALLOWED_CONSTANT_READERS = {
  'features/settings/SettingsView.vue':
    '首帧占位：onMounted 之前用常量顶上，避免版本号位置空一拍；随后被 resolveAppVersion 覆盖。',
  'features/more/MoreHubView.vue':
    '同上，页脚注首帧占位。',
}

const files = vueFilesUnder(SRC)
const relOf = new Map(files.map((f) => [relPosix(SRC, f), f]))
const codes = new Map(
  files.map((f) => {
    const rel = relPosix(SRC, f)
    return [rel, readVueCode(f).code]
  }),
)

describe('app version display coverage（不变量，不是枚举）', () => {
  it('防空跑：扫到了真实的 .vue 语料，且已知显示点都还在', () => {
    assert.ok(files.length >= 150, '只扫到 ' + files.length + ' 个 .vue，路径可能不对')
    for (const site of DISPLAY_SITES) {
      assert.ok(relOf.has(site), '已知显示点 ' + site + ' 不见了 —— 文件被改名/移动，请更新清单')
    }
  })

  it('A. 没有任何 .vue 在模板或脚本里写死版本号字面量', () => {
    const hits = []
    for (const [rel, code] of codes) {
      for (const h of findVersionLiterals(code)) hits.push(rel + ':' + h.line + '  ' + h.text)
    }
    assert.equal(
      hits.length,
      0,
      '发现写死的版本号字面量（这类东西不会出现在任何 import 里，靠 import 判据查不到）：\n  ' +
        hits.join('\n  '),
    )
  })

  it('B. 引用 APP_VERSION 常量的文件集合 == 白名单（不多、不少、条目不失效）', () => {
    const actual = [...codes.entries()].filter(([, c]) => /\bAPP_VERSION\b/.test(c)).map(([r]) => r).sort()
    const allowed = Object.keys(ALLOWED_CONSTANT_READERS).sort()

    const unexpected = actual.filter((r) => !allowed.includes(r))
    assert.deepEqual(
      unexpected,
      [],
      '这些文件把常量喂给了界面。若只是首帧占位，请加进 ALLOWED_CONSTANT_READERS 并写明理由：\n  ' +
        unexpected.join('\n  '),
    )

    // 反向：白名单条目失效也要报。否则删了代码的条目会留在白名单里，
    // 白名单慢慢就变成「什么都行」的通行证。
    const stale = allowed.filter((r) => !actual.includes(r))
    assert.deepEqual(
      stale,
      [],
      '白名单里这些文件已经不引用常量了，请删掉对应条目和理由：\n  ' + stale.join('\n  '),
    )

    for (const [r, why] of Object.entries(ALLOWED_CONSTANT_READERS)) {
      assert.ok(why && why.length > 8, '白名单条目 ' + r + ' 没有写明理由')
    }
  })

  it('B2. 常量只能做首帧占位，绝不能成为显示值的活来源', () => {
    // 这条才是真正有牙齿的那条。
    // 负控实测：把 MoreHubView 的 version 退回成 computed(() => APP_VERSION.version)、
    // 而 onMounted 的赋值原样留着 —— 上一版判据（只查「文件里出现过
    // resolveAppVersion」）**照样全绿**。因为符号还在，绑定已经断了。
    //
    // 所以这里锁的是不变式本身：常量允许出现在 ref() 的初值里（首帧占位，
    // 免得版本号位置空一拍），但绝不允许出现在 computed 里或被模板直接绑定 ——
    // 那两种形态下，常量就是用户最终看到的那个值。
    const asComputed = []
    const inTemplate = []
    for (const [rel, code] of codes) {
      // 用括号配对精确取实参，不用「后面没有 ;」这种字符类护栏 ——
      // 本仓库不写分号，那招在正确代码上就会误报（实测踩过）。
      if (computedArgs(code).some((a) => a.includes('APP_VERSION'))) asComputed.push(rel)
      if (/\{\{[^}]*APP_VERSION[^}]*\}\}/.test(code)) inTemplate.push(rel)
    }
    assert.deepEqual(
      asComputed,
      [],
      '这些文件把常量接进了 computed —— 那就是活来源，不是首帧占位：\n  ' + asComputed.join('\n  '),
    )
    assert.deepEqual(
      inTemplate,
      [],
      '模板里直接绑定了常量：\n  ' + inTemplate.join('\n  '),
    )
  })

  it('C. 已知显示点真的调用了 resolveAppVersion（而不是只 import 了它）', () => {
    // 注意这条判据**比较弱**：只要文件里出现过 resolveAppVersion() 调用就过。
    // 真正有牙齿的是上面 B2 —— 我实测过：把 MoreHubView 的 version 退回成
    // computed(() => APP_VERSION.version) 而 onMounted 保留原样时，本条照样绿。
    // 留在这里是为了在「整个接线被删掉」时报出可读的名字，而不是只报一个空集合。
    const bad = DISPLAY_SITES.filter((rel) => !/resolveAppVersion\(\)/.test(codes.get(rel) ?? ''))
    assert.deepEqual(bad, [], '这些显示点没有调用真相源：\n  ' + bad.join('\n  '))
  })

  it('C. 出现清单外的消费方时要报出来（逼一次决定，而不是默默多一个数据源）', () => {
    const consumers = [...codes.entries()]
      .filter(([, c]) => /resolveAppVersion/.test(c))
      .map(([r]) => r)
      .sort()
    const extra = consumers.filter((r) => !DISPLAY_SITES.includes(r))
    assert.deepEqual(
      extra,
      [],
      '这些文件也开始读真相源了。若它们确实把版本号显示给人，请加进 DISPLAY_SITES：\n  ' +
        extra.join('\n  '),
    )
  })
})
