// 从 npm 的完整 material-symbols-outlined.woff2 里,只保留工程实际用到的图标。
// 修复「设置页 LIGHT_MODE / DARK 显示为连字原文」bug——此前固定种子名单漏了
// light_mode / dark_mode / brightness_auto 等新加入的图标。真机 redmi 上证据
// 充分:SettingsView.vue 主题三选项里 "<span class=\"material-symbols-outlined\">light_mode</span>"
// 直接显示为 "LIGHT_MODE" 文本。
//
// Output: src/assets/fonts/material-symbols-outlined.woff2
// Output: dist/assets/*.woff2 by vite build pipeline.
//
// ## 体积的实话（别再被文件头的旧注释误导）
//
// 实测：原始 3.80 MB → 产物 3.52 MB，**只削掉 7.3%**。
// 原因：material-symbols 的连字由基础字形合成，121 个图标名覆盖了 a–z、0–9 和
// 常用标点，harfbuzz 的 layout closure 会把它们全部拉进来，基座本身几乎削不动。
// 所以这个脚本的价值**不是减体积，而是控制「哪些连字存在」**——这才是那个
// LIGHT_MODE 豆腐块 bug 的本质。文件头曾写「≈4-12 KB」，与实际差约 300 倍，已更正。
//
// 真正的体积优化要换思路（例如改用 SVG 图标或按页面分包），不在本脚本范围内。

