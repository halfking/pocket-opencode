// silent-catch-must-explain-itself.test.mjs
//
// 「静默 catch」必须在同一块里写明为什么可以静默。
//
// 2026-10-02 普查：src 下 535 个 .ts/.vue 里共 119 处 catch 块剥掉注释后为空
// （空 catch 52 + 只含注释的 catch 67），其中 **118 处已带理由注释**，
// 只有 1 处（SettingsLLMGateway 的连通性测试后刷新配置）没有——
// 而那一处经查证确实是合理的（连通性已测成功，后续刷新失败不影响结论），
// 只是没把理由写下来。
//
// 所以本规则**不禁止空 catch**：仓库里 118 处都是有意为之（持久化、预取、
// TTS 缺失、引擎降级……），逐个改代码既无必要也有风险。
// 它只要求一件事：后来人读到这处静默时，能立刻知道「这是有意的还是漏的」。
// 一个没有理由的空 catch 与一个漏捕获的错误，在代码里长得一模一样。
//
// 防空跑用合成样本自检，而不是数真实命中：真实数量会随重构变化，
// 拿它当阈值会在「终于没有静默 catch 了」这个好状态下反而报红。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, extname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

// 本文件在 frontend/src/__tests__/，所以 ROOT 是 frontend（退两级），
// SRC 是 frontend/src。第一版写成退三级，直接扫到仓库根去，readdirSync ENOENT。
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const SRC = join(ROOT, 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

function collect(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collect(full, out)
    else if (['.ts', '.vue'].includes(extname(name))) out.push(full)
  }
  return out
}

const LINE = '//'
const BLOCK_OPEN = '/*'

/**
 * 剥掉注释（行注释 + 块注释）。
 *
 * 第一版写成 `.split(LINE).join(' ')` —— 那只把 `//` 这个**标记**换成空格，
 * 注释正文原封不动：`' // note '` 变成 `'  note '`，trim 后非空，
 * 于是「只含注释的静默 catch」被判成「不静默」，自检直接报 0 !== 1。
 * 行注释必须连到行尾一起删。
 *
 * 只匹配「行首（可含空白）的 //」，避免把字符串里的 `https://` 当注释起点。
 * 静默 catch 的块体只可能是整行注释或纯空白，这个范围够用。
 */
function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/^[^\S\n]*\/\/[^\n]*/gm, ' ')
}

/**
 * 找出「静默 catch」：块体在剥掉注释后为空。
 *
 * 块体用 `[^{}]*` 限定，所以带嵌套语句/对象字面量的 catch 不会被匹配——
 * 那类本来就不是静默的。
 */
function silentCatches(css) {
  const out = []
  for (const m of css.matchAll(/catch\s*(?:\([^)]*\))?\s*\{([^{}]*)\}/g)) {
    const rawBody = m[1]
    if (stripComments(rawBody).trim().length === 0) {
      out.push({
        at: m.index,
        explained: rawBody.includes(LINE) || rawBody.includes(BLOCK_OPEN),
      })
    }
  }
  return out
}

describe('静默 catch 必须自带理由', () => {
  it('检测器自检：认得出静默、认得出有解释的、认得出真干活的', () => {
    // 没有理由的空 catch —— 必须被抓到且标记为未解释
    const bare = silentCatches('try { a() } catch (e) {}')
    assert.equal(bare.length, 1)
    assert.equal(bare[0].explained, false, '空 catch 应被判为「无理由」')

    // 只含注释 —— 仍是静默，但已解释
    const withNote = silentCatches(`try { a() } catch (e) { ${LINE} 引擎缺失，静默 }`)
    assert.equal(withNote.length, 1)
    assert.equal(withNote[0].explained, true, '带注释的静默 catch 应被判为「有理由」')

    // 真正干活的 catch —— 不该被算成静默
    const real = silentCatches('try { a() } catch (e) { report(e); }')
    assert.equal(real.length, 0, '有语句的 catch 不算静默')

    // 可选 catch 绑定写法 try/catch {}
    const bare2 = silentCatches('try { a() } catch {}')
    assert.equal(bare2.length, 1)
    assert.equal(bare2[0].explained, false)
  })

  it('仓库里没有「无理由的静默 catch」', () => {
    const files = collect(SRC)
    assert.ok(files.length > 200, `只扫到 ${files.length} 个文件，扫描范围可能失效`)
    const bad = []
    for (const f of files) {
      const rel = relative(ROOT, f).replace(/\\/g, '/')
      for (const c of silentCatches(readFileSync(f, 'utf8'))) {
        if (!c.explained) bad.push(`${rel}:${lineOf(f, c.at)}`)
      }
    }
    assert.equal(
      bad.length,
      0,
      `发现 ${bad.length} 处「无理由的静默 catch」——\n` +
        `  它和有理由的静默在代码里长得一模一样，后来人无法区分「有意为之」与「漏捕获」：\n` +
        `  - ${bad.join('\n  - ')}`,
    )
  })
})

function lineOf(file, index) {
  const text = readFileSync(file, 'utf8')
  let n = 1
  for (let i = 0; i < index && i < text.length; i++) if (text[i] === '\n') n++
  return n
}
