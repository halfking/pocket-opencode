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