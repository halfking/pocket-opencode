/**
 * overlay-back-wiring.test.mjs — 「返回键要能先关弹窗」这条契约的**接线**卡口。
 *
 * ## 为什么单测不够
 *
 * `BackDispatcher` 的「覆盖层优先」逻辑**本来就有 12 条单测覆盖**，
 * 逻辑侧完全正确。缺的是**接线**：
 *   2026-10-06 设备实跑时查到 `registerOverlay()` **全仓零调用**（连测试都没调），
 *   而 `Dialog` / `BottomSheet` 都 `<Teleport to="body">`。
 *   ⇒ 硬件返回键会导航路由，而弹窗挂在 body 上**留在屏幕上**。
 *   而 `AppLayout.vue` 的注释正好写着它想避免这个（「避免…关了弹窗又跳路由」）。
 *
 * 失败形态与 `handler-wiring` 同一句：**编译通过、类型通过、gates 全绿、
 * 运行时也不报错 —— 只是那件事从来没发生过。**
 * ⚠️ 更要命的是它在**测试全绿**的情况下发生：测逻辑的用例测不到「有没有接上」。
 *
 * ## 本卡口的形状：钉**接线点**，不是钉行为
 *
 * 行为由设备矩阵验（开弹窗 → 硬件返回 → 弹窗关、路由不变）；
 * 这里守的是「接线别被拆掉」，成本极低且能进 CI。
 *
 * Run: node --test src/components/__tests__/overlay-back-wiring.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { blankComments } from '../../styles/__tests__/style-scan-utils.mjs'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const read = (rel) => readFileSync(join(SRC, rel), 'utf8')
/**
 * 剥注释后再判。
 *
 * ⚠️ 2026-10-06：接线点 1b 第一版直接对原文匹配 `getShellRuntime\(`，
 *   结果**红在自己身上** —— `useOverlayBack.ts` 的注释里正好写着
 *   「不能用 `getShellRuntime(router)` 兜底」，那是**禁令说明**，
 *   不是调用。与 `handler-wiring`「注释里的引用不算接线」是同一条纪律。
 *   ⇒ 判据自己先犯了自己写的规矩。
 */
const readCode = (rel) => blankComments(read(rel))

/** 必须登记到 BackDispatcher 的覆盖层组件（用 Teleport ⇒ 不会随父组件卸载而消失）。 */
const OVERLAY_COMPONENTS = [
  { rel: 'components/base/Dialog.vue', presentation: 'modal' },
  { rel: 'components/base/BottomSheet.vue', presentation: 'sheet' },
]

test('【接线点 1b】必须有一条**与组件树位置无关**的兜底取用（否则全局弹窗永远注入不到）', () => {
  // ⚠️ 2026-10-06 设备实跑直接打出来的洞：只验「AppLayout provide 了」是不够的。
  //   全局弹窗挂在 **App.vue**，是 `<AppLayout>` 的**兄弟节点** ——
  //   ```html
  //   <AppLayout> … </AppLayout>   <!-- 运行时在这里 provide -->
  //   <ConfirmDialog />            <!-- ← 在 provide 作用域之外 -->
  //   ```
  //   ⇒ `ConfirmDialog` 里的 `Dialog` 注入恒为 null，overlay 永不登记，
  //   硬件返回键照旧导航路由、弹窗留在屏幕上（第一版就是这样红的）。
  //
  //   ⇒ 必须有 `peekShellRuntime()` 这个**只读**兜底：
  //   拿不到就是 null、**永不创建**（用 getShellRuntime 兜底会造出第二个
  //   没有路由守卫的实例，返回仲裁形同虚设）。
  const txt = readCode('composables/useOverlayBack.ts')
  assert.match(txt, /peekShellRuntime\(\)/,
    'useOverlayBack 必须有 peekShellRuntime 兜底 —— 没有它，全局弹窗（挂在 App.vue，' +
    '不在 AppLayout 的 provide 作用域内）永远注入不到运行时')
  assert.ok(!/getShellRuntime\(/.test(txt),
    '不能拿 getShellRuntime(router) 兜底：它会**建**一个没有路由守卫的实例')
  // 兜底与 inject 要串在同一条取值链上，而不是二选一
  assert.match(txt, /inject\([^)]*\)[\s\S]{0,40}peekShellRuntime\(\)/,
    'inject 与 peekShellRuntime 必须串成「inject ?? peekShellRuntime」')
})

test('【接线点 1】AppLayout 必须 provide 出那个已经建好的运行时实例', () => {
  const app = read('app/AppLayout.vue')
  assert.match(app, /provide\(\s*SHELL_RUNTIME_KEY\s*,\s*shellRuntime\s*\)/,
    'AppLayout 没有 provide(SHELL_RUNTIME_KEY, shellRuntime) ⇒ 弹窗拿不到运行时。' +
    '不能改成弹窗自己 getShellRuntime(router)：那是进程内单例且必须传 router，' +
    '自己调会造出第二个没有路由守卫的实例，返回仲裁形同虚设。')
})

