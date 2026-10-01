// email-refresh-trigger.test.mjs
//
// 锁住两条与「数据刷新」直接相关的性质：
//
//   1. **冷启动必须拉一次**——不拉就只能看到本地库里的旧数据；
//   2. **失败不该吃掉成功后的长节流窗口**——否则断网一次，接下来 15 分钟
//      都不重试，而且失败被吞掉、界面上没有任何提示。
//
// 2026-10-03 的两处缺陷都出在 email-fetch-host.ts：
//
//   · `startEmailFetchHost()` 只调 `bindNative()`，**没有 kick**。而 kick 只
//     挂在 visibilitychange / appStateChange 上，这两个事件在冷启动时都不
//     触发（前者只在可见性**变化**时触发，后者在状态**变化**时触发）。
//     于是用户打开应用 → 收件箱是本地旧数据 → 服务端一次都不拉 → 要等他把
//     应用切后台再切回来。表现为「邮件不刷新」，且没有任何报错。
//
//   · 只有一个 `lastAt` 字段，而且在**发请求之前**就 `lastAt = now`。断网 /
//     5xx 照样占掉 15 分钟窗口，失败又被 `.catch(() => {})` 吞掉。用户在
//     这 15 分钟里只会看到「邮件不刷新了，重开也没用」。
//
// 负控见文件末尾，全部实测转红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript')

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')            // frontend/src
const HOST = path.join(SRC, 'features', 'email', 'email-fetch-host.ts')

const hostSrc = fs.readFileSync(HOST, 'utf8')

/** 去掉注释（判据不能被源码里的说明文字满足）。 */
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

const hostCode = codeOnly(hostSrc)

describe('后台收信的触发条件', () => {
  it('冷启动会主动 kick 一次（不只 bindNative）', () => {
    // 判据问的是「startEmailFetchHost 的函数体里有没有执行 kick()」，
    // 而不是「文件里有没有出现 kick」——后者会被 visibilitychange 那个
    // 监听器里的调用满足，而那正是漏掉冷启动的形态。
    const body = fnBody(hostCode, 'export function startEmailFetchHost()')
    assert.notEqual(body, null, '找不到 startEmailFetchHost 的函数体（判据可能已失效）')

    // 把监听器里的那两处调用去掉，剩下的才是「启动即跑」的那一次。
    const onlyListeners = body
      .replace(/document\.addEventListener\('visibilitychange'[\s\S]*?\)\n\s*\}\)/, '')
      .replace(/App\.addListener\('appStateChange'[\s\S]*?\n\s*\}\)/, '')
    assert.notEqual(onlyListeners, body, '负向准备失败：没识别出监听器块（源码形状变了？）')
    assert.match(
      onlyListeners,
      /\bkick\(\)/,
      '启动路径里没有 kick() —— 冷启动不拉取，用户只能看到本地旧数据，' +
      '且没有任何报错',
    )
  })

  it('未登录时不发起，也不占用节流窗口', () => {
    assert.match(
      hostCode,
      /if\s*\(!useAuthStore\(\)\.token\)\s*return/,
      '未登录时仍会发起收信：白打一趟需要 token 的接口，' +
      '还会把节流窗口记在这次注定失败的尝试上',
    )
  })

  it('失败会被记成失败（从而走短重试间隔），不是被吞掉', () => {
    const body = fnBody(hostCode, 'const kick = () => {')
    assert.notEqual(body, null, '找不到 kick 的函数体（判据可能已失效）')
    assert.match(
      body,
      /lastAttemptFailed\s*=\s*true/,
      '失败分支没有把 lastAttemptFailed 置 true —— 失败会继续享受 15 分钟的' +
      '成功间隔，断网一次之后 15 分钟不重试',
    )
    assert.match(body, /lastAttemptFailed\s*=\s*false/, '成功分支没有把 lastAttemptFailed 复位')
  })

  it('在途时会拦住第二轮（不会并发发起）', () => {
    const body = fnBody(hostCode, 'const kick = () => {')
    assert.match(
      body,
      /decideFetchKick\(state,\s*now[^)]*\)\s*!==\s*'run'\)\s*return/,
      'kick 没有用 decideFetchKick 的返回值做门禁 —— in-flight 拦不住，' +
      '切页/切前台会叠起多轮收信',
    )
  })

  it('时间戳记在发起时、而不是成功时', () => {
    const body = fnBody(hostCode, 'const kick = () => {')
    const at = body.indexOf('lastAttemptAt = now')
    const call = body.indexOf('runDelegatedEmailFetch')
    assert.notEqual(at, -1, 'kick 里没有写 lastAttemptAt')
    assert.notEqual(call, -1, 'kick 里没有调用 runDelegatedEmailFetch')
    assert.ok(
      at < call,
      'lastAttemptAt 记在请求**之后** —— 一轮跑三分钟的收信会把下一次也顺延三分钟',
    )
  })
})

