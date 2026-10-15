// capabilities-stub-gate.test.mjs — 门禁：**「已注册但未实现」的插件必须如实登记**。
//
// 背景（§179）
// ----------
// Capacitor 的 `isPluginAvailable()` 查的是**运行时插件注册表**，不查方法有没有实现。
// Android 的 `MainActivity.onCreate` 里 `registerPlugin(SherpaPlugin.class)` 让它进了
// PluginHeaders ⇒ 真机上 `isPluginAvailable('Sherpa')` 返回 **true**；
// 而 `SherpaPlugin.java` 的 5 个功能方法全是 `call.reject("... not integrated (Phase 4)")`。
//
// ⇒ 不加登记，能力快照会报 `recognition.asr = 'model'`（本地 ASR 可用），
//   而真实调用 100% 失败 —— 「能力探测绿灯、功能全无」。
//
// 这道门守什么
// ------------
// 登记表与 Java 源码**交叉校验**，且判据不是「认得 Sherpa 这个名字」，而是
// **「作者自己在类注释里写了这是占位/骨架」**：
//
//   1. 每个登记项：对应 Java 文件必须存在，且**指纹仍在**（骨架没被真正实现）
//   2. **任何**类注释含占位标记的插件，**必须**已登记 ⇒ 新增骨架会被自动抓住
//   3. 指纹不得出现在未登记的插件里
//   4. 负控：把登记去掉 ⇒ 第 1 条必须转红（证明它真在咬）
//
// ⇒ AAR 真接进来的那天，`SherpaPlugin.java` 的占位注释会先消失 ⇒ 第 1 条红 ⇒
//   提醒把名字移出登记表，而不是让它继续骗人。
//
// Run: node --test src/lib/shell/__tests__/capabilities-stub-gate.test.mjs
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readdirSync, readFileSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { NOT_IMPLEMENTED_PLUGINS } from '../capabilities.ts'

// 向上找带 package.json 的目录 = frontend 根。
// ⚠️ 原本写死 '../..' 各种级数，两次都算错（一次指向 frontend/src，一次 frontend/src/lib）
//    ⇒ 插件目录读成空 ⇒ 第 ② 条**恒真通过**。
//    是「量具自证」那条把它抓住的 —— 那正是它存在的用途。
// 改成自定位：移动文件也不会再错。
function findFrontendRoot(start) {
  let dir = start
  for (let i = 0; i < 10; i++) {
    if (existsSync(join(dir, 'package.json'))) return dir
    const up = dirname(dir)
    if (up === dir) break
    dir = up
  }
  throw new Error(`从 ${start} 向上找不到含 package.json 的目录（frontend 根）`)
}
const FRONTEND = findFrontendRoot(dirname(fileURLToPath(import.meta.url)))
const PLUGIN_DIR = join(FRONTEND, 'android/app/src/main/java/com/kaixuan/opencode/pocket/plugins')

/**
 * 「作者自陈这是占位」的标记词。
 *
 * ⚠️ 为什么用注释而不是「reject 次数 / import 面 / 文件行数」——这三个都试过，全部不可用：
 *   · reject 次数：`BackgroundMicPlugin` 3 个方法 7 处 reject（**权限/错误路径**），
 *     比 Sherpa 的 6 方法 5 reject 还高，但它是真的实现了。
 *   · import 面：`DocumentPlugin` 266 行、零第三方 import（用的是 android.* 的 PdfRenderer）；
 *     而 Sherpa 54 行看着就少。方向都是反的。
 *   · 行数：同上。
 * ⇒ 只有**类注释里的自陈**能稳定区分「作者说这是占位」与「作者实现了但有错误分支」。
 */
const PLACEHOLDER_MARKERS = ['占位', '骨架', 'TODO', 'not integrated', 'not implemented', 'stub']

/** 取 @CapacitorPlugin 之前的那段类 Javadoc（**不要剥注释** —— 要找的正是它）。 */
function classJavadoc(src) {
  const m = /\/\*\*([\s\S]*?)\*\/\s*@CapacitorPlugin/.exec(src)
  return m ? m[1] : ''
}

function pluginFiles() {
  if (!existsSync(PLUGIN_DIR)) return []
  return readdirSync(PLUGIN_DIR).filter((f) => f.endsWith('Plugin.java'))
}

/** 插件名（Sherpa）→ 文件名（SherpaPlugin.java）。 */
const fileFor = (name) => join(PLUGIN_DIR, `${name}Plugin.java`)

