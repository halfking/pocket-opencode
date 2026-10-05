/**
 * 设备真实构建身份：原生 BuildConfig 优先，常量兜底。
 *
 * ## 这个护栏防的是什么
 *
 * SettingsView 的「应用信息」过去直接渲染 TS 常量 APP_VERSION，于是**无论设备
 * 上装的是哪个 APK，界面都显示 "v1.2.0 (Build 2) / 2026-06-29"**。而 gradle
 * 里写的是 versionCode 3 / versionName "1.2.0-openpocket"，build-mobile.mjs
 * 两个计数器都不碰。
 *
 * 后果不是「版本号难看了」，而是**「设备上跑的是不是最新包」这件事从界面上
 * 完全看不出来**——docs/handoff §4.74.2 就是这么丢掉一整轮验收的：整轮在测
 * 一个过时产物，没有任何异常信号。
 *
 * ## 为什么判据必须是行为而不是形状
 *
 * 只断言「SettingsView 里出现了 resolveAppVersion」是装饰性的：把 onMounted
 * 里的赋值删掉、模板改回常量，源码里那个词还在，判据照样绿。
 *
 * 下面第 1 组真的调用 resolveAppVersion，并区分**两个不同的值域**：
 *   - 原生返回 1.2.0-openpocket / build 3  -> 期望 fromNative=true 且拿到 3
 *   - provider 抛错                        -> 期望回退到常量且 fromNative=false
 * 这两条结论**不可能同时由「恒返回常量」的实现给出**，所以判据有区分度。
 *
 * 顺带钉住一条容易顺手做错的事：checkUpdate() 上报的 currentVersion 仍用常量
 * 而不是原生值。原生的 versionName 带 "-openpocket" 后缀，喂给服务端的版本
 * 比较会被判成比 "1.2.0" 更旧，从而**误报「发现新版本」**。统一版本语义是
 * 产品决定（见 handoff 待决项），在那之前比较层必须留在常量上。
 */

import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  APP_VERSION,
  resolveAppVersion,
  buildTimestamp,
  displayBuildDate,
  __setNativeInfoProviderForTest,
  checkUpdate,
} from '../version.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))   // frontend/src/utils/__tests__
const REPO = path.join(HERE, '..', '..', '..', '..')        // 仓库根
const readAbs = (p) => fs.readFileSync(path.join(REPO, p), 'utf8')

test.afterEach(() => {
  __setNativeInfoProviderForTest(null)
})

// ---------------------------------------------------------------------------
// 1. 行为：原生值真的赢过常量，且两者的值刻意不同
// ---------------------------------------------------------------------------
test('原生 BuildConfig 的 versionName/versionCode 必须压过 APP_VERSION 常量', async () => {
  // 刻意用与常量不同的值：常量是 1.2.0 / build 2，原生是 1.2.0-openpocket / 3。
  // 如果实现退化成「忽略原生、返回常量」，下面两条断言会同时转红。
  __setNativeInfoProviderForTest(async () => ({ version: '1.2.0-openpocket', build: '3' }))
  const v = await resolveAppVersion()

  assert.equal(v.fromNative, true, '拿到了原生信息，fromNative 必须是 true')
  assert.equal(v.version, '1.2.0-openpocket')
  assert.equal(v.buildNumber, 3)
  assert.notEqual(v.version, APP_VERSION.version,
    '本用例失去意义：注入的原生值与常量相同，恒返回常量的实现也能通过')
})

test('provider 抛错时必须回退到常量并把 fromNative 置 false，绝不把异常抛给调用方', async () => {
  __setNativeInfoProviderForTest(async () => { throw new Error('no bridge') })
  const v = await resolveAppVersion()

  assert.equal(v.fromNative, false)
  assert.equal(v.version, APP_VERSION.version)
  assert.equal(v.buildNumber, APP_VERSION.buildNumber)
  assert.equal(v.name, APP_VERSION.name)
})

test('原生返回的 build 是字符串：Android 的 versionCode 必须被解析成数字', async () => {
  // App.getInfo() 的 build 类型是 string。若漏了 parseInt，比较层会拿
  // 字符串 "3" 去和服务端的 int 比，行为随 JS 隐式转换漂移。
  __setNativeInfoProviderForTest(async () => ({ version: '9.9.9', build: '42' }))
  const v = await resolveAppVersion()
  assert.equal(typeof v.buildNumber, 'number', 'buildNumber 必须是 number，不是字符串')
  assert.equal(v.buildNumber, 42)
})