describe('判据自检：负控必须转红', () => {
  const kickBody = () => fnBody(hostCode, 'const kick = () => {')

  it('把失败标记去掉 → 转红', () => {
    const broken = hostCode.replace('lastAttemptFailed = true', 'void 0')
    assert.notEqual(broken, hostCode, '负控样本没有真的改掉失败标记（替换没命中）')
    assert.equal(
      /lastAttemptFailed\s*=\s*true/.test(fnBody(broken, 'const kick = () => {')),
      false,
      '负控本该转红却判成了通过',
    )
    assert.ok(kickBody(), '判据在真实代码上就没命中过')
  })

  it('注释里写 lastAttemptFailed = true 不算数', () => {
    // 两处都要对：
    //   ① 变异必须在**剥注释之前**做。先 codeOnly 再注入注释，注入的
    //      「注释」在已经没有注释的源码里就是普通代码，判据当然命中——
    //      那样测的是「codeOnly 跑没跑」，不是「判据会不会被骗」。
    //   ② 必须把**真的那行**弄成注释。第一版是保留真赋值、另加一条注释，
    //      于是判据报「存在」是完全正确的，负控自己先转红了。
    const mutated = hostSrc.replace(
      'lastAttemptFailed = true',
      '/* lastAttemptFailed = true */',
    )
    assert.notEqual(mutated, hostSrc, '负控样本没有真的注释掉赋值（替换没命中）')
    const stripped = codeOnly(mutated)
    assert.ok(
      !stripped.includes('/* lastAttemptFailed = true */'),
      '负控样本里的注释没被剥掉——codeOnly 本身坏了，先修它',
    )
    assert.equal(
      /lastAttemptFailed\s*=\s*true/.test(fnBody(stripped, 'const kick = () => {')),
      false,
      '判据被注释骗过了——它必须只认代码',
    )
  })

  it('把时间戳挪到请求之后 → 转红', () => {
    const broken = codeOnly(hostSrc.replace(
      'state.lastAttemptAt = now\n    state.inFlight = true',
      'state.inFlight = true',
    ))
    assert.notEqual(broken, hostCode, '负控样本没有真的挪走时间戳（替换没命中）')
    const b = fnBody(broken, 'const kick = () => {')
    assert.equal(b.indexOf('lastAttemptAt = now'), -1, '时间戳行没被摘掉，样本无效')
    // 判据是「时间戳必须早于请求」；样本里根本没有时间戳，等价于违反。
    assert.equal(
      b.indexOf('lastAttemptAt = now') !== -1 &&
        b.indexOf('lastAttemptAt = now') < b.indexOf('runDelegatedEmailFetch'),
      false,
      '负控本该转红却判成了通过',
    )
  })

  it('未登录门禁去掉 → 转红', () => {
    const broken = hostCode.replace(/if\s*\(!useAuthStore\(\)\.token\)\s*return/, 'void 0')
    assert.notEqual(broken, hostCode, '负控样本没有真的去掉门禁（替换没命中）')
    assert.equal(
      /if\s*\(!useAuthStore\(\)\.token\)\s*return/.test(broken),
      false,
      '负控本该转红却判成了通过',
    )
  })
})

/** 取 `function <decl>` 或箭头函数体的源码（按大括号配平）。 */
function fnBody(code, decl) {
  const start = code.indexOf(decl)
  if (start < 0) return null
  const open = code.indexOf('{', start)
  if (open < 0) return null
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
