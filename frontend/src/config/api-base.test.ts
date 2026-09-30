import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  API_BASE_STORAGE_KEY,
  PRODUCTION_API_BASE,
  displayApiBase,
  isCapacitorShellOrigin,
  normalizeApiBase,
  persistApiBase,
  probeHealthz,
  readApiBaseOverride,
  resolveApiBase,
  resolveRuntimeApiBase,
} from './api-base.ts'

function memoryStorage(init: Record<string, string> = {}): Storage {
  const map = new Map(Object.entries(init))
  return {
    get length() {
      return map.size
    },
    clear() {
      map.clear()
    },
    getItem(key: string) {
      return map.has(key) ? map.get(key)! : null
    },
    key(index: number) {
      return [...map.keys()][index] ?? null
    },
    removeItem(key: string) {
      map.delete(key)
    },
    setItem(key: string, value: string) {
      map.set(key, String(value))
    },
  }
}

describe('normalizeApiBase', () => {
  it('trims, strips trailing slash, and keeps https origin', () => {
    assert.equal(normalizeApiBase('  https://pocket.itestu.cn/  '), 'https://pocket.itestu.cn')
  })

  it('maps same-origin URL to empty so requests stay relative', () => {
    assert.equal(
      normalizeApiBase('https://app.example/ ', 'https://app.example'),
      '',
    )
  })

  it('rejects non-http schemes', () => {
    assert.throws(() => normalizeApiBase('javascript:alert(1)'), /invalid-protocol/)
    assert.throws(() => normalizeApiBase('not a url'), /invalid-url/)
  })
})

describe('resolveApiBase', () => {
  it('prefers override over build default', () => {
    assert.equal(
      resolveApiBase({
        override: 'https://pocket.itestu.cn/',
        buildDefault: 'https://build.example',
      }),
      'https://pocket.itestu.cn',
    )
  })

  it('empty override does not swallow the build default', () => {
    // 取舍说明：真机 Capacitor 的 origin 是 https://localhost，「同源」等于本地壳，
    // /api 必然打不到后端（收信/归类会 Failed to fetch）。所以空串 override
    // 不清空构建默认值，而是继续用 buildDefault —— 代价是 ServerSelectView
    // 的「与页面同源」在有构建默认值时并非字面意义上的同源，预览由
    // previewServerBase 如实展示 buildDefault 而不是空地址。
    assert.equal(
      resolveApiBase({ override: '', buildDefault: 'https://build.example' }),
      'https://build.example',
    )
    assert.equal(resolveApiBase({ override: '', buildDefault: '' }), '')
  })

  it('missing override falls back to build default then origin', () => {
    assert.equal(
      resolveApiBase({ override: null, buildDefault: 'https://build.example/' }),
      'https://build.example',
    )
    assert.equal(resolveApiBase({ override: null, buildDefault: '' }), '')
  })
})

describe('displayApiBase', () => {
  it('shows page origin when resolved base is same-origin', () => {
    assert.equal(
      displayApiBase({ resolved: '', pageOrigin: 'https://app.example' }),
      'https://app.example',
    )
  })

  it('shows the resolved absolute base as-is', () => {
    assert.equal(
      displayApiBase({ resolved: 'https://pocket.itestu.cn', pageOrigin: 'https://localhost' }),
      'https://pocket.itestu.cn',
    )
  })
})

describe('persist and read override', () => {
  it('null clears the key so build default wins; empty string persists force-origin', () => {
    const storage = memoryStorage({ [API_BASE_STORAGE_KEY]: 'https://old.example' })
    persistApiBase(null, storage)
    assert.equal(readApiBaseOverride(storage), null)
    persistApiBase('', storage)
    assert.equal(readApiBaseOverride(storage), '')
    persistApiBase(PRODUCTION_API_BASE + '/', storage)
    assert.equal(readApiBaseOverride(storage), PRODUCTION_API_BASE)
  })
})

describe('isCapacitorShellOrigin / resolveRuntimeApiBase (BUG-J)', () => {
  // BUG-J 回归锁：BUG-F 引入 CAP_ANDROID_SCHEME=http 逃生舱后，页面 origin
  // 变成 http://localhost，而旧守卫只认 https://localhost，导致同源回退失效，
  // /api/* 全部落到 WebView 本地 index.html。
  it('recognizes every Capacitor shell scheme, not just https', () => {
    for (const origin of [
      'https://localhost',
      'http://localhost',
      'capacitor://localhost',
      'HTTPS://LOCALHOST',
    ]) {
      assert.equal(isCapacitorShellOrigin(origin), true, `expected shell origin: ${origin}`)
    }
  })

  it('does not treat a real backend or a port-bearing origin as the shell', () => {
    for (const origin of [
      'http://localhost:8088',
      'https://app.example',
      'http://192.168.31.20:8088',
      '',
      undefined,
      null,
    ]) {
      assert.equal(isCapacitorShellOrigin(origin as string), false, `unexpected shell origin: ${origin}`)
    }
  })

  it('falls back to the production base on an http Capacitor shell', () => {
    assert.equal(
      resolveRuntimeApiBase({ override: null, buildDefault: '', pageOrigin: 'http://localhost' }),
      PRODUCTION_API_BASE,
    )
  })

  it('keeps honoring an explicit base on an http Capacitor shell', () => {
    assert.equal(
      resolveRuntimeApiBase({
        override: 'http://localhost:8088',
        buildDefault: '',
        pageOrigin: 'http://localhost',
      }),
      'http://localhost:8088',
    )
  })

  it('stays same-origin for a plain web page with no configured base', () => {
    assert.equal(
      resolveRuntimeApiBase({ override: null, buildDefault: '', pageOrigin: 'https://app.example' }),
      '',
    )
  })
})

describe('probeHealthz', () => {  it('accepts HTTP 200 with body ok', async () => {
    const result = await probeHealthz('https://pocket.itestu.cn', async (input) => {
      assert.equal(String(input), 'https://pocket.itestu.cn/healthz')
      return new Response('ok', { status: 200 })
    })
    assert.deepEqual(result, { ok: true })
  })

  it('fails on non-ok body or network error', async () => {
    const badBody = await probeHealthz('https://pocket.itestu.cn', async () => {
      return new Response('nope', { status: 200 })
    })
    assert.equal(badBody.ok, false)
    const down = await probeHealthz('', async () => {
      throw new Error('failed to fetch')
    })
    assert.equal(down.ok, false)
    assert.match(down.error || '', /failed to fetch/)
  })
})
