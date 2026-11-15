import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { chromium } from 'playwright'

async function until(predicate) {
  const deadline = Date.now() + 3000
  while (!predicate()) { assert.ok(Date.now() < deadline, 'HTTP condition not reached'); await delay(10) }
}

async function fixture(t, detail = false, options = {}) {
  const streams = []
  const details = [], cancels = [], polls = []
  const errors = []
  let cancelStatus = 404
  let holdDetails = false, holdCancels = false, pollStatus = 404
  let cancellations = 0
  const detailReply = (request, projectId = 'Initial private detail', status = 200) => {
    request.res.writeHead(status, { 'Content-Type': 'application/json' })
    request.res.end(JSON.stringify(status === 200
      ? { run_id: request.runId, project_id: projectId, status: 'running', nodes: [] }
      : { message: projectId }))
  }
  const cancelReply = (request, status = cancelStatus) => {
    request.res.writeHead(status, { 'Content-Type': 'application/json' })
    request.res.end(JSON.stringify(status === 200 ? { ok: true } : { message: 'unsupported' }))
  }
  const entry = `
    import {createApp,h,ref} from 'vue';
    import {createPinia,setActivePinia} from 'pinia';
    import {createRouter,createMemoryHistory} from 'vue-router';
    import {createI18n} from 'vue-i18n';
    import {useAuthStore} from '/src/stores/auth.ts';
    import {setAccBase,setAccToken} from '/src/api/accApi.ts';
    import LogStream from '/src/features/acc-board/LogStream.vue';
    import Detail from '/src/features/acc-board/TaskDetailView.vue';
    import {getAccLogSession} from '/src/features/acc-board/acc-log-session.ts';
    const pinia=createPinia();setActivePinia(pinia);
    const auth=useAuthStore();
    auth.$patch({token:'pocket-fixture',user:'Alice',userId:'alice',workspaceId:'one',activationStatus:'ready'});
    setAccBase(location.origin+'/acc-a');setAccToken('acc-fixture-a');
    const session=getAccLogSession(auth);
    const visible=ref(true);
    const router=createRouter({history:createMemoryHistory(),routes:[{path:'/runs/:runId',component:Detail}]});
    await router.push('/runs/run-1');await router.isReady();
    const app=createApp({setup(){return()=>h('div',[
      h('button',{id:'toggle',onClick:()=>visible.value=!visible.value},'Toggle log view'),
      h('button',{id:'workspace',onClick:()=>{auth.activationStatus='pending';auth.workspaceId='two';auth.activationStatus='ready'}},'Switch workspace'),
      h('button',{id:'account',onClick:()=>{auth.activationStatus='pending';auth.userId='bob';auth.user='Bob';auth.activationStatus='ready'}},'Switch account'),
      h('button',{id:'restore-account',onClick:()=>{auth.activationStatus='pending';auth.userId='alice';auth.user='Alice';auth.workspaceId='one';auth.activationStatus='ready'}},'Restore Alice'),
      h('button',{id:'run-two',onClick:()=>router.push('/runs/run-2')},'Run two'),
      h('button',{id:'run-one',onClick:()=>router.push('/runs/run-1')},'Run one'),
      h('button',{id:'credential',onClick:()=>setAccToken('acc-fixture-b')},'Replace ACC credential'),
      h('button',{id:'base',onClick:()=>setAccBase(location.origin+'/acc-b')},'Replace ACC server'),
      h('output',{id:'authority-state'},session.authority.value?'owned':'none'),
      h('output',{id:'route-state'},router.currentRoute.value.fullPath),
      visible.value?h(${detail ? 'Detail' : 'LogStream'},{runId:'run-1'}):h('p',{id:'away'},'Other route'),
    ])}});
    app.use(pinia).use(router).use(createI18n({legacy:false,locale:'en',messages:{en:{}}})).mount('#app');
  `
  const root = fileURLToPath(new URL('../../../../', import.meta.url))
  const cacheDir = await mkdtemp(join(tmpdir(), 'acc-log-browser-'))
  let server, browser
  t.after(async () => {
    await browser?.close()
    for (const stream of streams) stream.res.end()
    await server?.close()
    await rm(cacheDir, { recursive: true, force: true })
  })
  server = await createServer({ root, configFile: false, cacheDir,
    resolve: { alias: { '@': `${root}src` } },
    optimizeDeps: { noDiscovery: true, include: ['vue', 'pinia', 'vue-router', 'vue-i18n'] },
    plugins: [vue(), { name: 'acc-log-browser-fixture', enforce: 'pre',
      resolveId(source) {
        if (source === 'virtual:acc-log-fixture') return '\0acc-log-fixture'
        // Auth state/generation is real. No DB IO is needed for a verified fixture
        // identity; fail loudly if this scoped stream test accidentally accesses it.
        if (/\/native\/local-db(?:\.ts)?$/.test(source)) return '\0acc-no-db'
      },
      load(id) {
        if (id === '\0acc-log-fixture') return entry
        if (id === '\0acc-no-db') return "export const localDB=new Proxy({},{get(){throw new Error('Unexpected DB IO in ACC stream fixture')}})"
      },
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          if (req.url === '/__acc_log__') {
            res.setHeader('Content-Type', 'text/html; charset=utf-8')
            res.end('<div id="app"></div><script type="module" src="/@id/__x00__acc-log-fixture"></script>')
          } else if (req.url.includes('/events/stream')) {
            const stream = { req, res, closed: false }
            streams.push(stream)
            if (!options.deferStreamHeaders) {
              res.writeHead(200, { 'Content-Type': 'text/event-stream' })
              res.write(': connected\n\n')
            }
            res.on('close', () => { stream.closed = true })
          } else if (/\/api\/v2\/sop\/runs\/[^/?]+$/.test(req.url)) {
            const request = { req, res, runId: req.url.split('/').at(-1) }
            details.push(request)
            if (!holdDetails) detailReply(request)
          } else if (/\/runs\/[^/]+\/cancel$/.test(req.url)) {
            cancellations++
            const request = { req, res }
            cancels.push(request)
            if (!holdCancels) cancelReply(request)
          } else if (/\/events(?:\?|$)/.test(req.url)) {
            polls.push({ req, res })
            res.writeHead(pollStatus, { 'Content-Type': 'application/json' })
            res.end(JSON.stringify({ message: 'denied' }))
          } else next()
        })
      },
    }], server: { host: '127.0.0.1', port: 0, hmr: false } })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const page = await browser.newPage({ viewport: { width: 390, height: 844 } })
  if (options.clock) await page.clock.install()
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__acc_log__`)
  if (options.deferStreamHeaders) await until(() => streams.length === 1)
  else await page.locator('.ls-mode.mode-sse').waitFor()
  return { page, streams, errors, details, cancels, polls,
    holdDetails() { holdDetails = true },
    holdCancels() { holdCancels = true },
    detailReply, cancelReply,
    denyPoll(status) { pollStatus = status },
    acceptCancel() { cancelStatus = 200 },
    cancellations() { return cancellations },
  }
}

function send(stream, seq, text, eventType = 'agent.log_line') {
  stream.res.write(`id: ${seq}\nevent: ${eventType}\ndata: ${JSON.stringify({
    sequence: seq, event_id: `event-${seq}`, event_type: eventType, run_id: 'run-1', payload: { text },
  })}\n\n`)
}

test('real Vue unmount/remount replays once; workspace/account/config observers outlive the view', { timeout: 30_000 }, async t => {
  const { page, streams, errors } = await fixture(t)
  send(streams[0], 1, 'Alice private one')
  await page.getByText('Alice private one', { exact: true }).waitFor()
  await page.locator('#toggle').click()
  await page.locator('#away').waitFor()
  send(streams[0], 2, 'Offscreen event')
  send(streams[0], 2, 'Duplicate event must not render')
  await page.locator('#toggle').click()
  await page.getByText('Offscreen event', { exact: true }).waitFor()
  assert.equal(await page.locator('.ls-line').count(), 2)
  assert.equal(streams.length, 1)
  assert.equal(streams[0].closed, false)

  await page.locator('#workspace').click()
  await until(() => streams[0].closed && streams.length === 2)
  assert.equal(await page.getByText('Alice private one', { exact: true }).count(), 0)
  send(streams[1], 1, 'Workspace two private')
  await page.getByText('Workspace two private', { exact: true }).waitFor()
  await page.locator('#account').click()
  await until(() => streams[1].closed && streams.length === 3)
  assert.equal(await page.getByText('Workspace two private', { exact: true }).count(), 0)

  await page.locator('#toggle').click()
  await page.locator('#credential').click()
  await until(() => streams[2].closed)
  await page.locator('#toggle').click()
  await until(() => streams.length === 4)
  assert.equal(streams[3].req.headers.authorization, 'Bearer acc-fixture-b')
  await page.locator('#base').click()
  await until(() => streams[3].closed && streams.length === 5)
  assert.match(streams[4].req.url, /^\/acc-b\//)
  send(streams[4], 1, 'Bob final event', 'run.terminal')
  await page.locator('.ls-mode.mode-ended').waitFor()
  await until(() => streams[4].closed)
  await page.locator('#toggle').click()
  await page.locator('#toggle').click()
  await page.getByText('Bob final event', { exact: true }).waitFor()
  assert.equal(streams.length, 5, 'terminal replay must not open another request')
  assert.deepEqual(errors, [])
})

test('actual detail cancel failure retains SSE; a successful response closes it explicitly', { timeout: 30_000 }, async t => {
  const { page, streams, errors, acceptCancel, cancellations } = await fixture(t, true)
  await page.getByRole('button', { name: '终止', exact: true }).click()
  await page.locator('.approval-note').waitFor()
  assert.equal(cancellations(), 1)
  assert.equal(streams[0].closed, false, 'unsupported production cancel surface must not fake success')
  send(streams[0], 1, 'Still receiving after unsupported cancel')
  await page.getByText('Still receiving after unsupported cancel', { exact: true }).waitFor()
  acceptCancel()
  await page.getByRole('button', { name: '终止', exact: true }).click()
  await page.locator('.ls-mode.mode-ended').waitFor()
  await until(() => streams[0].closed)
  assert.equal(cancellations(), 2)
  assert.deepEqual(errors, [])
})

async function flushResponses(page) {
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
}

for (const outcome of ['success', 'error']) {
  test(`late old-owner detail ${outcome} cannot replace a new run/view`, { timeout: 30_000 }, async t => {
    const f = await fixture(t, true)
    f.holdDetails()
    await f.page.getByRole('button', { name: '刷新', exact: true }).click()
    await until(() => f.details.length === 2)
    const stale = f.details[1]
    await f.page.locator('#account').click()
    await f.page.locator('#run-two').click()
    await until(() => f.details.some(request => request.runId === 'run-2'))
    f.detailReply(f.details.findLast(request => request.runId === 'run-2'), 'Bob current detail')
    await f.page.getByText('项目 Bob current detail', { exact: true }).waitFor()
    const response = f.page.waitForResponse(res => res.url().endsWith('/sop/runs/run-1'))
    f.detailReply(stale, 'Alice stale private detail', outcome === 'success' ? 200 : 503)
    await (await response).finished()
    await flushResponses(f.page)
    assert.equal(await f.page.getByText('项目 Bob current detail', { exact: true }).count(), 1)
    assert.equal(await f.page.locator('.error').count(), 0)
    assert.equal(await f.page.getByText('项目 Alice stale private detail', { exact: true }).count(), 0)
    assert.deepEqual(f.errors, [])
  })

  test(`late old-owner cancel ${outcome} cannot write notes, clear a new spinner or reload the new view`, { timeout: 30_000 }, async t => {
    const f = await fixture(t, true)
    f.holdCancels()
    await f.page.getByRole('button', { name: '终止', exact: true }).click()
    await until(() => f.cancels.length === 1)
    await f.page.locator('#account').click()
    await f.page.locator('#run-two').click()
    await f.page.locator('.run-id').filter({ hasText: 'run-2' }).waitFor()
    const button = f.page.locator('.tool.danger')
    assert.equal(await button.isEnabled(), true, 'new scope cannot inherit the old cancelling state')
    await button.click()
    await until(() => f.cancels.length === 2)
    const detailCount = f.details.length
    const response = f.page.waitForResponse(res => res.url().endsWith('/runs/run-1/cancel'))
    f.cancelReply(f.cancels[0], outcome === 'success' ? 200 : 503)
    await (await response).finished()
    await flushResponses(f.page)
    assert.equal(await button.isDisabled(), true, 'old finally cannot clear the current cancelling flag')
    assert.equal(await f.page.locator('.approval-note').count(), 0)
    assert.equal(f.details.length, detailCount, 'old completion cannot start a new-view refresh')
    assert.equal(f.streams.at(-1).closed, false)
    f.cancelReply(f.cancels[1], 200)
    await f.page.locator('.ls-mode.mode-ended').waitFor()
    assert.deepEqual(f.errors, [])
  })
}

test('owner and route A→B→A cannot admit the first A response', { timeout: 30_000 }, async t => {
  const f = await fixture(t, true)
  f.holdDetails()
  await f.page.getByRole('button', { name: '刷新', exact: true }).click()
  await until(() => f.details.length === 2)
  const stale = f.details[1]
  await f.page.locator('#account').click()
  await f.page.locator('#run-two').click()
  await until(() => f.details.some(request => request.runId === 'run-2'))
  await f.page.locator('#restore-account').click()
  await f.page.locator('#run-one').click()
  await until(() => f.details.at(-1).runId === 'run-1' && f.details.at(-1) !== stale)
  f.detailReply(f.details.at(-1), 'Fresh Alice detail')
  await f.page.getByText('项目 Fresh Alice detail', { exact: true }).waitFor()
  const response = f.page.waitForResponse(res => res.url().endsWith('/sop/runs/run-1'))
  f.detailReply(stale, 'First Alice stale detail')
  await (await response).finished()
  await flushResponses(f.page)
  assert.equal(await f.page.getByText('项目 Fresh Alice detail', { exact: true }).count(), 1)
  assert.equal(await f.page.getByText('项目 First Alice stale detail', { exact: true }).count(), 0)
})

for (const status of [401, 403]) {
  test(`SSE ${status} clears the reactive authority and visible private detail immediately`, { timeout: 30_000 }, async t => {
    const f = await fixture(t, true, { deferStreamHeaders: true })
    await f.page.getByText('项目 Initial private detail', { exact: true }).waitFor()
    f.streams[0].res.writeHead(status, { 'Content-Type': 'text/plain' })
    f.streams[0].res.write('denied') // Keep body open: headers alone must revoke admission.
    await f.page.waitForFunction(() => document.querySelector('#authority-state').textContent === 'none', null, { timeout: 1500 })
    assert.equal(await f.page.locator('.summary, .nodes, .ls-line, .approval-note').count(), 0)
    await until(() => f.streams[0].closed)
    assert.equal(f.details.length, 1, 'revocation must not auto-retry with the same credential')
  })

  test(`poll ${status} clears both existing private detail and replayed logs`, { timeout: 30_000 }, async t => {
    const f = await fixture(t, true)
    send(f.streams[0], 1, 'Private log before denial')
    await f.page.getByText('Private log before denial', { exact: true }).waitFor()
    f.denyPoll(status)
    f.streams[0].res.end()
    await until(() => f.polls.length === 1)
    await f.page.waitForFunction(() => document.querySelector('#authority-state').textContent === 'none', null, { timeout: 1500 })
    assert.equal(await f.page.locator('.summary, .nodes, .ls-line, .approval-note').count(), 0)
    assert.equal(f.details.length, 1)
  })
}

test('same-owner slow detail polling coalesces ticks and still publishes its response', { timeout: 30_000 }, async t => {
  const f = await fixture(t, true, { clock: true })
  f.holdDetails()
  await f.page.clock.runFor(5000)
  await until(() => f.details.length === 2)
  await f.page.clock.runFor(5000)
  await delay(50) // Let real HTTP dispatch complete before asserting no second flight.
  assert.equal(f.details.length, 2, 'same-owner polling must not overlap a pending read')
  f.detailReply(f.details[1], 'Slow current response')
  await f.page.getByText('项目 Slow current response', { exact: true }).waitFor()
  await f.page.clock.runFor(5000)
  await until(() => f.details.length === 3)
  f.detailReply(f.details[2], 'Next current response')
  await f.page.getByText('项目 Next current response', { exact: true }).waitFor()
})
