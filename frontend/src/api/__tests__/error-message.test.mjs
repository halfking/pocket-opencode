/**
 * 错误消息本地化回归测试。
 *
 * 缺陷背景（真机实测）：RSS 页把后端原始错误 `rss_unavailable: store not
 * configured` 直接渲染给用户 —— 英文技术标识，且用户无法据此知道该做什么。
 *
 * Run: node --test src/api/__tests__/error-message.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { extractErrorCode, resolveErrorI18nKey, toUserMessage } from '../error-message.ts'

class FakeApiError extends Error {
  constructor(status, message, body) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

/** 模拟 i18n：只认识 errors.* 里我们显式登记的几个 key */
const dict = {
  'errors.rssNotConfigured': 'RSS 订阅源尚未配置，请先在「订阅」设置中添加源',
  'errors.notConfigured': '功能尚未完成配置',
  'errors.gatewayAdminMissing': '该网关节点尚未补录 Admin 账号，请先在「模型网关」的节点列表里填写 Admin 用户名和密码',
  'errors.unauthorized': '身份验证已失效，请重新登录',
  'errors.notFound': '找不到对应的内容',
  'errors.timeout': '请求超时，请稍后重试',
  'errors.network': '无法连接服务器，请检查网络',
  'errors.rateLimited': '操作过于频繁，请稍后再试',
  'errors.conflict': '内容已存在或状态冲突',
  'errors.server': '服务器开小差了，请稍后重试',
}
const t = (k) => dict[k] ?? k

describe('error code extraction', () => {
  it('从 "code: 说明" 里取出稳定的错误码', () => {
    assert.equal(extractErrorCode('rss_unavailable: store not configured'), 'rss_unavailable')
  })

  it('说明部分是自然语言时不误判为错误码', () => {
    assert.equal(extractErrorCode('something went wrong'), '')
    assert.equal(extractErrorCode('数据库连接失败'), '')
  })
})

describe('resolveErrorI18nKey', () => {
  it('命中已知错误码的精确映射', () => {
    assert.equal(
      resolveErrorI18nKey(new FakeApiError(503, 'rss_unavailable: store not configured')),
      'errors.rssNotConfigured',
    )
  })

  it('未登记的错误码按语义归类，不把原始串返回给 UI', () => {
    assert.equal(resolveErrorI18nKey(new Error('imap server said: 535 auth failed')), 'errors.unauthorized')
    assert.equal(resolveErrorI18nKey(new Error('Failed to fetch')), 'errors.network')
    assert.equal(resolveErrorI18nKey(new Error('deadline exceeded')), 'errors.timeout')
  })

  it('完全无法归类时返回 null，交由调用方用领域文案', () => {
    assert.equal(resolveErrorI18nKey(new Error('¯\\_(ツ)_/¯')), null)
  })

  it('从 body 里挖错误信息（后端 200/4xx 都可能把 error 放 body）', () => {
    assert.equal(
      resolveErrorI18nKey(new FakeApiError(400, 'Bad Request', { error: 'email_unavailable: no account' })),
      'errors.emailNotConfigured',
    )
  })
})

describe('toUserMessage', () => {
  it('原始错误码被换成可执行指引', () => {
    const msg = toUserMessage(
      new FakeApiError(503, 'rss_unavailable: store not configured'),
      t,
      '加载订阅源失败',
    )
    assert.equal(msg, 'RSS 订阅源尚未配置，请先在「订阅」设置中添加源')
    assert.ok(!msg.includes('rss_unavailable'))
    assert.ok(!msg.includes('not configured'))
  })

  it('i18n 缺词时回退到领域文案，而不是把 key 显示给用户', () => {
    const msg = toUserMessage(new Error('some_brand_new_error: 新错误码'), t, '加载订阅源失败')
    assert.equal(msg, '加载订阅源失败')
  })

  it('i18n 返回 key 本身时也回退（防止漏配语言包露出 errors.xxx）', () => {
    const emptyDict = () => 'errors.notConfigured'
    const msg = toUserMessage(new Error('not configured'), emptyDict, '加载订阅源失败')
    assert.equal(msg, '加载订阅源失败')
  })

  it('完全无法归类时用调用方文案', () => {
    assert.equal(toUserMessage(new Error('¯\\_(ツ)_/¯'), t, '加载订阅源失败'), '加载订阅源失败')
    assert.equal(toUserMessage(null, t, '加载订阅源失败'), '加载订阅源失败')
  })
})

