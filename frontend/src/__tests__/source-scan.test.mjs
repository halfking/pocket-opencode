// source-scan.test.mjs — 剥注释底座的自检。
//
// 这个底座被两个「扫源码」判据压着：一个是版本号显示点覆盖，
// 一个是密码箱入口门控。底座一旦失明（把代码当注释、或把注释当代码），
// 下游判据不会报错，只会**安静地给出错误答案** —— 那比没有判据更糟。
//
// 所以这里不测「stripVueComments 删掉了某些字符」，而测三件更要紧的事：
//   ① 注释里的词确实被删掉（否则注释能翻转判据结论）；
//   ② 代码里的词确实还在（否则判据对真实实现也失明）；
//   ③ URL 里的 // 不会把整行截断（否则会把后面的目标串一起藏掉）。
// ①②③ 在**真实仓库文件**上验证，而不是只验证人造样本 ——
// 人造样本绿了但真实文件不绿的情况，正是这个底座最初出问题的方式。

import assert from 'node:assert/strict'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { stripVueComments, vueFilesUnder, readVueCode, relPosix, findVersionLiterals, computedArgs, balancedSpan, spanToEnclosingBlock } from './source-scan.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const SRC = join(here, '..')
const MORE_VIEW = join(SRC, 'features', 'more', 'MoreHubView.vue')

