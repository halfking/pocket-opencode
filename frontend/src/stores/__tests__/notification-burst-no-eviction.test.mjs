// notification-burst-no-eviction.test.mjs — 需求 4「超 50 条不丢历史」的**突发路径**。
//
// ## 为什么第一版判据（notification-first-load-limit.test.ts）不够
//
// 它钉住的是「首次加载要 200 条，与后端硬上限一致」——那是**冷启动**那条路。
// 但真实库里那 32 条积压提醒不是冷启动到达的：
//
//   2026-10-03 08:00 定时流水线 → notifyImportant 无任何限流 → 一次性推 32 条
//   → 已在使用中的设备（首次加载发生在 08:00 **之前**）走的是 WS 突发这条路
//
// 这条路上有三处曾经只有注释、没有判据，任何一处被改都会让「不丢历史」失效
// 而没有任何用例变红：
//
//  1. `pushLocal` / `subscribeWs` 入账时**不截断** inbox。
//     一个看起来完全合理的优化（`this.inbox = this.inbox.slice(0, 50)` 省内存）
//     会把最旧的 6 条挤掉，而且只在真机上表现为「翻不到更早的提醒」。
//  2. 视图 `items` 是**整个** inbox，没有 slice。
//     同理，`computed(() => store.inbox.slice(0, 50))` 会让 store 里 56 条、
//     界面只画 50 条——store 测试全绿，界面上就是丢了历史。
//  3. 增量水位线 `since = max(created_at)` 配服务端 `created_at > since`：
//     同秒产生的 32 条不会被增量重拉捞回来，**它们能进 store 只因为 WS 推了**。
//     所以 1/2 一旦破了，没有第二条路能补。
//
// ## 为什么是源码判据而不是跑 store
//
// 与 notification-first-load-limit.test.ts 同一个理由：stores/notification.ts
// import '../api/notifications'（无扩展名），node 的 ESM 解析器解不了。
// 这里同样**不复制判定逻辑**，只断言「复制不出来的东西」。
//
// ## 负控（实测过）
//
// 1. 往 pushLocal 里注入 `this.inbox = this.inbox.slice(0, 50)`
//    → 「入账不得截断 inbox」转红。
// 2. 把视图的 items 改成 `computed(() => store.inbox.slice(0, 50))`
//    → 「视图必须渲染整个 inbox」转红。

import { strict as assert } from 'node:assert'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { test } from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const repo = join(here, '..', '..', '..')
// 本仓的 .ts 是 CRLF，归一化后再切边界，否则「找不到方法」会伪装成断言失败。
const read = (rel) => {
  const src = readFileSync(join(repo, rel), 'utf8')
  assert.ok(src.length > 0, `${rel} 读出来是空的：判据扫不到东西时会静默放行`)
  return src.replace(/\r\n/g, '\n')
}

const store = read('src/stores/notification.ts')
const view = read('src/features/notifications/NotificationsView.vue')

/**
 * 取 `indent` 缩进的方法体。
 *
 * 结尾只认**独占一行**的 `}`（`\n{indent}}\n`）：用 `\n}` 会撞上内层箭头函数的
 * 收尾，切出来的范围比想的短——那种情况下断言会「因为扫不到而通过」，
 * 是判据最危险的失败方式。
 */
function methodBody(src, signature, indent = '    ') {
  const start = src.indexOf(signature)
  assert.notEqual(start, -1, `找不到 ${signature} —— 若被改名，请同步更新本判据`)
  // 必须是 indent 个**空格** + 独占一行的 `}`（对象字面量里的方法收尾是 `},`，
  // 所以逗号可选）。两个坑都踩过：
  //   - `\{4\}` 是「一个含 4 的花括号」，永远匹配不到；
  //   - 放宽成「任意缩进」会先撞上内层 6 空格的 `}`，切出来的范围比想的短 ——
  //     而那种情况下断言会「因为扫不到而通过」，是判据最危险的失败方式。
  const closer = new RegExp(`\\n${' '.repeat(indent.length)}\\}\\n|\\n${' '.repeat(indent.length)}\\},\\n`)
  const m = closer.exec(src.slice(start))
  assert.ok(m, `${signature} 的结尾没找到 ${indent.length} 个空格缩进的 } —— 范围切片失效`)
  const body = src.slice(start, start + m.index)
  assert.ok(body.includes('inbox'),
    `${signature} 的切片里没有 inbox：范围切错了，不是「它真的不碰 inbox」`)
  return body
}

test('WS 入账不得截断 inbox（32 条突发不能挤掉更早的历史）', () => {
  // 两个入账口都要查：pushLocal 是分发器用的，subscribeWs 是兼容旧接线的。
  for (const sig of ['pushLocal(n: Notification) {', 'subscribeWs(']) {
    const body = methodBody(store, sig)
    assert.match(body, /inbox\.unshift\(/,
      `${sig} 必须用 unshift 入账（新的在前）`)
    assert.doesNotMatch(body, /\.slice\(/,
      `${sig} 里出现 slice —— 截断 inbox 会把最旧的提醒挤掉，` +
      '而 store 里其余测试仍然全绿，只在真机上表现为「翻不到更早的提醒」')
    assert.doesNotMatch(body, /inbox\s*=\s*this\.inbox\.(filter|concat)/,
      `${sig} 重新赋值 inbox 会丢掉本次入账之外的历史`)
  }
})

test('视图必须渲染整个 inbox，不能在展示层截断', () => {
  // items 是列表的唯一数据源；它一旦被切片，store 正确也救不了界面。
  // 用贪婪匹配取到**行尾**那个右括号：`() =>` 里自带一个 `)`，
  // 非贪婪写法会切在箭头函数参数处（第一版就栽在这里，报的却像「实现不对」）。
  const items = /const items = computed\((.*)\)/.exec(view)
  assert.ok(items, 'NotificationsView.vue 里找不到 `const items = computed(...)`')
  assert.match(items[1], /store\.inbox\s*$/,
    `items 的定义 = computed(${items[1]})，它必须是整个 store.inbox：` +
    '切片会变成「store 里有 56 条、界面只画 50 条」，而 store 侧测试全绿')
  assert.doesNotMatch(view, /\.slice\(/,
    '视图里出现 slice：展示层截断等于对用户丢历史')
  assert.match(view, /v-for="n in items"/,
    '模板必须直接遍历 items；若换成另一个被截断的数组，本判据要跟着改')
})

test('增量水位线仍是 max(created_at)（同秒突发只能靠 WS 入账，这条不能退化）', () => {
  // 不是「要求它更聪明」，而是把现状钉住：一旦有人把 since 改成别的语义，
  // 上面两条的「唯一入账路径」论证就不再成立，必须重新审。
  const body = methodBody(store, 'async loadInbox(')
  assert.match(body, /Math\.max\(\.\.\.this\.inbox\.map\(\(n\) => n\.created_at \|\| 0\)\)/,
    '增量 since 必须仍是本地最大 created_at')
})