test('原生返回空/垃圾字段时逐项回退，不得产出 NaN 或空串版本号', async () => {
  __setNativeInfoProviderForTest(async () => ({ version: '   ', build: 'not-a-number' }))
  const v = await resolveAppVersion()
  assert.equal(v.version, APP_VERSION.version, '空白 version 应回退到常量')
  assert.equal(v.buildNumber, APP_VERSION.buildNumber, '非数字 build 应回退到常量')
  assert.ok(!Number.isNaN(v.buildNumber), 'buildNumber 绝不能是 NaN——那会让版本比较静默失效')
})

test('结果必须缓存：同一进程内版本号不会变，不该每次渲染都去问原生', async () => {
  let calls = 0
  __setNativeInfoProviderForTest(async () => { calls += 1; return { version: '5.0.0', build: '7' } })
  const a = await resolveAppVersion()
  const b = await resolveAppVersion()
  assert.equal(calls, 1, 'resolveAppVersion 被调用两次时原生只该被问一次，实际 ' + calls + ' 次')
  assert.equal(a.version, b.version)
})

// ---------------------------------------------------------------------------
// 2. 契约：设置页真的用了解析值，而不是又直接摸常量
// ---------------------------------------------------------------------------
test('SettingsView 的版本行必须读 appVersion（解析值），不能直接摸 APP_VERSION', () => {
  const src = readAbs('frontend/src/features/settings/SettingsView.vue')

  // 版本行：必须来自 appVersion
  assert.ok(
    /t\('settings\.versionFormat',\s*\{\s*version:\s*appVersion\.version,\s*buildNumber:\s*appVersion\.buildNumber\s*\}\)/.test(src),
    '设置页版本行没有读 appVersion —— 界面会继续显示常量，也就继续分不清设备上装的是哪个构建',
  )

  // 挂载时必须真的赋值，否则 appVersion 永远停在初值（= 常量），同上
  assert.ok(
    /appVersion\.value\s*=\s*await resolveAppVersion\(\)/.test(src),
    'SettingsView 的 onMounted 没有调用 resolveAppVersion —— appVersion 永远是初值',
  )
})

test('checkUpdate 上报的是「归一后的原生身份」，既不是常量也不是带后缀的原始 versionName', async () => {
  // 这条判据 2026-10-06 改写过一次。改写前它钉的是「必须用常量」，
  // 理由是原生 versionName 带 "-openpocket"，而服务端 splitVersion
  // （backend/internal/server/app_version_compare.go:111）把 `-xxx` 当**预发布**，
  // 于是原样上报会被判成比 "1.2.0" 更旧 → 误报「发现新版本」。
  //
  // 旧钉法的代价是**永久欠报**：客户端上报常量 buildNumber=2，而 gradle 里
  // 真实 versionCode 已经是 3。于是服务端一旦把 version.json 升到 build 3，
  // 每一台已是最新版的设备都会被告知「有更新」，而 UpdateChecker 在
  // onMounted 就弹模态框 ⇒ 每天冷启动被同一个假更新拦住。这与「无感更新」相反。
  //
  // 现在改成钉**行为**（抓真实请求体），且三个值域刻意互不相同：
  //   常量          = '1.2.0' / 2
  //   原生原始串    = '2.0.0-openpocket' / 7
  //   期望上报      = '2.0.0'      / 7      ← 归一后 + 真实 build
  // 「恒返回常量」与「原样上报原生」两种实现都过不了这一条。
  __setNativeInfoProviderForTest(async () => ({ version: '2.0.0-openpocket', build: '7' }))

  const realFetch = globalThis.fetch
  let body = null
  globalThis.fetch = async (_url, init) => {
    body = JSON.parse(init.body)
    return {
      ok: true,
      // assertNotHTML 会读 headers.get('content-type')，缺了会抛
      // "Cannot read properties of undefined (reading 'get')"，
      // 那是在测假对象而不是测产品。
      headers: { get: () => 'application/json' },
      clone() { return this },
      async json() { return { hasUpdate: false, forceUpdate: false, message: '' } },
    }
  }
  try {
    await checkUpdate()
  } finally {
    globalThis.fetch = realFetch
  }

  assert.ok(body, 'checkUpdate 没有发出请求 —— 判据不能靠「没调用就当没违规」过关')
  assert.equal(
    body.currentVersion, '2.0.0',
    '必须剥掉构建变体后缀再上报：服务端把 `-xxx` 当预发布，带后缀会被判成更旧',
  )
  assert.equal(
    body.currentBuild, 7,
    '必须上报原生的真实 buildNumber（7），不是 APP_VERSION 常量里的 2 —— ' +
      '欠报会让已是最新版的设备被反复提示更新',
  )
  assert.notEqual(body.currentVersion, APP_VERSION.version, '不能回落成常量版本')
  assert.notEqual(body.currentVersion, '2.0.0-openpocket', '不能原样上报带后缀的 versionName')
})

