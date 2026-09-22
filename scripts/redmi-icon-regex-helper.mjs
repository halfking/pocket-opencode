// 从 npm 的完整 material-symbols-outlined.woff2 里,只保留工程实际用到的图标。
// 修复「设置页 LIGHT_MODE / DARK 显示为连字原文」bug——此前固定种子名单漏了
// light_mode / dark_mode / brightness_auto 等新加入的图标。真机 redmi 上证据
// 充分:SettingsView.vue 主题三选项里 "<span class=\"material-symbols-outlined\">light_mode</span>"
// 直接显示为 "LIGHT_MODE" 文本。
//
// Output: src/assets/fonts/material-symbols-outlined.woff2 (≈4-12 KB)
// Output: dist/assets/*.woff2 by vite build pipeline.

import { readFile, writeFile, readdir } from 'node:fs/promises'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { subset } from 'subset-font'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const ROOT = join(__dirname, '..')
const SRC_DIR = join(ROOT, 'frontend', 'src')
const FONT_PATH = join(
  ROOT,
  'frontend',
  'node_modules',
  'material-symbols',
  'material-symbols-outlined.woff2',
)
const OUT_PATH = join(ROOT, 'frontend', 'src', 'assets', 'fonts', 'material-symbols-outlined.woff2')

// 1. 从所有 .vue / .ts / .css 里抓 icon 名字(模板里的字符串,直接是 ligature 名字)。
const ICON_RE = /material-symbols-outlined"[^>]*>([a-z_]+)</g

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
      }
    }
  }
  // 兜底:即使代码里没用过,这些图标也得保证能渲染——很多页面是动态主题或后续会加。
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
  ]
  for (const f of FALLBACK) out.add(f)
  return [...out].sort()
}

async function main() {
  const icons = await collectIcons()
  console.log(`[subset] 工程用到 + 兜底共 ${icons.length} 个图标`)
  console.log(`[subset] 原字体 ${(await readFile(FONT_PATH)).length / 1024} KB → 子集化中...`)
  const font = await readFile(FONT_PATH)
  const start = Date.now()
  const out = await subset(font, icons, {
    targetFormat: 'woff2',
    layoutFeatures: ['liga'],
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
