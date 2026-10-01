// email-job-runtime-singleton.test.mjs
//
// 锁住一条需求级不变式：**邮件长作业的状态必须活在进程里，而不是活在组件里。**
//
// 2026-10-03 审计到的缺陷：`useEmailInbox()` / `useInvoiceList()` 把
// classifying / syncing 放在 composable 的局部 `ref()` 里，而视图
// `onUnmounted` 之后组件连同这些 ref 一起消失——**在途的 fetch 却不会跟着停**。
// 于是：
//
//   1. 点「自动归纳整理」，作业在后台正常跑（最长 20 轮 × 20 封）；
//   2. 切页 → 进度与「取消」按钮一起消失；
//   3. 切回来 → 新实例，`classifying=false`，界面像什么都没发生过；
//   4. 再点一次 → 起**第二个并发作业**，在后端 emailPipelineMu 上排队，
//      界面上就是「转圈不动」。
//
// 这同时违反需求「确认可以在切换页面后仍能执行」与「后台执行的 api 可以
// 强行终止」：作业确实在跑，用户却既看不见也停不掉。
//
// 修法是照 recordingRuntime / aiStreamRuntime 已有的进程级单例模式，把状态
// 和中止器挂到 globalThis 上。这个文件守两件事：
//   - **运行时真语义**（import 真实模块，验证共享与中止真的生效）；
//   - **源码接线**（两个 composable 不得再自己声明作业状态的局部 ref）。
//
// 负控在文件末尾，且都实测转红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript')

const HERE = path.dirname(fileURLToPath(import.meta.url))
const FEAT = path.resolve(HERE, '..')

const INBOX_SRC = path.join(FEAT, 'use-email-inbox.ts')
const INVOICE_SRC = path.join(FEAT, 'use-invoice-list.ts')

/**
 * 去掉源码里所有注释（只留 trivia 之外的代码）。
 *
 * 判据必须剥注释：这些文件的说明文字里就写着 `emailJobs`、`classifyAbort`
 * 这些词，用「源码里出现过 X」来判会被自己的注释满足。
 */
export function codeOnly(src) {
  const sf = ts.createSourceFile('x.ts', src, ts.ScriptTarget.Latest, true)
  const ranges = []
  const scan = (node) => {
    for (const r of ts.getLeadingCommentRanges(src, node.pos) ?? []) ranges.push(r)
    for (const r of ts.getTrailingCommentRanges(src, node.end) ?? []) ranges.push(r)
    ts.forEachChild(node, scan)
  }
  scan(sf)
  ranges.sort((a, b) => a.pos - b.pos)
  let out = ''
  let last = 0
  for (const r of ranges) {
    out += src.slice(last, r.pos) + ' '.repeat(r.end - r.pos)
    last = r.end
  }
  return out + src.slice(last)
}

