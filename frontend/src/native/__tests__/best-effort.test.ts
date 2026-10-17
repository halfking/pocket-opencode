/**
 * best-effort 后台任务的**行为**回归测试（2026-10-17）。
 *
 * 背景：`recordingRuntime.ts` 收尾那段原先是
 *   `void getMeeting(id).then((m) => { if (m) syncMeetingMetadata(m) })`
 * —— 同文件另两处同类写法都带 `.catch`，唯独这行漏了，而 `getMeeting` 会
 * `await localDB.queryOne(...)`，DB 异常时能 reject ⇒ 未处理拒绝日志。
 *
 * ⚠⚠ **这道门为什么不做成「源码里有没有 catch」的静态判据**：
 *   那类判据恒真风险最高（改个写法就绕开、换个人写就漏），仓库有
 *   `check:vacuous-guard` 专门盯着。**撤掉 `best-effort.ts` 里的 `.catch`
 *   会让本文件第一组用例转红**（见文件末的变异记录），这才是有牙的形态。
 *
 * 第二组用例是**调用点**的静态契约：防止有人把 `runBestEffort(...)` 改回裸
 * `.then(...)` 而行为测试还绿——因为行为测试只测助手，不测调用点。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it, mock } from 'node:test'

import { runBestEffort } from '../best-effort.ts'

const HERE = dirname(fileURLToPath(import.meta.url))

describe('runBestEffort：失败的 best-effort 不得变成未处理拒绝', () => {
  it('work 异步 reject ⇒ 返回的 promise 仍然 resolve（而不是 reject）', async () => {
    let resolved = false
    const p = runBestEffort('t/async', () => Promise.reject(new Error('db down')))
      .then(() => { resolved = true })
    await p
    assert.equal(resolved, true, 'best-effort 的失败必须被吞掉：调用方不该看到 reject')
  })

  it('work 同步 throw ⇒ 同样不得冒泡', async () => {
    let resolved = false
    await runBestEffort('t/sync', () => { throw new Error('boom') })
      .then(() => { resolved = true })
    assert.equal(resolved, true)
  })

  it('work 正常 resolve ⇒ 值原样通过，不吞成功', async () => {
    let seen: unknown = 'NOT_CALLED'
    await runBestEffort('t/ok', () => Promise.resolve(42)).then(() => { seen = 'called' })
    assert.equal(seen, 'called')
  })

  it('work 返回非 promise 值也不该炸', async () => {
    let resolved = false
    await runBestEffort('t/plain', () => 7).then(() => { resolved = true })
    assert.equal(resolved, true)
  })

  it('失败会留下可定位的日志（label 必须在消息里）', async () => {
    const warn = mock.method(console, 'warn', () => {})
    try {
      await runBestEffort('my-label-xyz', () => Promise.reject(new Error('x')))
      // ⚠ node:test 的 `mock.calls[i]` 是 `{ arguments, stack, this }` 结构，
      //   **不是数组** —— 取参数要走 `.arguments`，写 `c[0]` 会永远拿到 undefined
      //   （第一版就是这么恒红的：断言本身坏掉，而不是被测代码坏掉）。
      const hit = warn.mock.calls.some((c) => String(c.arguments?.[0] ?? '').includes('my-label-xyz'))
      assert.equal(hit, true, '失败必须留下带 label 的日志，否则等于静默吞掉')
    } finally {
      warn.mock.restore()
    }
  })
})

describe('调用点契约：录音收尾的元数据同步必须走 runBestEffort', () => {
  const src = readFileSync(join(HERE, '..', 'recordingRuntime.ts'), 'utf8')

  it('recordingRuntime 必须 import 助手', () => {
    assert.match(src, /import\s*\{\s*runBestEffort\s*\}\s*from\s*'\.\/best-effort'/,
      'recordingRuntime.ts 没有 import runBestEffort ⇒ 收尾那段又退回裸 .then')
  })

  it('收尾处的元数据同步必须包在 runBestEffort 里，且不得留裸 .then 调用', () => {
    assert.match(src, /runBestEffort\(\s*'[^']*metadata-sync[^']*'/,
      '找不到包住元数据同步的 runBestEffort(...)')
    // 反向对照：这一行形态的裸调用不允许再出现
    assert.doesNotMatch(src,
      /void\s+getMeeting\([^)]*\)\.then\(/,
      '又出现了裸 `void getMeeting(...).then(` —— 那就是本次修掉的未处理拒绝')
  })
})
