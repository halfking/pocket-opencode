/**
 * BUG-F 明文后端构建守卫的回归测试。
 *
 * 为什么要有这个测试：守卫本身是「失败即中止构建」的脚本，一旦有人为了
 * 让流水线变绿把它改软（比如只 warn 不 exit 1），没有任何测试会发现。
 * 这里把判定逻辑（纯函数）钉住。
 *
 * Run: node --test src/config/__tests__/assert-no-plaintext-backend.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { checkApiBase, isLoopbackHost } from '../../../scripts/assert-no-plaintext-backend.mjs'

describe('isLoopbackHost', () => {
  it('本机与模拟器回环算 loopback', () => {
    for (const h of ['localhost', '127.0.0.1', '127.1.2.3', '10.0.2.2', '::1', 'foo.local']) {
      assert.equal(isLoopbackHost(h), true, h)
    }
  })
  it('局域网 IP / 域名不算 loopback', () => {
    for (const h of ['192.168.31.20', '10.1.2.3', 'pocket.kxpms.cn', 'llm.kxpms.cn']) {
      assert.equal(isLoopbackHost(h), false, h)
    }
  })
})

describe('checkApiBase（BUG-F 判定）', () => {
  it('https 一律放行', () => {
    assert.equal(checkApiBase('https://pocket.kxpms.cn').offending, false)
    assert.equal(checkApiBase('https://pocket.kxpms.cn/v1').offending, false)
  })

  it('未设置 → 放行（同源相对路径）', () => {
    assert.equal(checkApiBase('').offending, false)
    assert.equal(checkApiBase(undefined).offending, false)
  })

  it('非本机明文 http → 拦截（这正是 BUG-F 的形态）', () => {
    const r = checkApiBase('http://192.168.31.20:8088')
    assert.equal(r.offending, true)
    assert.match(r.reason, /非本机的明文 http/)
  })

  it('本机明文 http → 放行（联调常态）', () => {
    assert.equal(checkApiBase('http://127.0.0.1:8088').offending, false)
    assert.equal(checkApiBase('http://localhost:8088').offending, false)
    assert.equal(checkApiBase('http://10.0.2.2:8088').offending, false)
  })

  it('非法 URL 与非 http(s) scheme 一律拦截', () => {
    assert.equal(checkApiBase('not a url').offending, true)
    assert.equal(checkApiBase('ws://pocket.kxpms.cn').offending, true)
    assert.equal(checkApiBase('file:///etc/passwd').offending, true)
  })
})