/**
 * 返回「类注释自陈占位、却没登记」的插件名。
 *
 * ⚠️ 第 ② 条与第 ④ 条（负控）**必须共用这一个函数** ——
 * 负控自己另写一套逻辑的话，它转红也证明不了正例在咬。
 */
function markedUnregistered(registry) {
  const bad = []
  for (const f of pluginFiles()) {
    const src = readFileSync(join(PLUGIN_DIR, f), 'utf8')
    if (!PLACEHOLDER_MARKERS.some((k) => classJavadoc(src).includes(k))) continue
    const name = f.replace(/Plugin\.java$/, '')
    if (!(name in registry)) bad.push(name)
  }
  return bad
}

test('量具自证：插件目录可读，且本仓确实存在「骨架」与「已实现」两类插件', () => {
  const files = pluginFiles()
  assert.ok(files.length >= 5, `插件目录只读到 ${files.length} 个文件，路径或剥离规则可能坏了`)

  const marked = files.filter((f) =>
    PLACEHOLDER_MARKERS.some((k) => classJavadoc(readFileSync(join(PLUGIN_DIR, f), 'utf8')).includes(k)),
  )
  const clean = files.filter((f) => !marked.includes(f))
  // 这一条是**前提**：若两类里有一类是空的，判据就分辨不出任何东西。
  assert.ok(marked.length >= 1, '没有任何插件被标成占位 ⇒ 标记词表已失效')
  assert.ok(clean.length >= 1, '所有插件都被标成占位 ⇒ 标记词表过宽，形同没有判据')
})

test('① 每个登记项：Java 文件必须存在，且指纹仍在（骨架尚未被真正实现）', () => {
  const names = Object.keys(NOT_IMPLEMENTED_PLUGINS)
  assert.ok(names.length >= 1, '登记表是空的 —— 那就没人守这条了')

  for (const name of names) {
    const entry = NOT_IMPLEMENTED_PLUGINS[name]
    const f = fileFor(name)
    assert.ok(existsSync(f), `${name} 登记为「已注册但未实现」，但找不到 ${f} ⇒ 登记项可能已过期`)
    const src = readFileSync(f, 'utf8')
    assert.ok(
      src.includes(entry.fingerprint),
      `${name} 的指纹 "${entry.fingerprint}" 已在 ${f} 里消失 ⇒ ` +
        `骨架很可能被真正实现了，请把它从 NOT_IMPLEMENTED_PLUGINS 移出去`,
    )
  }
})

test('② ★ 任何类注释自陈「占位/骨架」的插件，都必须登记在册（新骨架会被自动抓住）', () => {
  const bad = markedUnregistered(NOT_IMPLEMENTED_PLUGINS)
  assert.deepEqual(
    bad,
    [],
    '这些插件的类注释写了占位标记（骨架），但没登记在 NOT_IMPLEMENTED_PLUGINS 里 ⇒ ' +
      '一旦 MainActivity 注册了它们，能力探测就会把它们算成「可用」',
  )
})

test('③ 指纹不得出现在未登记的插件里（防止把理由安到别人头上）', () => {
  for (const f of pluginFiles()) {
    const src = readFileSync(join(PLUGIN_DIR, f), 'utf8')
    const name = f.replace(/Plugin\.java$/, '')
    for (const [owner, entry] of Object.entries(NOT_IMPLEMENTED_PLUGINS)) {
      if (owner === name) continue
      assert.ok(
        !src.includes(entry.fingerprint),
        `${f} 里出现了 ${owner} 的指纹 "${entry.fingerprint}" ⇒ 要么是复制粘贴，要么登记挂错了插件`,
      )
    }
  }
})

test('④ 负控：把登记去掉，同一条判据必须抓到 Sherpa（证明它真在咬）', () => {
  // 正例：现状应当是干净的
  assert.deepEqual(markedUnregistered(NOT_IMPLEMENTED_PLUGINS), [], '现状不该有漏登记的骨架')

  // 负控：抽掉 Sherpa 的登记后，同一条判据必须报出且只报出 Sherpa
  const stripped = {}
  for (const f of pluginFiles()) {
    const name = f.replace(/Plugin\.java$/, '')
    if (name === 'Sherpa') continue
    stripped[name] = NOT_IMPLEMENTED_PLUGINS[name]
  }
  const caught = markedUnregistered(stripped)
  assert.deepEqual(
    caught,
    ['Sherpa'],
    `抽掉 Sherpa 的登记后判据必须只报出 Sherpa，实际报出 ${JSON.stringify(caught)}`,
  )
})