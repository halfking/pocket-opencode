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
import { execFileSync } from 'node:child_process'
import { describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { checkApiBase, checkSchemePair, isLoopbackHost, SCHEME_FIX_HINT } from '../../../scripts/assert-no-plaintext-backend.mjs'

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

/**
 * 第二轴：后端协议 × 壳 scheme。
 *
 * 这条轴的由来：2026-10-08 实测发现「明文 http 指向模拟器回环」这一档按设计放行，
 * 而壳 scheme 仍取默认 https，于是 `vite build` 全绿、`cap sync` 全绿，
 * 产出的 APK 实时通道（ws://）却被 Chromium>=111 硬阻断。
 * 两轴互不相干，缺一不可。
 */
describe('checkSchemePair（BUG-F 第二轴）', () => {
  it('明文 http 后端 + https 壳 → 拦截（实测那份产物就是这个组合）', () => {
    const r = checkSchemePair('http://10.0.2.2:8124', 'https')
    assert.equal(r.offending, true)
    assert.equal(r.relevant, true)
    assert.match(r.reason, /硬阻断/)
  })

  it('明文 http 后端 + http 壳 → 放行（这才是联调该有的组合）', () => {
    const r = checkSchemePair('http://10.0.2.2:8124', 'http')
    assert.equal(r.offending, false)
    assert.equal(r.relevant, true)
  })

  it('未传 scheme 时按 capacitor.config.ts 的缺省 https 处理', () => {
    assert.equal(checkSchemePair('http://localhost:8088').offending, true)
    assert.equal(checkSchemePair('http://localhost:8088', undefined).offending, true)
  })

  it('https/wss 后端 → 这一轴不适用（别把规则写成「壳是 https 就拦」）', () => {
    for (const base of ['https://pocket.kxpms.cn', 'https://pocket.kxpms.cn/v1']) {
      const r = checkSchemePair(base, 'https')
      assert.equal(r.relevant, false, base)
      assert.equal(r.offending, false, base)
    }
  })

  it('未设置后端 / 非法 URL → 这一轴不适用，交给第一轴报（避免一条问题两行红）', () => {
    assert.equal(checkSchemePair('', 'https').relevant, false)
    assert.equal(checkSchemePair(undefined, 'https').relevant, false)
    assert.equal(checkSchemePair('not a url', 'https').relevant, false)
  })

  it('非 http/https 的壳值 → 拦截（CapConfig.validateScheme 对它只 warn 不拦）', () => {
    for (const s of ['HTTP', 'ws', 'ftp', '']) {
      assert.equal(checkSchemePair('http://10.0.2.2:8124', s).offending, true, s)
    }
  })

  it('修法文案必须指向 cap sync，且点明 vite build 改不了这一轴', () => {
    assert.match(SCHEME_FIX_HINT, /cap sync/)
    assert.match(SCHEME_FIX_HINT, /vite build 改不了/)
  })
})

/**
 * CLI 层：真起子进程跑守卫脚本。
 *
 * 为什么纯函数测试不够：单测只钉 `checkSchemePair` 的判定，接线（`main()` 里
 * 有没有把第二轴算进 `bad`）是另一处代码，纯函数被改软或接线被回退都不会让
 * 单测变红。守卫本身是「红即中止构建」的脚本，接线一掉，缺陷立刻静默复发。
 */
describe('守卫 CLI 的第二轴接线', () => {
  const SCRIPT = new URL('../../../scripts/assert-no-plaintext-backend.mjs', import.meta.url)
// ★ 用 fileURLToPath 而不是 SCRIPT.pathname（2026-10-22 Windows 实测修掉）：
//   URL 的 pathname 在 Windows 上是 '/C:/workspace/...'（**带前导斜杠**的 URL 形态），
//   直接当路径用 ⇒ 拼出 'C:\C:\workspace\...' ⇒ node 报
//   「Cannot find module 'C:\C:\workspace\...\'」⇒ 这道门整个瘫掉。
//   ⇒ Linux 上 pathname = '/workspace/...' 正常 ⇒ **Linux 一直绿**。
//   ⇒ fileURLToPath 给的是**真文件系统路径**（Windows: 'C:\workspace\...'）。

  /** 起一个干净 env：先抹掉所有 VITE_ 变量，避免外层 shell 污染判定。 */
  function runEnv(vars) {
    const env = { ...process.env }
    for (const k of Object.keys(env)) {
      if (/^VITE_/.test(k) || k.startsWith('CAP_ANDROID_SCHEME') || k === 'POCKET_ALLOW_PLAINTEXT_API') {
        delete env[k]
      }
    }
    return { ...env, ...vars }
  }

  function run(vars) {
    try {
      const stdout = execFileSync(process.execPath, [fileURLToPath(SCRIPT)], {
        env: runEnv(vars),
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
      })
      return { code: 0, stdout }
    } catch (e) {
      return { code: e.status ?? -1, stdout: `${e.stdout ?? ''}${e.stderr ?? ''}` }
    }
  }

  it('明文后端 + https 壳（未配 CAP_ANDROID_SCHEME）→ 退出码非 0，且修法指向 cap sync', () => {
    const r = run({ VITE_API_BASE: 'http://10.0.2.2:8124' })
    assert.notEqual(r.code, 0, '这正是 2026-10-08 那次构建的实参，必须拦下')
    assert.match(r.stdout, /cap sync/)
  })

  it('同一后端 + CAP_ANDROID_SCHEME=http → 退出码 0（配套组合不该被拦）', () => {
    const r = run({ VITE_API_BASE: 'http://10.0.2.2:8124', CAP_ANDROID_SCHEME: 'http' })
    assert.equal(r.code, 0, r.stdout)
    assert.match(r.stdout, /守卫通过/)
  })

  it('https 后端 → 退出码 0，且第二轴一栏都不出现', () => {
    const r = run({ VITE_API_BASE: 'https://pocket.kxpms.cn' })
    assert.equal(r.code, 0, r.stdout)
    assert.doesNotMatch(r.stdout, /第二轴/)
  })

  it('不设任何后端地址 → 退出码 0（这条门禁不能给普通构建添噪音）', () => {
    const r = run({})
    assert.equal(r.code, 0, r.stdout)
    assert.match(r.stdout, /守卫通过/)
  })

  it('逃生舱仍只对第一轴生效：非本机明文 + scheme 配套，也仍然红', () => {
    const r = run({
      VITE_API_BASE: 'http://192.168.31.20:8088',
      CAP_ANDROID_SCHEME: 'http',
      POCKET_ALLOW_PLAINTEXT_API: '1',
    })
    assert.equal(r.code, 0, '开了逃生舱就放行，这是既有语义')
    const r2 = run({ VITE_API_BASE: 'http://192.168.31.20:8088', CAP_ANDROID_SCHEME: 'http' })
    assert.notEqual(r2.code, 0, '没开逃生舱时非本机明文必须红')
  })
})
