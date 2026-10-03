// notificationSourceLabel 的语义单测。
//
// 这条函数决定「消息」tab 每条通知行下面那行副标题显示什么。
// 2026-10-03 真机实测：改之前它返回 `n.kind`，于是**每一行**都显示 `email.important`
// （`/api/notifications` 50 条，kind 全是这个值）。所以这不是"个别脏数据"，
// 是稳定复现的用户可见缺陷，值得把语义钉住。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  notificationSourceLabel,
  NOTIFICATION_SOURCE_KEYS,
} from '../sourceLabels.ts'

// 假装成 vue-i18n 的 t：直接把 key 回显，便于断言"到底取了哪条文案"
const t = (key) => `T(${key})`

test('已知来源映射到已有的本地化文案', () => {
  assert.equal(notificationSourceLabel('email', 'email.important', t), 'T(messagesHub.filter.email)')
  assert.equal(notificationSourceLabel('rss', 'rss.new', t), 'T(messagesHub.filter.rss)')
  assert.equal(notificationSourceLabel('task', 'task.due', t), 'T(messagesHub.filter.task)')
})

test('kind 永远不参与展示 —— 这正是原缺陷的根', () => {
  // 真机上 50 条通知的 kind 全是 'email.important'。哪怕 source 缺失，
  // 也不能退回去显示 kind。
  assert.equal(notificationSourceLabel('', 'email.important', t), '')
  assert.equal(notificationSourceLabel(undefined, 'email.important', t), '')
  assert.equal(notificationSourceLabel(null, 'email.important', t), '')
  // source 已知时，输出与 kind 无关
  const a = notificationSourceLabel('email', 'email.important', t)
  const b = notificationSourceLabel('email', 'scheduledtask.anything', t)
  assert.equal(a, b)
})

test('未知来源留空，而不是把内部键原样透出去', () => {
  // 方向：空是安全的一侧，泄漏不是。与邮件摘要那条同源。
  assert.equal(notificationSourceLabel('scheduledtask.weekly', 'x', t), '')
  assert.equal(notificationSourceLabel('some-new-source', 'x', t), '')
  assert.equal(notificationSourceLabel('  email  ', 'x', t), 'T(messagesHub.filter.email)', '两端空白应被容忍')
})

test('含点的内部键被白名单拦下（靠的是白名单，不是某道正则）', () => {
  // 这两条**过去**被挂在一条名叫「兜底」的用例下，注释声称覆盖
  // `INTERNAL_KEY_RE` 那一道兜底。实测那是假的：把兜底整段删掉，
  // 本文件 6 条用例照样 6/6 全绿 —— 因为含点的 raw 根本走不到兜底，
  // 它在白名单查表（!key 就 return ''）那一步就已经被拦下了。
  //
  // 所以真正保护这批值的是白名单，本用例断言的也正是白名单的行为。
  // 少写一层就少一层，但不能靠一条**名字里写着它、实际碰不到它**的用例
  // 来假装有两层防护。
  assert.equal(notificationSourceLabel('email.important', 'x', t), '')
  assert.equal(notificationSourceLabel('a.b.c', 'x', t), '')

  // 负控（实测可转红）：把未知来源**放行**（`if (!key) return ''` 改成透传 raw），
  // 上面两条立刻转红 —— 说明本用例不是恒绿的装饰。
  //
  // 负控（实测可转红）：往白名单里加一个带点的键 `'email.important'`，
  // 下一条的不变量立刻转红。
})

test('白名单里不许出现带点的键 —— 这是「只有一层防护」成立的前提', () => {
  // 含点的值之所以必然被拦下，是因为整张表一个带点的键都没有。
  // 有人往表里塞一个 `email.important` 之类，这条就会转红，
  // 逼着做决定，而不是让真机上某一行的副标题静默变空。
  //
  // 内部键的形态（`email.important` / `scheduledtask.weekly` / `a.b`）在
  // 上一条用例里以实参形式钉住了，这里只钉「表里不许有点」这一条不变量。
  for (const [src, key] of Object.entries(NOTIFICATION_SOURCE_KEYS)) {
    assert.ok(!src.includes('.'), `白名单的来源名不许带点，否则内部键会被当合法来源放行: ${src}`)
    // 反过来，**文案 key 本来就该带点**（它是 i18n 命名空间路径
    // messagesHub.filter.email），所以不能对它做同样的断言 ——
    // 写成 !key.includes('.') 会把正确的表判红（本条第一版就栽在这里，
    // 自己把自己写红了）。别把「来源名不许有点」错误地套到 key 上。
    assert.ok(key.includes('.'), `文案 key 应是带点的 i18n 路径: ${key}`)
  }
})

test('映射表里的每个值都必须是 messagesHub.filter.* 下的 key', () => {
  // 防跑偏：新增来源时若写到了别的命名空间，这条会转红。
  // ⚠️ 这条**只**保证命名空间前缀，不保证 key 拼写正确 ——
  //   `messagesHub.filter.emial` 这种拼错它照样放行。
  //   key 拼错时 vue-i18n 会静默回退成 key 本身，那又变成
  //   「把 key 显示给用户」，正是我们在修的那个缺陷。
  //   全量 key 的存在性由仓库门禁 `npm run check:i18n` 把关，不在这里重复实现。
  for (const [src, key] of Object.entries(NOTIFICATION_SOURCE_KEYS)) {
    assert.ok(
      key.startsWith('messagesHub.filter.'),
      `来源 ${src} 的文案 key 不在 messagesHub.filter.* 下: ${key}`,
    )
  }
})
