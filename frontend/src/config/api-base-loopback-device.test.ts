/**
 * 设备可达性：构建默认值里的 loopback 在 Capacitor 壳上不得生效。
 *
 * 缺陷现场（2026-10-02）：装机包把 VITE_API_BASE=http://localhost:18099 烘进了
 * 产物。设备上的 localhost 是**手机自己**，不是后端。此前 App 在模拟器上一切正常，
 * 只是因为一直挂着 `adb reverse host-22 tcp:18099 tcp:18099` 这根开发拐杖；
 * 真机上它不存在，整个 App（含邮箱配置）连不上任何后端。
 *
 * 对照实验（模拟器，已 `adb reverse --remove-all`）：
 *   device -> localhost:18099   nc: connect: Connection refused
 *   device -> 10.0.2.2:18099     HTTP/1.0 200 OK ... ok
 *
 * 修法只针对**构建默认值**：用户在「后端服务器」页显式填的 localhost 仍然尊重，
 * 因为 `adb reverse` 开发流本来就需要用户主动指定它。
 *
 * Run: node --experimental-strip-types --test src/config/api-base-loopback-device.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  API_BASE_STORAGE_KEY,
  PRODUCTION_API_BASE,
  displayApiBase,
  isLoopbackApiBase,
  resolveApiBase,
  resolveApiBaseWithSource,
  resolveRuntimeApiBase,
} from './api-base.ts'
import { previewServerBase, resolveServerSave } from '../features/servers/server-select-logic.ts'

/** 装机包的页面 origin：capacitor.config.ts 的 androidScheme 默认 https。 */
const SHELL = 'https://localhost'
/** BUG-F 逃生舱（CAP_ANDROID_SCHEME=http）下的壳 origin。 */
const HTTP_SHELL = 'http://localhost'
/** 这次出事的构建默认值。 */
const LOCALHOST_BUILD = 'http://localhost:18099'

function memoryStorage(init: Record<string, string> = {}): Storage {
  const map = new Map(Object.entries(init))
  return {
    get length() {
      return map.size
    },
    clear() {
      map.clear()
    },
    getItem: (k) => (map.has(k) ? map.get(k)! : null),
    key: (i) => [...map.keys()][i] ?? null,
    removeItem: (k) => map.delete(k),
    setItem: (k, v) => map.set(k, String(v)),
  }
}

describe('isLoopbackApiBase', () => {
  it('识别全部 loopback 主机名（含整个 127/8 与 IPv6）', () => {
    for (const base of [
      'http://localhost:18099',
      'https://localhost',
      // 带端口的 localhost 主机名依然是 loopback；Web 开发页不被这条规则保护，
      // 它靠的是「页面 origin 带端口 ⇒ 不是 Capacitor 壳」那一条。
      'http://localhost:4175',
      'http://127.0.0.1:8088',
      'http://127.1.2.3:8088',
      'http://[::1]:18099',
      'http://0.0.0.0:18099',
    ]) {
      assert.equal(isLoopbackApiBase(base), true, `expected loopback: ${base}`)
    }
  })

  it('真实可达的地址不算 loopback', () => {
    for (const base of [
      // 模拟器宿主别名：设备访问宿主的正确写法（真机无效，所以不能算 loopback）
      'http://10.0.2.2:18099',
      'http://192.168.31.20:8088',
      'https://pocket.itestu.cn',
      '',
      null,
      undefined,
      'not a url',
    ]) {
      assert.equal(isLoopbackApiBase(base as string), false, `unexpected loopback: ${base}`)
    }
  })
})