test('更新弹窗的「当前版本」也必须读解析值（同一个病灶的第二个显示点）', () => {
  // UpdateChecker 是第二个把版本号显示给人的地方。它过去同样直接读常量，
  // 于是弹窗会写「当前 v1.2.0 (Build 2)」而设备上装的是 Build 3 ——
  // 用户拿这一行判断「我是不是最新版」时，被告知的信息本身就是错的。
  const src = readAbs('frontend/src/components/UpdateChecker.vue')

  assert.ok(
    /const\s+currentVersion\s*=\s*ref\(''\)/.test(src),
    'UpdateChecker 的 currentVersion 不再是 ref —— 若是有意改动请同步改本用例',
  )
  assert.ok(
    /await resolveAppVersion\(\)/.test(src),
    'UpdateChecker 没有调用 resolveAppVersion —— 弹窗会继续显示常量',
  )
  assert.ok(
    /currentVersion\.value\s*=\s*me\.version/.test(src) &&
      /currentBuild\.value\s*=\s*me\.buildNumber/.test(src),
    'UpdateChecker 拿到了解析值却没有赋给模板用的 ref',
  )
  // 旧写法必须已经不在了
  assert.ok(
    !/currentVersion\s*=\s*APP_VERSION\.version/.test(src),
    'UpdateChecker 仍在用 APP_VERSION.version —— 弹窗继续显示假的当前版本',
  )
})

test('侧边抽屉的版本脚注也必须读解析值（第三个显示点）', () => {
  // SettingsMenuDrawer 底部那行「Redclaw · v{version}」过去读的是常量。
  // 它不是可有可无的角落：用户判断「我是不是最新版」时最先看的就是这一行，
  // 而它在任何 APK 上都写同一个版本号 —— 说的全是假话。
  const src = readAbs('frontend/src/components/base/SettingsMenuDrawer.vue')
  assert.ok(
    /version\.value\s*=\s*\(await resolveAppVersion\(\)\)\.version/.test(src),
    'SettingsMenuDrawer 的版本脚注没有用 resolveAppVersion —— 它继续显示常量',
  )
  assert.ok(
    !/APP_VERSION\.version/.test(src),
    'SettingsMenuDrawer 仍在读 APP_VERSION.version —— 脚注继续显示假的当前版本',
  )
})

test('空分组不得渲染标题：运维与高级没有条目时不能只剩一个空标题', () => {
  // 真机上侧边抽屉出现一个只有标题、下面什么都没有的「运维与高级」分区。
  // 根因：groups 里那一组 items 是空的（2026-09-23 迁移后留下的空壳），
  // 而模板 v-for 无条件渲染每个 section，于是空壳的 <h4> 也画了出来。
  // 看着就像渲染坏了。
  const src = readAbs('frontend/src/components/base/SettingsMenuDrawer.vue')

  assert.ok(
    /v-for="group in nonEmptyGroups"/.test(src),
    '模板仍然遍历原始 groups —— 空分组的标题会被渲染出来。请改用 nonEmptyGroups。',
  )
  assert.ok(
    /groups\.value\.filter\(\(g\) => g\.items\.length > 0\)/.test(src),
    'nonEmptyGroups 没有按 items.length 过滤 —— 判据不能靠「过滤了就行」过关，要看过滤条件',
  )
  // 确认那一组确实是空的（否则这条判据的前提就不成立）
  const opsBlock = /title:\s*t\('settingsMenu\.groupOps'\)[\s\S]{0,80}?items:\s*\[\s*\]/.test(src)
  assert.ok(opsBlock,
    'groupOps 那一组的 items 不再是空数组 —— 若是有意加了条目，请删掉或改写本判据，' +
      '它现在描述的是「空分组」这个前提')
})

