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
  INTERNAL_KEY_RE,
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

test('source 里混进内部键形态时同样拦住（兜底）', () => {
  // 万一有人把 'email.important' 塞进 source 字段，也不能漏到界面上。
  assert.equal(notificationSourceLabel('email.important', 'x', t), '')
  assert.equal(notificationSourceLabel('a.b.c', 'x', t), '')
})

test('INTERNAL_KEY_RE 只认「至少两段小写点分」，不会误伤正常文案', () => {
  for (const s of ['email.important', 'scheduledtask.weekly', 'a.b']) {
    assert.ok(INTERNAL_KEY_RE.test(s), `应判为内部键: ${s}`)
  }
  for (const s of ['email', '邮件', 'Important Email', 'email-important', 'Email.Alert']) {
    assert.equal(INTERNAL_KEY_RE.test(s), false, `不该判为内部键: ${s}`)
  }
})

test('映射表里的每个值都必须是已存在的 i18n 命名空间', () => {
  // 防漂移：新增来源时若拼错命名空间，vue-i18n 会静默回退成 key 本身，
  // 于是又变成"把 key 显示给用户"——那正是我们在修的那个缺陷。
  for (const [src, key] of Object.entries(NOTIFICATION_SOURCE_KEYS)) {
    assert.ok(
      key.startsWith('messagesHub.filter.'),
      `来源 ${src} 的文案 key 不在 messagesHub.filter.* 下: ${key}`,
    )
  }
})
