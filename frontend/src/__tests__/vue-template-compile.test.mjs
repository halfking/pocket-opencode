/**
 * 全量 .vue 模板可编译性回归测试。
 *
 * 缺陷背景（2026-10-01，main 上真实发生）：
 *   `src/features/email/EmailInboxView.vue` 里
 *     <span v-if="pageState.loadingMore" …>            ← v-if 在 <Transition> **外面**
 *     <Transition name="morefade" mode="out-in">
 *       <span v-else-if="pageState.hasMore" …>          ← v-else-if 在里面
 *       <span v-else …>                                 ← v-else 也在里面
 *     </Transition>
 *   `v-else-if` 找不到相邻的 `v-if` 兄弟，@vue/compiler-sfc 直接抛
 *   “v-else/v-else-if has no adjacent v-if or v-else-if”，
 *   **`vite build` 整体失败** —— 连带 APK 构建不出来，真机验证全部卡死。
 *
 * 为什么必须单独设护栏：这类错误不会被任何 *.test.mjs 现有用例碰到（它们大多只
 * 扫源码文本或跑纯函数），也不会被 vue-tsc 报出（模板结构合法性不归它管），
 * 只有真正跑一遍 SFC 编译器才知道。而跑 `vite build` 又太重（十几秒、还要 env），
 * 不会有人每改一个模板就跑一次。
 *
 * 这里直接用 @vue/compiler-sfc 逐个 parse + compileTemplate：单个文件毫秒级，
 * 能把「模板根本编译不过」这一类缺陷挡在测试里。
 *
 * Run: node --test src/__tests__/vue-template-compile.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse as parseSFC, compileTemplate } from '@vue/compiler-sfc'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const SRC = join(ROOT, 'src')
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

function collectVue(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collectVue(full, out)
    else if (extname(name) === '.vue') out.push(full)
  }
  return out
}

const files = collectVue(SRC)

describe('全量 .vue 模板可编译', () => {
  it('至少扫到一批 .vue（防止 collectVue 路径写错而空转成绿灯）', () => {
    assert.ok(
      files.length > 50,
      `只扫到 ${files.length} 个 .vue，路径八成写错了，护栏会空转`,
    )
  })

  for (const file of files) {
    const rel = file.slice(ROOT.length + 1)
    it(`${rel} 模板编译无错`, () => {
      const source = readFileSync(file, 'utf8')
      const { descriptor, errors } = parseSFC(source, { filename: file })
      // parse 阶段的错（未闭合标签、非法 SFC 结构）也算失败
      assert.deepEqual(
        errors.map((e) => e.message),
        [],
        `${rel} SFC 解析失败`,
      )
      if (!descriptor.template) return // 无模板的纯 ts/css 组件，跳过

      const res = compileTemplate({
        source: descriptor.template.content,
        filename: file,
        id: 'guard',
      })
      assert.deepEqual(
        res.errors.map((e) => (typeof e === 'string' ? e : e.message)),
        [],
        `${rel} 模板编译失败（这类错误会让 vite build 整体挂掉）`,
      )
    })
  }
})