describe('source-scan: 剥注释底座', () => {
  it('① 注释里的关键词被删掉', () => {
    const s = ['<template>', '  <!-- 这里问的不是 security.keystore_v1 -->', '  <p>hi</p>', '</template>'].join('\n')
    assert.ok(!stripVueComments(s).includes('keystore_v1'))
  })

  it('① 行注释与块注释同样被删掉', () => {
    const s = ['const a = 1', '// 提一嘴 APP_VERSION', '/* 块注释里的 APP_VERSION */', 'const b = 2'].join('\n')
    const out = stripVueComments(s)
    assert.ok(!out.includes('APP_VERSION'), '行/块注释没被删干净')
    assert.ok(out.includes('const a = 1') && out.includes('const b = 2'), '代码被误删了')
  })

  it('② 代码里的关键词不会被误删', () => {
    const out = stripVueComments('const version = ref(APP_VERSION.version)')
    assert.ok(out.includes('APP_VERSION'), '真实代码被当成注释删掉了')
  })

  it('③ URL 里的 // 不截断整行', () => {
    const out = stripVueComments("const u = 'https://x.cn' // 真正的注释 APP_VERSION")
    assert.ok(out.includes('https://x.cn'), 'URL 被当成注释截断了')
    assert.ok(!out.includes('APP_VERSION'), '真正的行注释反而没被删')
  })

  it('防空跑：真的扫到了 .vue 文件', () => {
    const files = vueFilesUnder(SRC)
    assert.ok(files.length >= 50, '只扫到 ' + files.length + ' 个 .vue，路径可能不对')
  })

  it('真实文件上：注释里的词删得掉，且这正是判据结论翻转的原因', () => {
    // MoreHubView 的说明注释里就写着 security.keystore_v1。
    const { src, code } = readVueCode(MORE_VIEW)
    assert.ok(src.includes('keystore_v1'), '前提变了：注释里已经没有这个词了，本用例失去意义')
    assert.ok(!code.includes('keystore_v1'), '剥完注释后代码里仍出现 keystore_v1，说明底座失明')
    // 反向：代码里真实用到的 import 必须留下
    assert.ok(code.includes('isKeystoreAvailable'), '代码里真实的能力探针被误删了')
  })

  it('relPosix 输出可读且不含反斜杠', () => {
    const p = relPosix(SRC, MORE_VIEW)
    assert.equal(p, 'features/more/MoreHubView.vue')
  })

  it('④ 多行注释被抹掉后行数不变（行号不能错位）', () => {
    // 判据要报「第几行」，就得保证剥注释不改变行结构。
    // 第一版把多行注释整段替换成一个空格，行号会集体上移 ——
    // 报出来的位置指向别处，而没人会知道。
    const src = [
      '<template>',
      '  <!--',
      '    这里写了很多行',
      '    v1.2.3 也在里面',
      '  -->',
      '  <p>hi</p>',
      '</template>',
    ].join('\n')
    const out = stripVueComments(src)
    assert.equal(out.split('\n').length, src.split('\n').length, '剥注释改变了行数')
    assert.ok(!out.includes('v1.2.3'), '多行 HTML 注释里的字面量没被删掉')
  })

  it('⑥ computedArgs 精确截取实参，不被无分号风格骗到', () => {
    // 反例就是第一版的误报现场：userName 的 computed 和下一行的 APP_VERSION
    // 之间既没有 ; 也没有 {，靠字符类护栏必然把两者当成一句。
    const code = [
      'const userName = computed(() => auth.user)',
      'const version = ref(APP_VERSION.version)',
    ].join('\n')
    const args = computedArgs(code)
    assert.equal(args.length, 1, '实参个数不对：' + JSON.stringify(args))
    assert.ok(args[0].includes('auth.user'), '实参截错了：' + JSON.stringify(args[0]))
    assert.ok(!args[0].includes('APP_VERSION'), '把下一行的常量算进了实参 —— 正是第一版误报的成因')
  })

  it('⑥ computedArgs 处理泛型、嵌套括号与字符串里的右括号', () => {
    const code = [
      'const a = computed<HubItem[]>(() => [',
      "  { to: '/x', label: t('a)b') },",
      '])',
      'const b = computed(() => applyCapabilityGates(mainFeatures.value, { k: 1 }))',
    ].join('\n')
    const args = computedArgs(code)
    assert.equal(args.length, 2, '实参个数不对：' + JSON.stringify(args))
    assert.ok(args[0].includes('HubItem[]') === false, '泛型参数不该进实参')
    assert.ok(args[0].includes("t('a)b')"), '字符串里的右括号把配对截断了：' + JSON.stringify(args[0]))
    assert.ok(args[1].includes('applyCapabilityGates') && args[1].endsWith('})'), '嵌套括号截错：' + args[1])
  })

  it('⑥ computedArgs 抓得住「常量接进 computed」这个真实形态', () => {
    const args = computedArgs('const version = computed(() => APP_VERSION.version)')
    assert.ok(args.some((a) => a.includes('APP_VERSION')), '没抓到常量接进 computed')
  })

  it('⑦ balancedSpan 配平取到**完整**实参区，不受任何字符上限影响', () => {
    // 这条判据钉的正是 §428 的形状：「取够 N 个字符」让覆盖率取决于排版。
    // 下面的 PAD 刻意超过 600 —— 旧的 slice(at, at + 600) 会把它切掉。
    const PAD = 'x'.repeat(900)
    const code = [
      `await finalizeRecording(id, ${PAD}, { toWav: w })`,
      'await somethingElse(bad)',
    ].join('\n')
    const span = balancedSpan(code, 'finalizeRecording(')
    assert.ok(span, '锚点存在却返回 null')
    assert.ok(span.span.includes('toWav'), '超长实参区被截断了 ⇒ 覆盖取决于排版')
    assert.ok(!span.span.includes('somethingElse'), '越界吃到了下一句 —— 覆盖不是由配平定的')
    assert.ok(span.inner.includes(PAD) && span.inner.trimEnd().endsWith('}'), 'inner 应只含括号内部')
  })

  it('⑦ balancedSpan 花括号模式取整个函数体（锚点以 ( 结尾）', () => {
    const code = [
      'async function onSummarize() {',
      '  const a = 1',
      '  return a',
      '}',
      'function unrelated() { return 2 }',
    ].join('\n')
    const span = balancedSpan(code, 'async function onSummarize(', '{')
    assert.ok(span && span.span.endsWith('}'), '函数体没配平到收尾')
    assert.ok(span.span.includes('return a'), '函数体内容缺失')
    assert.ok(!span.span.includes('unrelated'), '越界吃到了下一个函数')
  })

  it('⑦ balancedSpan 锚点缺失必须返回 null，不得返回空串', () => {
    // 返回空串会把调用方自己的「找不到调用点」提示吃掉，
    // 让缺陷表现成「判据静默通过」而不是「判据报红」。
    assert.equal(balancedSpan('const a = 1', 'finalizeRecording('), null)
  })

  it('⑦ balancedSpan 跳过字符串里的括号', () => {
    const code = `call(a, ')))', b)\nnext()`
    const span = balancedSpan(code, 'call(')
    assert.ok(span && !span.span.includes('next'), '字符串里的括号把配平带偏了')
  })

  it('⑧ spanToEnclosingBlock 取到锚点所在块的开头（断言目标常在**下一句**）', () => {
    // 这一族的锚点是「一条语句」，断言的目标在紧随其后 ⇒
    // 配平实参会缩到括号内部、看不见目标，正确单元是**所在的块**。
    const code = [
      'function f() {',
      '  try {',
      '    const notice = pick()',
      '    if (notice) this.err = NOTICE',
      '    return notice',
      '  } finally {',
      '    cleanup()',
      '  }',
      '}',
      'function g() { after() }',
    ].join('\n')
    const r = spanToEnclosingBlock(code, 'const notice = pick()')
    assert.ok(r, '锚点存在却返回 null')
    assert.ok(r.span.includes('if (notice) this.err = NOTICE'), '块尾没覆盖到断言目标')
    assert.ok(r.span.includes('return notice'), '块尾应包含紧随的 return')
    assert.ok(!r.span.includes('cleanup'), '越界吃进了 finally 块')
    assert.ok(!r.span.includes('after'), '越界吃到了下一个函数')
  })

  it('⑧ spanToEnclosingBlock 锚点缺失返回 null，且不被字符串里的花括号带偏', () => {
    assert.equal(spanToEnclosingBlock('const a = 1', 'nothing here'), null)
    const code = [
      'function f() {',
      "  const s = '{'",
      '  const notice = pick()',
      '  after(notice)',
      '}',
    ].join('\n')
    const r = spanToEnclosingBlock(code, 'const notice = pick()')
    assert.ok(r, '锚点存在却返回 null')
    assert.ok(r.span.includes('after(notice)'), '字符串里的 { 把块边界算错了')
  })

  // ★ 这条是 §428.7 真实踩到的那颗雷（api/meetings.ts 的 fallbackRefine 签名）：
  //   参数表里有**类型注解的花括号**，直接 indexOf('{') 会命中它，
  //   配平于是从对象类型开始收尾 —— 整个函数体落在窗口外，判据静默变瞎。
  it('⑧ balancedSpan 找函数体时先配平参数表，不被类型注解的 { 带偏', () => {
    const code = [
      'async function fallbackRefine(',
      '  segments: MeetingSegment[],',
      '  signal?: AbortSignal,',
      '  meta?: { title?: string; participants?: string[] },',
      '): Promise<RefineResult> {',
      '  try { await go() }',
      '  catch (e) { if (isAbortError(e)) throw e }',
      '  return { ok: true }',
      '}',
      'async function nextOne() { unrelated() }',
    ].join('\n')
    const r = balancedSpan(code, 'async function fallbackRefine(', '{')
    assert.ok(r, '取不到函数体')
    assert.ok(r.span.includes('isAbortError(e)'), '配平被类型注解的花括号带偏 ⇒ 函数体落在窗口外')
    assert.ok(!r.span.includes('nextOne'), '越界吃到了下一个函数')
  })

  it('⑤ findVersionLiterals 能抓到模板里的裸字面量，并报对行号', () => {
    // 探针必须先证明自己在真实代码上看得见，否则下面「全仓 0 命中」是空转。
    const code = ['<template>', '  <p>hi</p>', '  <p>v1.2.0-mobile</p>', '</template>'].join('\n')
    const hits = findVersionLiterals(code)
    assert.equal(hits.length, 1, '没抓到模板里的版本字面量')
    assert.equal(hits[0].line, 3, '行号不对：' + JSON.stringify(hits[0]))
    assert.equal(hits[0].text, 'v1.2.0')
  })
})
