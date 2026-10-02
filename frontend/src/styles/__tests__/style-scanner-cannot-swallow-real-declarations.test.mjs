// style-scanner-cannot-swallow-real-declarations.test.mjs
//
// 锁住一条元性质：**剥注释这个动作本身不许吞掉真实声明。**
//
// 为什么值得单独守
// ----------------
// 2026-10-03 复查字号护栏时发现：font-size-token-equal.test.mjs 一直绿，
// 而独立普查报出 NoteEditView.vue 里还有 2 处 font-size: 12px（正好等于
// --text-sm，是那条护栏该抓的违规）。根因是它的 blankComments()：
//
//   模板里 accept="video/*" 的 /* 被当成块注释起点，一路吞到下一个 */，
//   该文件后面 7658 个字符的真实 CSS 全被抹成空格。
//
// **方向是 fail-open**：判据扫不到 → 集合为空 → 绿。这类盲区不会自己出声，
// 不会让任何测试变红，所以只能靠一条独立判据显式钉住。
//
// 这条护栏与 font-size-token-equal 是**互相独立**的两条：
//   · 那一条管「字号等于 token 值就不许写死像素」；
//   · 这一条管「无论守什么，扫描器都不许因为剥注释而漏扫」——
//     也就是说，它对**未来任何**复用 blankComments 的护栏都生效。
//
// 负控见文件末尾，实测转红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..')

const FONT_SIZE_DECL = /font-size\s*:\s*\d+(?:\.\d+)?px/g

// 从工具模块取，不从护栏取：import 一个 .test.mjs 会把它的顶层
// describe/it 注册进当前进程，两边互相取消（实测满屏 "test did not finish
// before its parent and was cancelled"）。第一版就是这么写的，症状看着像判据坏了。
//
// 用静态 import 而不是顶层 await import()：顶层 await 把整个文件的求值推迟到
// 微任务之后，node:test 的顶层 suite 收集先跑完了，于是第一条用例拿到
// undefined——同样是看着像判据坏、实则是时序。
import { blankComments, walkStyleFiles } from './style-scan-utils.mjs'

describe('剥注释不许吞掉真实声明', () => {
  it('判据自身有效：blankComments 能取到，且它确实会改写内容（防空跑）', () => {
    assert.equal(typeof blankComments, 'function', '工具模块没有导出 blankComments，本判据无从检验')
    // 断言「注释内容被抹掉」，**不是**断言整串不含 font-size——
    // 第一版写成 !masked.includes('font-size')，而 font-size 恰恰在注释**之后**，
    // 是必须留下的真实声明，于是这条防空跑断言自己转红。
    // 负控样本自身写错时，症状与判据坏了完全一样：都必须先怀疑样本。
    const src = '/* 旧写法 font-size: 12px */\n.a { font-size: 12px; }'
    const masked = blankComments(src)
    assert.notEqual(masked, src, 'blankComments 对这段什么都没做，判据是空转的')
    assert.ok(!masked.includes('旧写法'), '真注释没被剥掉，判据方向反了')
    assert.ok(masked.includes('.a { font-size: 12px; }'), '真实声明被连带抹掉了')
  })

  it('模板属性值里的 video/* 不会被当成块注释起点', () => {
    const src = '<input accept="video/*" />\n.a { font-size: 12px; }\n'
    const masked = blankComments(src)
    assert.equal(masked.length, src.length, '必须保持长度不变（判据按偏移回原文取上下文）')
    assert.ok(
      masked.includes('font-size: 12px'),
      'video/* 之后的真实声明被当成注释吞了——这正是 NoteEditView.vue 上发生过的盲区',
    )
  })

  it('全仓没有任何样式文件因剥注释而少扫到 font-size 声明', () => {
    const files = walkStyleFiles(SRC)
    assert.ok(files.length >= 100, `只扫到 ${files.length} 个样式文件，路径可能不对`)

    const lost = []
    for (const f of files) {
      const raw = fs.readFileSync(f, 'utf8')
      const masked = blankComments(raw)
      if (masked.length !== raw.length) {
        lost.push(`${path.relative(SRC, f).replace(/\\/g, '/')}（blankComments 改了长度）`)
        continue
      }
      const before = (raw.match(FONT_SIZE_DECL) || []).length
      const after = (masked.match(FONT_SIZE_DECL) || []).length
      if (before !== after) {
        lost.push(`${path.relative(SRC, f).replace(/\\/g, '/')}（抹掉 ${before - after} 处）`)
      }
    }
    assert.deepEqual(lost, [], `这些文件的 font-size 声明被剥注释误吞了：\n  ${lost.join('\n  ')}`)
  })
})

describe('判据自检：负控必须转红', () => {
  it('把守卫换回「无字符串感知」的旧实现 → 普查判据必须报出误吞', () => {
    // 不能直接改磁盘上的实现（那会污染工作区），所以在这里构造一个等价于
    // 旧实现的替身，喂同一份内容，看普查判据会不会开口。
    const legacy = (src) => {
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
        } else i++
      }
      return out.join('')
    }

    // ⚠️ 负控样本必须是**合成**的，不能拿真实文件当样本。
    // 第一版直接读 NoteEditView.vue，结果样本随修复一起失效了：
    // 那 2 处 12px 换成 var(--text-sm) 之后，文件里再无写死像素声明，
    // 旧实现吞掉 0 处，负控报 "before=0 after=0" 而转红——
    // **一个因为 bug 修好了而失败的负控，证明不了判据好坏，只证明样本会过期。**
    const sample = [
      '<input accept="video/*" class="hidden-file" />',
      '<style>',
      '.extract-btn { padding: 8px 12px; font-size: 12px; }',
      '.media-hint { margin: 4px 0; font-size: 12px; }',
      '</style>',
    ].join('\n')

    const before = (sample.match(FONT_SIZE_DECL) || []).length
    const after = (legacy(sample).match(FONT_SIZE_DECL) || []).length
    assert.equal(before, 2, '合成样本本身写错了，负控无从谈起')
    assert.equal(after, 0, '负控本该报出旧实现吞掉 2 处，却一处没吞——判据坏了')

    // 同一份样本交给修好的实现，必须一处不丢。
    assert.equal(
      (blankComments(sample).match(FONT_SIZE_DECL) || []).length,
      before,
      '修好的实现仍漏扫——负控只是碰巧成立',
    )
  })

  it('真实文件里那个 video/* 陷阱仍然存在（否则负控场景已从仓库消失）', () => {
    // 这一条不参与判定成败的证明，只登记现状：陷阱还在，负控场景是真的。
    // 哪天模板里不再有 accept="video/*" 了，这里会提醒同步更新负控样本。
    const raw = fs.readFileSync(path.join(SRC, 'features', 'notes', 'NoteEditView.vue'), 'utf8')
    assert.ok(
      raw.includes('accept="video/*"'),
      'NoteEditView.vue 里已没有 accept="video/*"——本负控对应的真实场景从仓库消失了，请复核是否还需要这条护栏',
    )
  })

  it('英文撇号（don\'t）不会开启字符串态而反向吞掉声明', () => {
    const src = "<p>don't stop</p>\n.a { font-size: 12px; }\n"
    assert.ok(blankComments(src).includes('font-size: 12px'))
  })
})
