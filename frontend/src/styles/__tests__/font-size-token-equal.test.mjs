// font-size-token-equal.test.mjs
//
// 锁住一条很窄但很值钱的不变式：
//
//   **凡是数值正好等于某个字号 token 的 font-size，不许写死像素。**
//
// 为什么单独守这一条
// ------------------
// 2026-10-03 普查发现全仓 1169 处 `font-size: Npx` 写死值。token 刻度当时是
// 10/12/14/15/16/18 六档，于是可以分成两堆：
//
//   · 673 处数值**正好**落在刻度上（10/12/14/15/16/18）——换成 var() 是
//     **零视觉变化**的纯机械替换，2026-10-03 全部转完了；
//   · 剩下的全是刻度外的值（11px 142 处、13px 206 处、20px 45 处……），
//     换 token 就**会改变观感**，那是需要人拍板的设计决策，不在本文件范围。
//
// 2026-10-02 补了 --text-2xs(11px) / --text-smd(13px) 两档，于是上表里
// 最大的两堆（11px 142 + 13px 206 = 348 处）也转成了「零视觉变化」的类别，
// 同一步替换完毕。**剩下的刻度外值是 20px(45) 等**，那些仍然不该被本判据管。
//
// ⚠️ 本判据现读 tokens.css，所以**任何新增字号刻度都会立刻让对应的写死像素
// 变成违规**。补刻度与做替换必须同一步落地，不能只补刻度——实测只补刻度会
// 让本文件转红并报出全部 348 处。同理，负控里「刻度外的值」必须选一个**将来
// 也不会被补进刻度**的数：这里原本写的是 11px，补刻度后它当场失效（该负控
// 反而转红，1 !== 0），已改为 20px。
//
// 所以这里守的不是「所有 font-size 都得用 token」（那会逼着人把 20px 悄悄
// 改成 18px），而是「**不该绕开 token 的那部分不许再绕开**」。
//
// 为什么值得守：写死像素的真正代价不是难看，而是**将来整体调档时它们不会跟着
// 变**。token 刻度一改，正文全变了、这些角标和徽章纹丝不动，于是「层级」越
// 调越乱——而这正是用户报的「字体不对」在长期尺度上的成因。
//
// 负控见文件末尾，实测转红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
// HERE = frontend/src/styles/__tests__ → 上溯 2 级 = frontend/src
// （第一版写了 3 级，去到了 frontend/，报出来的却是 ENOENT「文件不存在」——
//  路径算错时的错误信息长得像源码缺失，很容易被误判成别的问题。）
const SRC = path.resolve(HERE, '..', '..')

/**
 * 把注释换成等长空格（长度必须不变——判据要按偏移回原文取上下文）。
 * 不剥注释的话，「在注释里解释旧写法」会被误判成违规，久了就没人敢写注释。
 */
export function blankComments(src) {
  const out = src.split('')
  let i = 0
  while (i < src.length) {
    if (src.startsWith('/*', i)) {
      const end = src.indexOf('*/', i + 2)
      const stop = end < 0 ? src.length : end + 2
      for (let k = i; k < stop; k++) if (out[k] !== '\n') out[k] = ' '
      i = stop
    } else if (src.startsWith('//', i)) {
      let end = src.indexOf('\n', i)
      if (end < 0) end = src.length
      for (let k = i; k < end; k++) if (out[k] !== '\n') out[k] = ' '
      i = end
    } else {
      i++
    }
  }
  return out.join('')
}

function walk(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) {
      if (e.name === 'node_modules' || e.name.startsWith('.')) continue
      walk(p, acc)
    } else if (e.name.endsWith('.vue') || e.name.endsWith('.css')) {
      acc.push(p)
    }
  }
  return acc
}

/** token 名 → 像素值（从 tokens.css 现读，不写死刻度）。 */
export function loadTokenScale(tokensCss) {
  const scale = {}
  for (const m of tokensCss.matchAll(/(--text-[a-z0-9-]+)\s*:\s*(\d+(?:\.\d+)?)px/g)) {
    scale[m[2]] = m[1]
  }
  return scale
}

/**
 * 找出「数值等于某个 token 却仍写死像素」的 font-size。
 * @returns {Array<{file:string,line:number,text:string}>}
 *
 * 注意 `(?!\s*(?:\/|!))`：px 后面紧跟 `/`（如 `font: 12px/1.5` 的简写残留）
 * 或 `!important` 的 ! 时，那不是本判据要管的独立 font-size 声明。
 */
export function findTokenEqualRawPx(files, scale) {
  return scanFiles(files, scale)
}