test('登录页底部版本号不得是模板里的裸字面量（第四个显示点）', () => {
  // 这一处比前三处都糟：模板里写的是 <p>v1.2.0-mobile</p> —— **连变量都不是**。
  // 改版本号只能靠全文搜字符串，于是它在前三轮里被一次又一次漏掉，
  // 而登录页恰恰是用户**第一眼**看到版本号的地方。
  const src = readAbs('frontend/src/features/auth/LoginView.vue')

  assert.ok(
    !/<p>\s*v?[\d.]+[\w.-]*\s*<\/p>/.test(src),
    'LoginView 的模板里仍有裸版本号字面量。请改成读 appVersion。',
  )
  assert.ok(
    /\{\{\s*appVersion\s*\}\}/.test(src),
    'LoginView 没有渲染 appVersion —— 登录页底部那行版本号会变成空的',
  )
  assert.ok(
    /appVersion\.value\s*=\s*\(await resolveAppVersion\(\)\)\.version/.test(src),
    'LoginView 的 appVersion 没有用 resolveAppVersion 赋值',
  )
  assert.ok(/import\s*\{[^}]*resolveAppVersion[^}]*\}\s*from\s*'.*version'/.test(src),
    'LoginView 没有导入 resolveAppVersion')
})

test('构建日期优先用编译期时间戳；只有退回常量时才加来源标注', () => {
  // buildDate 现在有真相源了：vite 在编译期把构建时刻注入 __BUILD_TIME__
  // （见 vite.config.ts 的 define）。有它就不该再显示「这是配置值」——
  // 那时候显示的就是真的构建时刻，标注反而变成误导。
  const src = readAbs('frontend/src/features/settings/SettingsView.vue')
  assert.ok(
    /v-if="!appVersion\.fromNative"/.test(src),
    '来源标注必须只在回退路径出现。有真实构建时间戳时还显示「配置值」标注是误导。',
  )
  assert.ok(
    /\{\{\s*appVersion\.buildDate\s*\}\}/.test(src),
    '构建日期行必须读 appVersion.buildDate（真实值），不能继续用 APP_VERSION.buildDate',
  )
  assert.ok(
    !/\{\{\s*APP_VERSION\.buildDate\s*\}\}/.test(src),
    '构建日期行仍在读常量 APP_VERSION.buildDate —— 那正是与产物无关的旧日期',
  )

  // 词条仍必须存在（回退路径要用），且 9 个语言包都要有
  for (const locale of ['zh-CN', 'zh-TW', 'en-US', 'de-DE', 'es-ES', 'fr-FR', 'ja-JP', 'ko-KR', 'pt-BR']) {
    const parsed = JSON.parse(readAbs('frontend/src/locales/' + locale + '.json'))
    assert.ok(
      typeof parsed.settings.buildDateNote === 'string' && parsed.settings.buildDateNote.length > 0,
      locale + '.json 缺少 settings.buildDateNote',
    )
  }
})

test('buildTimestamp()：node 下没有编译期常量时必须返回 null 而不是 ReferenceError', () => {
  // 判据自己 import 了 version.ts，所以这一条是真实跑 __BUILD_TIME__ 分支。
  // 若守卫写错（比如直接引用未声明标识符），import 阶段就炸 —— 那是「跑不起来」，
  // 不是「测试失败」，两者在 CI 输出里长得完全不一样，必须能分开。
  const t = buildTimestamp()
  assert.ok(t === null || (typeof t === 'string' && t.length > 0),
    'buildTimestamp() 既不是 null 也不是非空字符串，实际 = ' + JSON.stringify(t))

  const d = displayBuildDate()
  assert.ok(typeof d === 'string' && d.length > 0, 'displayBuildDate() 必须永远返回非空字符串')
})
