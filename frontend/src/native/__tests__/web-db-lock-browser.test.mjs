import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'
import { createServer } from 'vite'

test('Web SQLite hands over the persisted snapshot to one tab at a time', { timeout: 60_000 }, async t => {
  const vite = await createServer({
    root: fileURLToPath(new URL('../../../', import.meta.url)),
    configFile: false,
    optimizeDeps: { noDiscovery: true },
    server: { host: '127.0.0.1', port: 0, hmr: false },
    plugins: [{
      name: 'local-db-browser-fixture',
      configureServer(server) {
        server.middlewares.use('/__local_db', (_req, res) => {
          res.setHeader('Content-Type', 'text/html')
          res.end(`<!doctype html><body><script type="module">
            import { localDB } from '/src/native/local-db.ts'
            window.fixtureDB = localDB
          </script></body>`)
        })
      },
    }],
  })
  t.after(() => vite.close())
  await vite.listen()
  const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE })
  t.after(() => browser.close())
  t.diagnostic(`Chromium ${browser.version()}`)
  const context = await browser.newContext()
  const url = `http://127.0.0.1:${vite.httpServer.address().port}/__local_db`
  const openTab = async () => {
    const page = await context.newPage()
    page.setDefaultTimeout(15_000)
    await page.goto(url)
    await page.waitForFunction(() => Boolean(window.fixtureDB))
    return page
  }
  const first = await openTab()
  const second = await openTab()

  await first.evaluate(async () => {
    const db = window.fixtureDB
    await db.init('')
    await db.execute("CREATE TABLE browser_writes (id INTEGER PRIMARY KEY, title TEXT); INSERT INTO browser_writes VALUES(1,'old title');")
  })
  const tryOpen = page => page.evaluate(async () => {
    const db = window.fixtureDB
    try {
      await db.init('')
      return { ready: db.isReady() }
    } catch (error) {
      return { ready: db.isReady(), name: error.name, message: error.message }
    }
  })
  const busy = {
    ready: false,
    name: 'WebDatabaseLockError',
    message: '本地数据正在另一个标签页使用。请关闭该标签页后重试。',
  }
  assert.deepEqual(await tryOpen(second), busy)
  await first.evaluate(() => window.fixtureDB.run('UPDATE browser_writes SET title=? WHERE id=1', ['first tab edit']))

  const failedClose = await first.evaluate(async () => {
    const db = window.fixtureDB
    const actualClose = db.sqlite.closeConnection
    db.sqlite.closeConnection = async () => { throw new Error('injected close failure') }
    try {
      await db.close()
    } catch (error) {
      return { ready: db.isReady(), message: error.message }
    } finally {
      db.sqlite.closeConnection = actualClose
    }
  })
  assert.deepEqual(failedClose, { ready: false, message: 'injected close failure' })
  assert.deepEqual(await tryOpen(second), busy)

  await first.evaluate(() => window.fixtureDB.close())
  assert.deepEqual(await tryOpen(second), { ready: true })
  assert.deepEqual(await second.evaluate(() => window.fixtureDB.queryOne('SELECT title FROM browser_writes WHERE id=1')), {
    title: 'first tab edit',
  })
  await second.evaluate(() => window.fixtureDB.run('INSERT INTO browser_writes VALUES(2,?)', ['second tab edit']))
  await second.close()
  await first.waitForFunction(async () => !(await navigator.locks.query()).held.some(lock => lock.name === 'openpocket:sqlite:lobster'))
  assert.deepEqual(await tryOpen(first), { ready: true })
  assert.deepEqual(await first.evaluate(() => window.fixtureDB.query('SELECT id, title FROM browser_writes ORDER BY id')), [
    { id: 1, title: 'first tab edit' },
    { id: 2, title: 'second tab edit' },
  ])
  await first.evaluate(() => window.fixtureDB.close())

  const unsupported = await openTab()
  await unsupported.evaluate(() => Object.defineProperty(navigator, 'locks', { value: undefined }))
  assert.deepEqual(await tryOpen(unsupported), {
    ready: false,
    name: 'WebDatabaseLockError',
    message: '当前浏览器环境无法协调本地数据库，请在支持 Web Locks 的安全页面中打开。',
  })
})
