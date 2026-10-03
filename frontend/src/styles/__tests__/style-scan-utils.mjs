// style-scan-utils.mjs
//
// 样式扫描器共用的底层工具。**刻意不叫 .test.mjs**——
// 工具模块一旦被 .test.mjs 引用，顶层 describe/it 会跟着注册进当前进程，
// 于是「A 护栏 import B 护栏」会把 B 的测试也跑一遍，两边的生命周期互相取消
// （实测表现为满屏 "test did not finish before its parent and was cancelled"，
//  看起来像判据坏了，其���是模块副作用）。
//
// 单独成文件是「护栏之间不互相 import」的落地方式。

import * as fs from 'node:fs'
import * as path from 'node:path'

/**
 * 把注释换成等长空格（长度必须不变——判据要按偏移回原文取上下文）。
 * 不剥注释的话，「在注释里解释旧写法」会被误判成违规，久了就没人敢写注释。
 *
 * ⚠️ 2026-10-03 修一处**永远绿的盲区**。
 * 第一版没有字符串感知，于是 .vue 模板里 accept 属性值中的 video 加星号
 * 被当成块注释起点，一路吞到下一个注释收尾——NoteEditView.vue 里 7658 个字符的
 * 真实 CSS 被整段抹成空格，其中正好有 2 处 font-size: 12px 是字号护栏
 * 该抓的违规。**方向是 fail-open：判据扫不到，于是测试一直绿。**
 *
 * 修法不是把块注释判据调松，而是让状态机认识引号：
 * 只有当引号紧跟在 = : ( 之后（HTML 属性值 / CSS 字符串 / url()）才进入字符串态。
 * 这么判是因为 Vue 模板正文里会出现英文撇号（don't），无差别地把单引号
 * 当字符串起点会反过来吞掉更多内容——那还是同一个 fail-open 方向。
 */
export function blankComments(src) {
  const out = src.split('')
  const isSpace = (c) => c === ' ' || c === '\t' || c === '\r' || c === '\n'
  /** 前一个非空白字符——用来判断引号是不是属性值/字符串的起点。 */
  const prevNonSpace = (from) => {
    let k = from - 1
    while (k >= 0 && isSpace(src[k])) k--
    return k >= 0 ? src[k] : ''
  }
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
      const c = src[i]
      if (c === '"' || c === "'" || c === '`') {
        const p = prevNonSpace(i)
        if (p === '=' || p === ':' || p === '(') {
          // 属性值 / CSS 字符串：跳到配对的收尾引号为止，内部一律不动。
          const close = src.indexOf(c, i + 1)
          i = close < 0 ? src.length : close + 1
          continue
        }
      }
      i++
    }
  }
  return out.join('')
}

/** 递归收集 .vue / .css 样式文件（跳过 node_modules 与点目录）。 */
export function walkStyleFiles(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) {
      if (e.name === 'node_modules' || e.name.startsWith('.')) continue
      walkStyleFiles(p, acc)
    } else if (e.name.endsWith('.vue') || e.name.endsWith('.css')) acc.push(p)
  }
  return acc
}