import { readFile, writeFile, readdir } from 'node:fs/promises'
import { readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
// subset-font 在 ESM 下导出的是默认函数,CommonJS 兼容
import subsetFontDefault from 'subset-font'
const subset = subsetFontDefault.default || subsetFontDefault

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const ROOT = join(__dirname, '..', '..')  // ../../ from frontend/scripts
const SRC_DIR = join(ROOT, 'frontend', 'src')
const FONT_PATH = join(
  ROOT,
  'frontend',
  'node_modules',
  'material-symbols',
  'material-symbols-outlined.woff2',
)
// POCKET_ICON_FONT_OUT 可把产物写到别处。用于「改完注册表 → 重建到临时文件 →
// 用 check-icon-font.mjs 对新字体跑门禁」的验证路径，避免每次试验都去覆盖
// 那个 3.5 MB 的已跟踪产物。不设则照旧写回仓库。
const OUT_PATH = process.env.POCKET_ICON_FONT_OUT
  ? join(ROOT, 'frontend', process.env.POCKET_ICON_FONT_OUT)
  : join(ROOT, 'frontend', 'src', 'assets', 'fonts', 'material-symbols-outlined.woff2')

// 1. 从所有 .vue / .ts / .css 里抓 icon 名字(模板里的字符串,直接是 ligature 名字)。
// 2. 也要抓插值表达式里的字面量名：`<span class="material-symbols-outlined">
//    {{ ok ? 'play_arrow' : 'pause' }}</span>`。这类名字同样要渲染成字形，
//    只扫第一类会让它们在真机上显示成连字原文。P1 遗留问题，19 处命中。
// 3. 上面两条原理上抓不到「运行时才决定的动态名」——数据表 `icon: 'x'`、
//    computed 早返回、map 表、跨行插值。这部分由 src/constants/icons.ts 的
//    ICON 注册表统一声明，见下方 readRegistry()。**不要再往下面的 FALLBACK 里
//    手工追加动态图标名**，那是两份会漂移的清单。
const ICON_RE = /material-symbols-outlined"[^>]*>([a-z_]+)</g
// 只在同一行里、出现在 material-symbols-outlined 之后的插值里找引号字符串，
// 避免把整个文件的普通字符串都当成图标。
// 注意：这里用 `([^}]*)\}\}` 锚定而不是 `.*$` —— 仓库文件是 CRLF，`.` 匹配不到
// `\r`，无 /m 的 `$` 又只认字符串末尾，两者叠加会让这条规则在 Windows 上整条失效。
const DYNAMIC_ICON_LINE_RE = /material-symbols-outlined"[^>]*>\s*\{\{([^}]*)\}\}/
const QUOTED_NAME_RE = /'([a-z_]{3,})'/g

const REGISTRY_PATH = join(SRC_DIR, 'constants', 'icons.ts')

/**
 * 读 ICON 注册表。动态引用的图标名只有这一个来源。
 * 用正则解析而不是 import：构建脚本要在纯 node 下跑，不想引入 TS 编译链。
 * 解析失败一律 fail-closed（退出码 2），绝不返回空列表——空列表会静默地把
 * 所有动态图标裁掉，重建出一个满屏豆腐块的字体。
 */
function readRegistry() {
  let src
  try {
    src = readFileSync(REGISTRY_PATH, 'utf8')
  } catch {
    console.error(`[subset] 读不到图标注册表 ${REGISTRY_PATH}`)
    process.exit(2)
  }
  const block = /export const ICON = \{([\s\S]*?)\n\} as const/.exec(src)
  if (!block) {
    console.error('[subset] 无法从 constants/icons.ts 解析 ICON 注册表')
    process.exit(2)
  }
  const names = [...block[1].matchAll(/:\s*'([a-z_]+)'/g)].map((m) => m[1])
  if (names.length < 20) {
    console.error(`[subset] ICON 注册表解析异常，只得到 ${names.length} 个名字`)
    process.exit(2)
  }
  return names
}

async function collectIcons() {
  const out = new Set()
  const stack = [SRC_DIR]
  while (stack.length) {
    const dir = stack.pop()
    const items = await readdir(dir, { withFileTypes: true })
    for (const item of items) {
      const p = join(dir, item.name)
      if (item.isDirectory()) {
        if (item.name === 'node_modules' || item.name.startsWith('.')) continue
        stack.push(p)
      } else if (/\.(vue|ts|css|js|mjs)$/.test(item.name)) {
        const txt = await readFile(p, 'utf8')
        const re = new RegExp(ICON_RE.source, 'g')
        let m
        while ((m = re.exec(txt)) !== null) {
          out.add(m[1])
        }
        // 插值式图标：只处理 material-symbols-outlined 与 {{ 同处一行的场景。
        for (const line of txt.split('\n')) {
          const d = DYNAMIC_ICON_LINE_RE.exec(line)
          if (!d) continue
          const q = new RegExp(QUOTED_NAME_RE.source, 'g')
          let qm
          while ((qm = q.exec(d[1])) !== null) {
            out.add(qm[1])
          }
        }
      }
    }
  }
  // 兜底:即使代码里没用过,这些图标也得保证能渲染——很多页面是动态主题或后续会加。
  // 注意:这里只放「连字层面的通用兜底」,不再放动态图标名——那些由 ICON 注册表负责。
  const FALLBACK = [
    'arrow_back', 'arrow_forward', 'arrow_downward', 'arrow_upward',
    'check', 'close', 'add', 'remove', 'delete', 'edit', 'refresh', 'sync',
    'home', 'menu', 'more_vert', 'settings', 'search', 'filter_list',
    'keyboard_arrow_down', 'keyboard_arrow_right', 'keyboard_arrow_up',
    'star', 'favorite', 'bookmark',
    'notifications', 'mail', 'send', 'mic', 'photo_camera',
    'expand_more', 'expand_less',
    'check_circle', 'error', 'warning', 'info',
    'chevron_right', 'chevron_left',

    // ---- 动态图标：权威名单在 src/constants/icons.ts ----
    // 历史上这里是手工维护的,而 `icon: 'x'` 这种「图标从模板字面量搬进数据表」
    // 的写法正是 light_mode / dark_mode / brightness_auto 丢字的根因。
    // 现在统一由 readRegistry() 读入,不要再往这里追加——
    // 追加会出现两份清单,漂移时构建以为有、门禁以为没有。
  ]
  for (const f of FALLBACK) out.add(f)

  const registry = readRegistry()
  for (const r of registry) out.add(r)
  console.log(`[subset] ICON 注册表并入 ${registry.length} 个动态图标名`)
  return [...out].sort()
}

async function main() {
  const icons = await collectIcons()
  // --list：只打印最终图标清单就退出，不做子集化。
  // 这是排查「图标写了但字体里没有」的唯一可靠入口 —— 收集规则有两条正则，
  // 跨行写法、动态表达式形态都可能静默漏扫，肉眼扫源码看不出来。
  if (process.argv.includes('--list')) {
    console.log(icons.join('\n'))
    return
  }
  console.log(`[subset] 工程用到 + 兜底共 ${icons.length} 个图标`)
  // harfbuzz-subset 需要一个 text string 来确定要保留哪些字形(material-symbols
  // 的 ligatures 是按名合成,我们用空格连起让 subset-font 把所有 ligature
  // 字形都加入)。
  const text = `material symbols ${icons.join(' ')} `
  console.log(`[subset] 原字体 ${(await readFile(FONT_PATH)).length / 1024} KB → 子集化中...`)
  const font = await readFile(FONT_PATH)
  const start = Date.now()
  const out = await subset(font, text, {
    targetFormat: 'woff2',
  })
  await writeFile(OUT_PATH, out)
  console.log(
    `[subset] 完成 ${icons.length} 图标, ${(out.length / 1024).toFixed(1)} KB in ${Date.now() - start} ms → ${relative(ROOT, OUT_PATH)}`,
  )
}

main().catch((err) => {
  console.error('[subset] FAILED:', err)
  process.exit(1)
})
