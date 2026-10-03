/**
 * 通知列表「超长不可断串被静默裁切」回归测试。
 *
 * 缺陷背景（真机 Redmi 14R 5G 实测，2026-10-01）：
 *   /notifications 的 100 个 .ntf-body / .ntf-title 元素里有 **46 个**横向溢出，
 *   累计 **12,311px** 内容宽度被祖先的 `overflow-x: hidden` 静默裁掉。
 *   被裁掉的内容既不显示、也没有滚动条、也没有任何「已截断」提示——
 *   用户看到的是一段正常到看不出异常的正文，只是结尾永远缺一截。
 *
 *   触发源是邮件摘要里存了整段 MIME 原文（boundary 是一长串不可断 ASCII），
 *   数据侧已由 backend 的 DeriveSnippet 修掉。但那是**数据**修复：只要将来
 *   任何一条通知出现超长不可断串（长 URL、长 token、长 base64 cid），
 *   样式层不该再把内容静默裁掉。这是结构性的护栏，不是重复修同一个 bug。
 *
 *   真机对照（CDP 注入 before/after，同一批元素同一份数据）：
 *     现状                                → 溢出 46，被裁 12,311px
 *     注入 overflow-wrap:anywhere 后       → 溢出  0，被裁      0px
 *     移除样式（负控）                     → 溢出 46，被裁 12,311px
 *
 * 断言形式必须是**正向**的：要求规则存在且断词值不是 normal。
 * 这与 a8e67ae 那次教训一致——UA 默认样式这类缺陷靠「有没有显式规则」才抓得到，
 * 靠「有没有出现坏值」是抓不到的。
 *
 * Run: node --test src/features/notifications/__tests__/notif-wrap.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const VIEW = join(dirname(fileURLToPath(import.meta.url)), '..', 'NotificationsView.vue')
const src = readFileSync(VIEW, 'utf8')

/** 取 `<style>` 块文本（组件是单文件样式，取最后一段即可）。 */
const rawStyleBlock = (src.match(/<style[^>]*>([\s\S]*?)<\/style>/g) || []).join('\n')

/**
 * 解析前必须先剥掉 CSS 注释。踩过的坑：注释里写「真机实测 46 个溢出，累计被裁掉
 * 12,311px」——这个千分位逗号会被下面的「按逗号切分组选择器」当成选择器分隔符，
 * 于是 `.ntf-title` 被粘在注释尾巴上，护栏把自己的修复误判成缺失。
 * 注释里出现 `{` `}` 同样会打乱规则配对。
 */
const styleBlock = rawStyleBlock.replace(/\/\*[\s\S]*?\*\//g, '')

/**
 * 收集某个选择器上出现过的属性。
 *
 * 必须能处理**分组选择器**（`.ntf-title, .ntf-body { … }`）——第一版只按单选择器
 * 匹配，结果 .ntf-body 命中（它另有单条规则）而 .ntf-title 误报缺失，
 * 护栏差点把自己的合并规则判成缺陷。
 */
function declaredFor(selector) {
  const out = {}
  const ruleRe = /([^{}]+)\{([^{}]*)\}/g
  let rule
  while ((rule = ruleRe.exec(styleBlock)) !== null) {
    const selectors = rule[1]
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
    if (!selectors.includes(selector)) continue
    for (const decl of rule[2].split(';')) {
      const i = decl.indexOf(':')
      if (i < 0) continue
      out[decl.slice(0, i).trim()] = decl.slice(i + 1).trim()
    }
  }
  return out
}

describe('通知列表断词（防静默裁切）', () => {
  for (const sel of ['.ntf-body', '.ntf-title']) {
    it(`${sel} 必须显式声明 overflow-wrap，且不能是 normal`, () => {
      const decls = declaredFor(sel)
      const val = decls['overflow-wrap'] ?? decls['word-break'] ?? decls['word-wrap']

      assert.ok(
        val !== undefined,
        `${VIEW} 里 ${sel} 没有声明 overflow-wrap / word-break / word-wrap。` +
          `祖先 overflow-x:hidden 会把超长不可断串静默裁掉，用户看不到也滚不到。`,
      )
      assert.notEqual(
        val,
        'normal',
        `${sel} 的断词值是 normal，等于没有断词策略（真机实测 46/100 元素被裁 12,311px）`,
      )
      // anywhere 优先：break-word 在部分内核里仍不给无空格长串断点。
      if (decls['overflow-wrap']) {
        assert.equal(
          decls['overflow-wrap'],
          'anywhere',
          `${sel} 的 overflow-wrap 应为 anywhere（无空格长串也要能断），实际是 ${decls['overflow-wrap']}`,
        )
      }
    })
  }

  it('.ntf-main 必须保留 min-width: 0，否则 flex 子项不会收缩', () => {
    // min-width:auto 是 flex 子项的默认值，.ntf-body 撑不破它，overflow-wrap 也就无效。
    const decls = declaredFor('.ntf-main')
    assert.equal(
      decls['min-width'],
      '0',
      '.ntf-main 缺 min-width: 0 —— flex 子项默认 min-width:auto，' +
        '内部长串仍会把整项撑破，此时加 overflow-wrap 也没用',
    )
  })
})