// 以下三条承接 origin/main 的 composables/api-error-message.test.ts：
// 那套 resolveApiErrorMessage / shouldSurfaceRawMessage 已并入本模块，
// 契约从「5xx 不上屏、4xx 保留原消息」收紧为「原始消息一律不上屏」，
// 行为不变但覆盖面更广，这里把等价断言固定下来。
describe('HTTP 状态码下的原始消息处理', () => {
  it('5xx 的内部细节不上屏（pq: duplicate key 属后端实现细节）', () => {
    const msg = toUserMessage(new FakeApiError(500, 'pq: duplicate key'), t, '保存失败')
    assert.equal(msg, '保存失败')
  })

  it('502 上游 HTML 片段不上屏', () => {
    const msg = toUserMessage(new FakeApiError(502, '<html>bad gateway</html>'), t, '保存失败')
    assert.equal(msg, '保存失败')
  })

  it('4xx 也不直接上屏，走调用方领域文案', () => {
    const msg = toUserMessage(new FakeApiError(409, 'deck name taken'), t, '保存失败')
    assert.equal(msg, '保存失败')
  })
})

// 网关节点缺 admin 账号（2026-10-02 真机实测）。
//
// 缺陷：后端 gatewayAdminClient.login 对「节点没配 admin 账号」返回
//   502 {"error":"node \"default\" has no admin credentials configured"}
// 这句**不匹配任何既有语义规则**（没有 "not configured"、没有
// "invalid credential"、正文里也没有状态码），resolveErrorI18nKey 因此返回
// null，/gateway/{id}/{providers,credentials,models} 三页统一显示
// 「加载网关信息失败」——一个完全不可行动的死胡同。
//
// 下面这组用例直接钉住后端真实报文（不是构造的近似串），并用反向用例证明
// 新规则没有把「凭据非法」这类语义相近的错误一起抢走。
describe('网关节点缺 admin 账号', () => {
  // 与 llm_gateway_admin_client.go:132 的 fmt.Errorf 逐字一致
  const REAL = 'node "default" has no admin credentials configured'

  it('后端真实报文被归类到 gatewayAdminMissing（修复前返回 null）', () => {
    assert.equal(resolveErrorI18nKey(new FakeApiError(502, 'Bad Gateway', { error: REAL })),
      'errors.gatewayAdminMissing')
  })

  it('文案指向"去补录 Admin 账号"这个具体动作，而不是通用失败', () => {
    const msg = toUserMessage(new FakeApiError(502, 'Bad Gateway', { error: REAL }), t, '加载网关信息失败')
    assert.notEqual(msg, '加载网关信息失败')
    assert.ok(msg.includes('Admin'), `文案必须点名 Admin 账号，实际为：${msg}`)
    assert.ok(!msg.includes('gatewayAdminMissing'), '不能把 i18n key 漏给用户')
  })

  it('节点名是变量：换个节点名同样命中（后端是 fmt 的 %q）', () => {
    assert.equal(
      resolveErrorI18nKey(new FakeApiError(502, 'Bad Gateway',
        { error: 'node "hk-prod-1" has no admin credentials configured' })),
      'errors.gatewayAdminMissing')
  })

  it('反向用例：凭据被拒仍归 unauthorized，不能被新规则抢走', () => {
    assert.equal(
      resolveErrorI18nKey(new FakeApiError(401, 'Unauthorized', { error: 'invalid credential' })),
      'errors.unauthorized')
  })

  it('反向用例：真正的 store 未配置仍归 notConfigured', () => {
    assert.equal(
      resolveErrorI18nKey(new Error('rss_unavailable: store not configured')),
      'errors.rssNotConfigured')
  })
})