describe('构建默认值里的 loopback 在设备上被丢弃', () => {
  it('Capacitor 壳 + loopback 构建默认值 → 换用生产入口并标记', () => {
    const r = resolveApiBaseWithSource({ override: null, buildDefault: LOCALHOST_BUILD, pageOrigin: SHELL })
    assert.equal(r.base, PRODUCTION_API_BASE)
    assert.equal(r.loopbackBuildRejected, true)
    assert.equal(r.source, 'build')
  })

  it('http 壳同样成立（BUG-F 逃生舱不能成为绕过口子）', () => {
    const r = resolveApiBaseWithSource({ override: null, buildDefault: LOCALHOST_BUILD, pageOrigin: HTTP_SHELL })
    assert.equal(r.base, PRODUCTION_API_BASE)
    assert.equal(r.loopbackBuildRejected, true)
  })

  it('显式选同源（override 空串）在设备上也落到生产入口，而不是 localhost', () => {
    const r = resolveApiBaseWithSource({ override: '', buildDefault: LOCALHOST_BUILD, pageOrigin: SHELL })
    assert.equal(r.base, PRODUCTION_API_BASE)
    assert.equal(r.loopbackBuildRejected, true)
  })

  it('resolveRuntimeApiBase / displayApiBase 与解析口径一致', () => {
    assert.equal(
      resolveRuntimeApiBase({ override: null, buildDefault: LOCALHOST_BUILD, pageOrigin: SHELL }),
      PRODUCTION_API_BASE,
    )
    assert.equal(
      displayApiBase({ resolved: resolveApiBase({ override: null, buildDefault: LOCALHOST_BUILD, pageOrigin: SHELL }) }),
      PRODUCTION_API_BASE,
    )
  })
})

describe('不该被这条规则波及的路径', () => {
  it('构建默认值本身可达时原样生效（生产域名 / 局域网 IP）', () => {
    for (const buildDefault of ['https://pocket.itestu.cn', 'http://192.168.31.45:8088', 'http://10.0.2.2:18099']) {
      const r = resolveApiBaseWithSource({ override: null, buildDefault, pageOrigin: SHELL })
      assert.equal(r.base, buildDefault, `should keep: ${buildDefault}`)
      assert.equal(r.loopbackBuildRejected, undefined, `should not flag: ${buildDefault}`)
    }
  })

  it('Web 页面（origin 带端口，不是 Capacitor 壳）上 loopback 构建默认值照常生效', () => {
    // vite dev 页与后端同源时收成 ''，走相对 /api——不是被这条规则改掉的。
    const sameOrigin = resolveApiBaseWithSource({
      override: null,
      buildDefault: 'http://localhost:4175',
      pageOrigin: 'http://localhost:4175',
    })
    assert.equal(sameOrigin.base, '')
    assert.equal(sameOrigin.loopbackBuildRejected, undefined)

    // 页面与构建默认值不同源（本地起前端 + 另一个端口的后端）时必须原样保留。
    const other = resolveApiBaseWithSource({
      override: null,
      buildDefault: 'http://localhost:18099',
      pageOrigin: 'http://localhost:4175',
    })
    assert.equal(other.base, LOCALHOST_BUILD)
    assert.equal(other.loopbackBuildRejected, undefined)
  })

  it('用户显式填的 localhost 仍被尊重（adb reverse 开发流依赖它）', () => {
    const r = resolveApiBaseWithSource({ override: LOCALHOST_BUILD, buildDefault: '', pageOrigin: SHELL })
    assert.equal(r.base, LOCALHOST_BUILD)
    assert.equal(r.loopbackBuildRejected, undefined)
    assert.equal(r.source, 'override')
  })

  it('自定义的非 loopback 地址不受影响', () => {
    const storage = memoryStorage({ [API_BASE_STORAGE_KEY]: 'http://192.168.31.20:8088' })
    assert.equal(resolveApiBase({ storage, buildDefault: LOCALHOST_BUILD, pageOrigin: SHELL }), 'http://192.168.31.20:8088')
  })

  it('真机可达的构建默认值 + 显式自定义地址时，仍以自定义为准', () => {
    const storage = memoryStorage({ [API_BASE_STORAGE_KEY]: 'http://192.168.31.20:8088' })
    const r = resolveApiBaseWithSource({ storage, buildDefault: LOCALHOST_BUILD, pageOrigin: SHELL })
    assert.equal(r.base, 'http://192.168.31.20:8088')
    assert.equal(r.loopbackBuildRejected, undefined)
  })
})