/** composable 里有没有自己声明作业状态的局部 ref（剥注释后找）。 */
export function declaresLocalJobRef(code, names) {
  const sf = ts.createSourceFile('x.ts', code, ts.ScriptTarget.Latest, true)
  let found = false
  const visit = (node) => {
    if (found) return
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && names.includes(node.name.text)) {
      const init = node.initializer
      if (init && ts.isCallExpression(init) && init.expression.getText(sf) === 'ref') {
        found = true
        return
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return found
}

const inboxCode = codeOnly(fs.readFileSync(INBOX_SRC, 'utf8'))
const invoiceCode = codeOnly(fs.readFileSync(INVOICE_SRC, 'utf8'))

describe('邮件长作业状态是进程级单例', () => {
  it('useEmailInbox 不再自己声明 classifying/hint 的局部 ref', () => {
    assert.equal(
      declaresLocalJobRef(inboxCode, ['classifying', 'classifyHint', 'classifyCancel']),
      false,
      'use-email-inbox.ts 又出现了作业状态的局部 ref —— 切页后状态会随组件一起消失，' +
      '需求「切页后仍能执行 + 可强行终止」会静默退回 2026-10-03 之前的形态',
    )
  })

  it('useInvoiceList 不再用局部 syncing 表示整理作业', () => {
    // syncing 本页还要用来驱动 syncAndReload 的瞬时态，判据只看 runPipeline 里
    // 有没有再去动它——动它就等于把全局作业状态当成本页的瞬时态，一收一放
    // 就会把仍在跑的作业抹成「没在跑」。
    const body = runPipelineBody(invoiceCode)
    assert.notEqual(body, null, '找不到 use-invoice-list.ts 里的 runPipeline（判据可能已失效）')
    assert.equal(
      /\bsyncing\.value\s*=/.test(body),
      false,
      'runPipeline 里仍在写 syncing.value —— 整理作业的 running 必须是进程级的，' +
      '否则 syncAndReload 一收尾就会把还在跑的作业抹掉',
    )
  })

  it('两个 composable 都接到了同一个 emailJobs 单例', () => {
    assert.match(inboxCode, /emailJobs\.classify/)
    assert.match(invoiceCode, /emailJobs\.pipeline/)
  })
})

describe('运行时真语义（import 真实模块）', () => {
  it('两次 import 拿到的是同一份状态（模拟 HMR 重新求值）', async () => {
    const a = await import('../email-job-runtime.ts')
    const b = await import('../email-job-runtime.ts?reload=1')
    assert.equal(a.emailJobs, b.emailJobs, 'globalThis 单例失效：模块被重新求值后拿到了一份新状态')
  })

  it('cancelClassifyRun 真的 abort 了在途请求，而不只是置标记', async () => {
    const { emailJobs, startClassifyRun, cancelClassifyRun, finishClassifyRun } =
      await import('../email-job-runtime.ts')
    finishClassifyRun()
    const c = startClassifyRun()
    assert.equal(emailJobs.classify.running.value, true, 'startClassifyRun 没有把 running 置起来')

    const didAbort = cancelClassifyRun()
    assert.equal(didAbort, true, 'cancelClassifyRun 报告没有中止任何在途请求')
    assert.equal(c.signal.aborted, true, '在途请求没有被 abort —— 这就是「点了取消没反应」')
    assert.equal(emailJobs.classify.cancelRequested.value, true, '没有置中止标记，批间循环还会再发一轮')

    finishClassifyRun()
    assert.equal(emailJobs.classify.running.value, false, '收尾后 running 仍是 true，界面会一直转圈')
  })

  it('没有在途请求时 cancelClassifyRun 返回 false（不是假成功）', async () => {
    const { finishClassifyRun, cancelClassifyRun } = await import('../email-job-runtime.ts')
    finishClassifyRun()
    assert.equal(cancelClassifyRun(), false)
  })

  it('重复 start 会先中止上一轮残留的中止器', async () => {
    const { startClassifyRun, finishClassifyRun } = await import('../email-job-runtime.ts')
    finishClassifyRun()
    const first = startClassifyRun()
    const second = startClassifyRun()
    assert.equal(first.signal.aborted, true, '上一轮的中止器还活着，可能被用来中止新一轮的 UI 操作')
    assert.equal(second.signal.aborted, false)
    finishClassifyRun()
  })

  it('pipeline 侧的终止语义与归类一致', async () => {
    const { startPipelineRun, cancelPipelineRun, finishPipelineRun, emailJobs } =
      await import('../email-job-runtime.ts')
    finishPipelineRun()
    const c = startPipelineRun()
    assert.equal(emailJobs.pipeline.running.value, true)
    assert.equal(cancelPipelineRun(), true)
    assert.equal(c.signal.aborted, true)
    finishPipelineRun()
    assert.equal(emailJobs.pipeline.running.value, false)
  })
})

describe('判据自检：负控必须转红', () => {
  it('把 use-email-inbox 改回局部 ref → 接线判据转红', () => {
    // 真实缺陷形态：classifying 重新变成 composable 里的局部 ref(false)
    const broken = inboxCode.replace(
      'const { running: classifying',
      'const classifying = ref(false)\n  const { running: _unusedClassifying',
    )
    assert.notEqual(broken, inboxCode, '负控样本没有真的插入局部 ref（替换没命中）')
    assert.equal(
      declaresLocalJobRef(broken, ['classifying']),
      true,
      '负控本该转红却判成了通过——说明判据只会读原始字符串',
    )
  })

  it('注释里写 ref(false) 不算声明（判据必须剥注释）', () => {
    const broken = inboxCode.replace('const purgeBusy = ref(false)', 'const purgeBusy = ref(false) // const classifying = ref(false)')
    assert.notEqual(broken, inboxCode, '负控样本没有真的加注释（替换没命中）')
    assert.equal(
      declaresLocalJobRef(broken, ['classifying']),
      false,
      '注释里的声明被算进去了——判据必须先剥注释',
    )
  })

  it('runPipeline 里的 syncing.value = true → 接线判据转红', () => {
    const broken = invoiceCode.replace(
      'const controller = startPipelineRun()',
      'syncing.value = true\n    const controller = startPipelineRun()',
    )
    assert.notEqual(broken, invoiceCode, '负控样本没有真的插入 syncing.value（替换没命中）')
    assert.equal(
      /\bsyncing\.value\s*=/.test(runPipelineBody(broken)),
      true,
      '负控本该转红却判成了通过——说明判据在读函数体之外的地方',
    )
  })
})

/** 取 use-invoice-list.ts 里 runPipeline 的函数体（按大括号配平）。 */
function runPipelineBody(code) {
  const start = code.indexOf('async function runPipeline(')
  if (start < 0) return null
  const open = code.indexOf('{', start)
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') {
      depth--
      if (depth === 0) return code.slice(open, i + 1)
    }
  }
  return null
}
