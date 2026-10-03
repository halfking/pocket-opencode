// settings-helper-text-tier.test.mjs
//
// 设置页「表单说明文字」必须与全站小字档一致，且不得使用极小档 token。
//
// 2026-10-02 普查设置页 4 个 .vue 的 55 条带 font-size 规则时发现，
// 同一个语义角色在设置页内部就有两个值：
//
//   SettingsPermissionsView  .hint       var(--text-xs) = 10px   （2 处）
//   SettingsSTT              .form-hint  12px                     （8 处）
//   SettingsLLMGateway       .form-hint  12px                     （6 处）
//
// 方向不是随意选的：tokens.css 给 --text-xs 的注释写明用途是
// 「极小文本（时间戳、徽章）」，而被套用的 `.hint` 是两行中文说明段落。
// 10px 的中文在手机上明显偏小 —— 这正是用户报的「设置页字体不对、显示错误」。
// 故以**多数 + token 自述用途**两条证据定方向：说明文字归 --text-sm（12px）。
//
// 判据只覆盖这一类角色，不普查全站字号：全站 11px(143 处)/13px(206 处)
// 都不在 token 刻度上，全面收敛会改动上千处观感，属另议事项。
//
// 负控（见文件末尾）：把 --text-sm 改回 --text-xs，本文件必须转红。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
// 本文件位于 settings/__tests__/，所以要往上一级才是设置页目录。
// 第一版写成 settingsDir = here，于是 readdirSync 扫的是 __tests__（没有 .vue），
// collect() 恒返回 0 条 —— 而「0 命中」恰好让"不得使用 --text-xs"那条**空跑通过**。
// 只有防空跑断言把它红着报出来。
const settingsDir = join(here, '..')

/** 说明文字角色的选择器白名单。 */
const HELPER_SELECTORS = ['.hint', '.form-hint']

function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ')
}

/** 收集设置页里每个说明文字选择器的 font-size 声明。 */
function collect() {
  const out = []
  for (const name of readdirSync(settingsDir)) {
    if (!name.endsWith('.vue')) continue
    const css = stripComments(readFileSync(join(settingsDir, name), 'utf8'))
    // 直接按选择器精确匹配即可。
    // 第一版在这里加过一个「选择器是否存在」的预检正则，条件是
    // `(?:^|[,{\s])hint\s*(?=[,{\s])` —— 忘了类选择器前面的 `.` 也不在那个
    // 字符集里，于是预检恒假、collect() 返回 0 条。
    // 后果是**两条断言里一条空跑通过**（0 命中恰好等于 0 违规），
    // 只剩防空跑那条红着报警。预检没有增加任何能力，直接删掉。
    for (const m of css.matchAll(/([.#][A-Za-z0-9_\-.]*)\s*\{([^}]*)\}/g)) {
      if (!HELPER_SELECTORS.includes(m[1])) continue
      const fs = m[2].match(/font-size\s*:\s*([^;]+);/)
      if (fs) out.push({ file: name, sel: m[1], size: fs[1].trim() })
    }
  }
  return out
}

const found = collect()

describe('设置页说明文字字号', () => {
  it('防空跑：真的扫到了设置页的说明文字规则', () => {
    assert.ok(
      found.length >= 3,
      `只扫到 ${found.length} 处说明文字规则（${JSON.stringify(found)}），路径或选择器可能不对`,
    )
    const files = new Set(found.map((f) => f.file))
    assert.ok(files.size >= 2, `只扫到 ${files.size} 个文件，判据可能失明`)
  })

  it('不得使用 --text-xs（该档 token 自述用途是时间戳与徽章）', () => {
    const bad = found.filter((f) => f.size.includes('--text-xs'))
    assert.equal(
      bad.length,
      0,
      `说明文字用了极小档 token：\n${bad.map((b) => `  ${b.file} ${b.sel} → ${b.size}`).join('\n')}`,
    )
  })

  it('三个设置页的说明文字落在同一档（--text-sm）', () => {
    const sizes = [...new Set(found.map((f) => f.size))]
    assert.equal(
      sizes.length,
      1,
      `说明文字出现 ${sizes.length} 个不同值 ${sizes.join(' / ')}：\n` +
        found.map((f) => `  ${f.file} ${f.sel} → ${f.size}`).join('\n'),
    )
    assert.equal(sizes[0], 'var(--text-sm)', `说明文字应是 var(--text-sm)，实际 ${sizes[0]}`)
  })
})

// 负控：把 SettingsPermissionsView 的 .hint 改回 var(--text-xs)，
// 后两条必须同时转红。