describe('服务器选择页的预览不许骗人', () => {
  it('预览与落盘后的实际生效地址一致（build 档，设备上）', () => {
    const storage = memoryStorage()
    const choice = { kind: 'build' as const, custom: '' }
    const preview = previewServerBase(choice, LOCALHOST_BUILD, SHELL)
    const outcome = resolveServerSave(choice, { buildDefault: LOCALHOST_BUILD, pageOrigin: SHELL, storage })
    assert.equal(preview, PRODUCTION_API_BASE, '预览必须是设备上真正会用的地址')
    assert.equal(outcome.resolved, preview)
    assert.equal(outcome.loopbackBuildRejected, true)
    // build 档本来就要删掉 override key，所以 fellBackToOrigin=true 是正常语义；
    // 保存页只对 custom 档拿这个字段报错。
    assert.equal(outcome.persistValue, null)
    assert.equal(storage.getItem(API_BASE_STORAGE_KEY), null)
  })

  it('同源档在设备上也不预览成 localhost', () => {
    assert.equal(previewServerBase({ kind: 'origin', custom: '' }, LOCALHOST_BUILD, SHELL), PRODUCTION_API_BASE)
  })

  it('可达的构建默认值下预览保持原样（没有无谓的改写）', () => {
    assert.equal(
      previewServerBase({ kind: 'build', custom: '' }, 'https://pocket.itestu.cn', SHELL),
      'https://pocket.itestu.cn',
    )
    assert.equal(previewServerBase({ kind: 'build', custom: '' }, LOCALHOST_BUILD, 'http://localhost:4175'), LOCALHOST_BUILD)
  })
})

/**
 * 接线护栏：上面的用例全是纯函数层面的，挡不住「UI 不再提示」和
 * 「展示口径被改回旧写法」这两类回归。必须 stripComments 再匹配，
 * 否则会被注释里出现的同名符号满足（这条护栏第一版就被自己的注释骗过一次）。
 */
function readSource(rel: string): string {
  return readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8')
}

/** 去掉块注释与行注释，避免注释里的符号把断言喂饱。 */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/.*$/gm, '$1')
}

describe('接线护栏', () => {
  it('displayApiBase 必须走 resolveRuntimeApiBase（否则登录页显示的不是实际地址）', () => {
    const src = stripComments(readSource('./api-base.ts'))
    const body = src.slice(src.indexOf('export function displayApiBase'))
    assert.match(body, /resolveRuntimeApiBase\(\)/)
    assert.doesNotMatch(body, /\?\?\s*resolveApiBase\(\)/)
  })

  it('ServerSelectView 必须渲染 loopback 不可达提示', () => {
    const src = stripComments(readSource('../features/servers/ServerSelectView.vue'))
    assert.match(src, /resolveApiBaseWithSource\(\{\s*override:\s*null/)
    assert.match(src, /loopbackBuildRejected\s*===\s*true/)
    // 模板里真的把它渲染出来了，而不只是算了个变量
    assert.match(src, /v-if="buildDefaultNotice"[\s\S]{0,80}buildDefaultNotice/)
    assert.match(src, /settings\.buildDefaultUnreachable/)
  })

  it('9 个语言包都必须有 buildDefaultUnreachable（漏一个就是某语言显示原始 key）', () => {
    const dir = fileURLToPath(new URL('../locales/', import.meta.url))
    const langs = readdirSync(dir).filter((f) => f.endsWith('.json'))
    assert.equal(langs.length, 9, `预期 9 个语言包，实际 ${langs.length}`)
    for (const f of langs) {
      const parsed = JSON.parse(readFileSync(join(dir, f), 'utf8')) as {
        settings?: Record<string, string>
      }
      const v = parsed.settings?.buildDefaultUnreachable
      assert.ok(v, `${f} 缺 settings.buildDefaultUnreachable`)
      assert.match(v, /\{url\}/, `${f} 的文案缺少 {url} 占位`)
      assert.match(v, /\{fallback\}/, `${f} 的文案缺少 {fallback} 占位`)
    }
  })
})
