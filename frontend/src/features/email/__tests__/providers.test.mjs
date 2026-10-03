import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  EMAIL_PROVIDERS,
  inferProviderId,
  isLocalTestAddress,
  providerById,
} from '../providers.ts'

test('qq / 163 / gmail / outlook infer from domain', () => {
  assert.equal(inferProviderId('user123456@qq.com'), 'qq')
  assert.equal(inferProviderId('someone@163.com'), '163')
  assert.equal(inferProviderId('a@126.com'), '163')
  assert.equal(inferProviderId('a@gmail.com'), 'gmail')
  assert.equal(inferProviderId('a@outlook.com'), 'outlook')
})

test('腾讯企业邮按公司自有域名识别，并套用 exmail 的 IMAP/SMTP', () => {
  // 2026-10-01：这里原先断言 'other'，那是密钥脱敏那轮的副作用（把真实公司
  // 域名从 providers.ts 删掉后，顺手把断言改成"不识别"），不是产品判断。
  // 腾讯企业邮的 IMAP 主机由腾讯统一提供（imap.exmail.qq.com），邮箱地址用的
  // 是租户自己的公司域名——不按公司域名识别，用户填公司邮箱时主机不会被自动
  // 填好，只能手输。providers.ts 的 hint 也一直写着「公司域名，如 @kxpms.cn」。
  assert.equal(inferProviderId('huangxutao@kxpms.cn'), 'exmail')
  assert.equal(inferProviderId('someone@exmail.qq.com'), 'exmail')
  const ex = providerById('exmail')
  assert.equal(ex.imapHost, 'imap.exmail.qq.com')
  assert.equal(ex.smtpHost, 'smtp.exmail.qq.com')
  assert.equal(ex.imapPort, 993)
  assert.equal(ex.smtpPort, 465)
})

test('真正无关的域名仍然退回 other（不能被企业邮规则误吞）', () => {
  assert.equal(inferProviderId('someone@gmail.com'), 'gmail')
  assert.equal(inferProviderId('someone@example.org'), 'other')
  // 子域也要能命中：kxpms.cn 的邮件子域同样是企业邮租户
  assert.equal(inferProviderId('someone@mail.kxpms.cn'), 'exmail')
})

test('qq and 163 use SSL SMTP 465 and require auth-code help URL', () => {
  const qq = providerById('qq')
  const wy = providerById('163')
  const ex = providerById('exmail')
  assert.equal(qq.smtpPort, 465)
  assert.equal(qq.imapHost, 'imap.qq.com')
  assert.equal(wy.smtpPort, 465)
  assert.equal(wy.imapHost, 'imap.163.com')
  assert.equal(ex.smtpHost, 'smtp.exmail.qq.com')
  assert.match(qq.authCodeUrl, /mail\.qq\.com/)
  assert.match(wy.authCodeUrl, /163\.com/)
  assert.match(ex.authCodeUrl, /exmail\.qq\.com/)
  assert.ok(qq.authCodeSteps.length >= 3)
})

test('catalog has the six user-facing providers', () => {
  assert.deepEqual(EMAIL_PROVIDERS.map((p) => p.id), [
    'exmail', 'qq', '163', 'gmail', 'outlook', 'other',
  ])
})

test('.local addresses are disposable test mirrors', () => {
  assert.equal(isLocalTestAddress('feikemanager@163.local'), true)
  assert.equal(isLocalTestAddress('someone@qq.com'), false)
})