function scanFiles(fileObjs, scale) {
  const out = []
  const re = /font-size\s*:\s*(\d+(?:\.\d+)?)px(?!\s*(?:\/|!))/g
  for (const f of fileObjs) {
    const raw = typeof f === 'string' ? fs.readFileSync(f, 'utf8') : f.readFileSync()
    const masked = blankComments(raw)
    if (typeof f === 'string' && masked.length !== raw.length) {
      throw new Error('blankComments 必须保持长度不变')
    }
    for (const m of masked.matchAll(re)) {
      if (!scale[m[1]]) continue
      const idx = m.index + m[0].length - `${m[1]}px`.length
      out.push({
        file: typeof f === 'string' ? f : 'fake',
        line: raw.slice(0, idx).split('\n').length,
        text: raw.slice(idx, idx + `${m[1]}px`.length),
      })
    }
  }
  return out
}

const files = walk(SRC)
const scale = loadTokenScale(fs.readFileSync(path.join(SRC, 'styles', 'tokens.css'), 'utf8'))

describe('字号等于 token 值时必须走 token', () => {
  it('扫描范围本身有效（防空跑：集合为空就绿）', () => {
    assert.ok(files.length >= 100, `只扫到 ${files.length} 个样式文件，路径可能不对`)
    assert.ok(Object.keys(scale).length >= 6, `只从 tokens.css 解析出 ${Object.keys(scale).length} 个字号 token`)
  })

  it('检测器认得出「该转没转」的写法（自检，防永远绿）', () => {
    const fake = [{ readFileSync: () => 'a { font-size: 12px; }\nb { font-size: 18px; }\nc { font-size: var(--text-sm); }' }]
    const hits = findTokenEqualRawPxWith(fake, scale)
    assert.deepEqual(hits.map((h) => h.text), ['12px', '18px'], '检测器认不出写死像素的写法')
    assert.deepEqual(hits.map((h) => h.line), [1, 2], '行号算错了，报错信息会指错位置')
  })

  it('检测器不会被注释里的旧写法骗到', () => {
    const fake = [{ readFileSync: () => '/* 此前是 font-size: 12px */\na { font-size: var(--text-sm); }' }]
    assert.deepEqual(findTokenEqualRawPxWith(fake, scale), [])
  })

  it('仓库里没有「数值等于 token 却仍写死像素」的 font-size', () => {
    const hits = findTokenEqualRawPx(files, scale)
    // 注意查表键：scale 是按**不带 px** 的数值建的（'12' → '--text-sm'），
    // 而 hits[].text 带 px（'12px'）。第一版直接 scale[h.text] 取值，
    // 报错信息里全是 `var(undefined)`——护栏红了，但红得没有信息量。
    const suggestion = (h) => `var(${scale[h.text.replace(/px$/, '')]})`
    assert.equal(
      hits.length,
      0,
      `${hits.length} 处 font-size 数值与某个字号 token 相同却仍写死像素（换 token 是零视觉变化）：\n` +
        hits.slice(0, 20)
          .map((h) => `  ${path.relative(SRC, h.file).replace(/\\/g, '/')}:${h.line}  ${h.text}  → 应为 ${suggestion(h)}`)
          .join('\n'),
    )
  })
})

describe('判据自检：负控必须转红', () => {
  it('把一处 12px 塞回去 → 必须被抓出来', () => {
    const fake = [{ readFileSync: () => '.a { font-size: 12px; }' }]
    const hits = findTokenEqualRawPxWith(fake, scale)
    assert.equal(hits.length, 1, '负控本该转红却判成了通过——检测器坏了')
    assert.equal(hits[0].text, '12px')
  })

  it('塞一个刻度外的值（20px）不算违规', () => {
    const fake = [{ readFileSync: () => '.a { font-size: 20px; }' }]
    assert.equal(
      findTokenEqualRawPxWith(fake, scale).length,
      0,
      '20px 不在刻度上，换 token 会改变观感——本判据不该管它',
    )
  })

  it('塞进注释里的 12px 不算违规', () => {
    const fake = [{ readFileSync: () => '/* 旧写法 font-size: 12px */\n.a { font-size: var(--text-sm); }' }]
    assert.equal(findTokenEqualRawPxWith(fake, scale).length, 0)
  })

  it('走 token 的写法当然不算违规', () => {
    const fake = [{ readFileSync: () => '.a { font-size: var(--text-sm); }' }]
    assert.equal(findTokenEqualRawPxWith(fake, scale).length, 0)
  })
})

/** 给伪文件对象用的小包装（省得为负控去碰真实磁盘）。 */
function findTokenEqualRawPxWith(fileObjs, scale) {
  return scanFiles(fileObjs, scale)
}
