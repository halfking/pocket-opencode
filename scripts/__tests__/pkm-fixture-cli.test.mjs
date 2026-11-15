import { test } from 'node:test'
import assert from 'node:assert/strict'
import { DatabaseSync } from 'node:sqlite'
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const execute = promisify(execFile)
const cli = new URL('../pkm-test-fixture.mjs', import.meta.url)
const seed = [
  ['test-a', 'owner-a', 'MaestroPKMNote', 'note'],
  ['legacy-a', 'owner-a', 'MaestroPKM笔记', 'note'],
  ['foreign-b', 'owner-b', 'MaestroPKMNote', 'note'],
  ['untitled-a', 'owner-a', '无标题', 'note'],
  ['personal-a', 'owner-a', 'MaestroPersonal', 'note'],
  ['other-kind', 'owner-a', 'MaestroPKMNote', 'task'],
]

async function probe({ dry = false, workspace = 'owner-a', authorized = true, revokeAfter = '', appId = 'com.example.pocket.audit' } = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'pkm-fixture-cli-'))
  const database = join(directory, 'fixture.db'), log = join(directory, 'package.txt')
  const db = new DatabaseSync(database)
  db.exec('CREATE TABLE local_assets (id TEXT, workspace_id TEXT, title TEXT, kind TEXT)')
  const insert = db.prepare('INSERT INTO local_assets VALUES (?, ?, ?, ?)')
  for (const row of seed) insert.run(...row)
  db.close()
  const preload = join(directory, 'preload.mjs')
  await writeFile(preload, `
    import { registerHooks } from 'node:module'
    import { DatabaseSync } from 'node:sqlite'
    import { writeFileSync } from 'node:fs'
    import { runInNewContext } from 'node:vm'
    const db = new DatabaseSync(process.env.PKM_FIXTURE_DB)
    let revoked = false
    const adapter = {
      all: async (sql, args = []) => {
        const rows = db.prepare(sql).all(...args)
        if (process.env.PKM_FIXTURE_REVOKE_AFTER === 'select') revoked = true
        return rows
      },
      run: async (sql, args = []) => db.prepare(sql).run(...args),
    }
    const origin = {
      workspaceId: JSON.parse(process.env.PKM_FIXTURE_OWNER),
      assertCurrent: () => { if (revoked) throw new Error('STALE_AUTH_SESSION') },
    }
    const stores = new Map([
      ['auth', { workspaceId: JSON.parse(process.env.PKM_FIXTURE_OWNER) }],
      ['connectivity', { runtime: { deps: {
        auth: () => process.env.PKM_FIXTURE_AUTHORIZED === 'true' ? origin : null,
        db: async bound => {
          if (process.env.PKM_FIXTURE_REVOKE_AFTER === 'db') revoked = true
          return bound === origin ? adapter : null
        },
      } } }],
    ])
    globalThis.__pkmFixtureOpen = async ({ pkg }) => {
      writeFileSync(process.env.PKM_FIXTURE_LOG, pkg)
      return {
        ev: expression => runInNewContext(expression, { document: { querySelector: () => ({
          __vue_app__: { config: { globalProperties: { $pinia: { _s: stores } } } },
        }) } }),
        close: async () => db.close(),
      }
    }
    registerHooks({ resolve(specifier, context, next) {
      if (specifier === './lib/adb-cdp.mjs' && context.parentURL === ${JSON.stringify(cli.href)}) {
        return { url: 'data:text/javascript,export const openCdp = globalThis.__pkmFixtureOpen', shortCircuit: true }
      }
      return next(specifier, context)
    } })
  `)
  try {
    let result
    try {
      result = await execute(process.execPath, ['--import', preload, cli.pathname, ...(dry ? ['--dry'] : [])], {
        timeout: 5000, env: { ...process.env, POCKET_APP_ID: appId, PKM_FIXTURE_DB: database,
          PKM_FIXTURE_OWNER: JSON.stringify(workspace), PKM_FIXTURE_AUTHORIZED: String(authorized),
          PKM_FIXTURE_REVOKE_AFTER: revokeAfter, PKM_FIXTURE_LOG: log },
      })
    } catch (error) { result = error }
    const inspect = new DatabaseSync(database)
    const ids = inspect.prepare('SELECT id FROM local_assets ORDER BY id').all().map(row => row.id)
    inspect.close()
    return { result, ids, pkg: await readFile(log, 'utf8').catch(() => '') }
  } finally { await rm(directory, { recursive: true, force: true }) }
}

test('PKM fixture targets the chosen package and deletes only exact test notes in the active workspace', async () => {
  const { result, ids, pkg } = await probe()
  assert.equal(result.code, undefined, result.stderr)
  assert.equal(pkg, 'com.example.pocket.audit')
  assert.deepEqual(ids, ['foreign-b', 'other-kind', 'personal-a', 'untitled-a'])
})

test('PKM fixture dry run preserves all rows and reports only scoped test notes', async () => {
  const { result, ids } = await probe({ dry: true })
  assert.equal(result.code, undefined, result.stderr)
  assert.equal(ids.length, seed.length)
  assert.deepEqual(JSON.parse(result.stdout).matched.map(row => row.id).sort(), ['legacy-a', 'test-a'])
})

test('PKM fixture refuses an unknown workspace without deleting rows', async () => {
  const { result, ids } = await probe({ workspace: null })
  assert.equal(result.code, 1)
  assert.match(result.stdout, /WORKSPACE_NOT_READY/)
  assert.equal(ids.length, seed.length)
})

test('PKM fixture rejects an invalid package before opening CDP', async () => {
  const { result, ids, pkg } = await probe({ appId: 'com.example; invalid' })
  assert.equal(result.code, 2)
  assert.equal(pkg, '')
  assert.equal(ids.length, seed.length)
})

test('PKM fixture refuses a missing runtime auth lease without deleting rows', async () => {
  const { result, ids } = await probe({ authorized: false })
  assert.equal(result.code, 1)
  assert.match(result.stdout, /WORKSPACE_NOT_READY/)
  assert.equal(ids.length, seed.length)
})

for (const boundary of ['db', 'select']) {
  test(`PKM fixture refuses a revoked session after ${boundary} without deleting rows`, async () => {
    const { result, ids } = await probe({ revokeAfter: boundary })
    assert.equal(result.code, 1)
    assert.match(result.stdout, /STALE_AUTH_SESSION/)
    assert.equal(ids.length, seed.length)
  })
}
