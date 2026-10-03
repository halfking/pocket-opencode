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

  it('uses explicit same-origin on Web but keeps the backend on a Capacitor shell', () => {
    assert.equal(
      resolveApiBase({ override: '', buildDefault: 'https://build.example', pageOrigin: 'https://app.example' }),
      '',
    )
    assert.equal(
      resolveApiBase({ override: '', buildDefault: 'https://build.example', pageOrigin: 'http://localhost' }),
      'https://build.example',
    )
    assert.equal(resolveApiBase({ override: '', buildDefault: '', pageOrigin: 'https://localhost' }), '')
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

describe('probeHealthz', () => {
  it('accepts HTTP 200 with body ok', async () => {
    const result = await probeHealthz('https://pocket.itestu.cn', async (input) => {
      assert.equal(String(input), 'https://pocket.itestu.cn/healthz')
      return new Response('ok', { status: 200 })
    })
    assert.deepEqual(result, { ok: true })
  })

  it('checks the API through the same-origin proxy when /healthz is only the frontend', async () => {
    const requested: string[] = []
    const result = await probeHealthz('http://localhost:4175/', async (input) => {
      requested.push(String(input))
      return requested.length === 1
        ? new Response('frontend ok', { status: 200 })
        : new Response('ok', { status: 200 })
    })
    assert.deepEqual(requested, [
      'http://localhost:4175/healthz',
      'http://localhost:4175/api/healthz',
    ])
    assert.deepEqual(result, { ok: true })
  })

  it('reports a failed API behind a healthy frontend', async () => {
    const result = await probeHealthz('http://localhost:4175', async (input) =>
      String(input).endsWith('/api/healthz')
        ? new Response('upstream down', { status: 502 })
        : new Response('frontend ok', { status: 200 }),
    )
    assert.deepEqual(result, { ok: false, error: 'HTTP 502' })
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
