import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  API_BASE_STORAGE_KEY,
  PRODUCTION_API_BASE,
  displayApiBase,
  normalizeApiBase,
  persistApiBase,
  probeHealthz,
  readApiBaseOverride,
  resolveApiBase,
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

  it('empty override forces same-origin even when build default exists', () => {
    assert.equal(
      resolveApiBase({ override: '', buildDefault: 'https://build.example' }),
      '',
    )
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

describe('probeHealthz', () => {
  it('accepts HTTP 200 with body ok', async () => {
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