test('【接线点 2】每个 Teleport 覆盖层组件都必须登记到 BackDispatcher', () => {
  const missing = []
  for (const c of OVERLAY_COMPONENTS) {
    const txt = read(c.rel)
    if (!/useOverlayBack\s*\(/.test(txt)) { missing.push(`${c.rel} 没有调用 useOverlayBack(...)`); continue }
    if (!new RegExp(`presentation:\\s*'${c.presentation}'`).test(txt)) {
      missing.push(`${c.rel} 的 presentation 不是 '${c.presentation}'`)
    }
    if (!/useOverlayBack/.test(txt)) missing.push(`${c.rel} 没有 import useOverlayBack`)
  }
  assert.deepEqual(missing, [],
    '这些组件 Teleport 到 body ⇒ 父组件卸载时它们不会消失。' +
    '不登记到 BackDispatcher，硬件返回键就会导航路由、把弹窗留在屏幕上。')
})

test('【接线点 3】useOverlayBack 必须处理三种注销时机', () => {
  // 少任何一种都会留下「幽灵拦截」：一个已经看不见的弹窗仍然吞掉返回键。
  const txt = readCode('composables/useOverlayBack.ts')
  assert.match(txt, /onBeforeUnmount\(release\)/, '组件卸载时必须注销')
  assert.match(txt, /visible\.value/, 'visible 变 false 时必须注销')
  // 重新注册前先清旧的
  const regIdx = txt.indexOf('const register = () => {')
  const regBody = regIdx < 0 ? '' : txt.slice(regIdx, regIdx + 220)
  assert.match(regBody, /release\(\)/, 'register() 必须先 release()，否则 visible 抖动会叠出多条拦截')
})

test('【接线点 4】BackDispatcher.registerOverlay 必须真的有产品侧调用方', () => {
  // 这一条是**反向**自证：如果哪天有人把 useOverlayBack 删了，
  // 接线点 2 会红，但这条会给出「零调用方」这个更直接的读数。
  const callers = ['components/base/Dialog.vue', 'components/base/BottomSheet.vue']
    .filter((rel) => /useOverlayBack\s*\(/.test(read(rel)))
  assert.ok(callers.length >= 2,
    `只有 ${callers.length} 个组件登记了 overlay；registerOverlay 再次变成零调用 ⇒ ` +
    '「覆盖层优先」契约在产品里失效')
})

test('【量具自证】Teleport 前提仍然成立（否则整条卡口的前提消失）', () => {
  for (const c of OVERLAY_COMPONENTS) {
    assert.match(read(c.rel), /<Teleport\s+to="body"/,
      `${c.rel} 不再 Teleport 到 body ⇒ 它会随父组件卸载而消失，本卡口的判据前提改变，需重新评估`)
  }
})

test('【变异 · 必须转红】把 Dialog 的 useOverlayBack 调用删掉，接线点 2 必须报警', () => {
  const src = read('components/base/Dialog.vue')
  const mutated = src.replace(/useOverlayBack\s*\(\s*\{[\s\S]*?\}\s*\)/, '')
  assert.notEqual(mutated, src, '变异没生效：Dialog.vue 里已经没有 useOverlayBack 调用，本用例失去意义')
  const stillCalls = OVERLAY_COMPONENTS.filter(
    (c) => c.rel !== 'components/base/Dialog.vue' && /useOverlayBack\s*\(/.test(read(c.rel)),
  ).length
  const calls = mutated.match(/useOverlayBack\s*\(/g) || []
  assert.ok(calls.length + stillCalls < 2,
    '删掉一个组件的登记后，登记方数量应降到 1 以下 —— 这条判据没在数接线')
})

test('【变异 · 必须转红】把 AppLayout 的 provide 删掉，接线点 1 必须报警', () => {
  const src = read('app/AppLayout.vue')
  const mutated = src.replace(/provide\(\s*SHELL_RUNTIME_KEY[^)]*\)/, '')
  assert.notEqual(mutated, src, '变异没生效：AppLayout.vue 里已经没有 provide')
  assert.ok(!/provide\(\s*SHELL_RUNTIME_KEY\s*,\s*shellRuntime\s*\)/.test(mutated),
    '删掉 provide 后接线点 1 的判据必须失守')
})

test('【变异 · 必须转红】把 peekShellRuntime 兜底换成 getShellRuntime，1b 必须报警', () => {
  // 变异走**真实文件**的文本，判据也走同一套剥注释逻辑。
  const src = read('composables/useOverlayBack.ts')
  const mutated = src.replace('?? peekShellRuntime()', '?? getShellRuntime(null)')
  assert.notEqual(mutated, src,
    '变异没生效：useOverlayBack.ts 里已经没有 `?? peekShellRuntime()`，本用例失去意义')
  const code = blankComments(mutated)
  assert.ok(/getShellRuntime\(/.test(code),
    '注入的真实调用必须被剥注释后的判据看见 —— 否则 1b 永远绿，这门等于不存在')
})

test('【反证 · 必须保持绿】注释里出现 getShellRuntime(router) 不得触发 1b', () => {
  // 这一条是 1b 自己的自证。它在 2026-10-06 真的红过一次：
  // 文件里的禁令注释「不能用 getShellRuntime(router) 兜底」被判成了调用。
  // 若哪天有人把「剥注释」改回直接匹配原文，本条会红。
  const src = read('composables/useOverlayBack.ts')
  const code = blankComments(src)
  assert.ok(/getShellRuntime\(/.test(src),
    '前提没了：原文里已经没有 getShellRuntime( —— 注释里那句「不能用…」被删了，本用例失去意义')
  assert.ok(!/getShellRuntime\(/.test(code),
    '剥注释后不应看到任何 getShellRuntime( 调用')
  assert.match(code, /peekShellRuntime\(\)/,
    '同时要保证剥注释没把真实代码一起抹掉（blankComments 原地涂空白，长度不变）')
})
