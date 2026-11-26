import assert from 'node:assert/strict'
import { test } from 'node:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { chromium } from 'playwright'

async function fixture(t, legacy = false) {
  const root = fileURLToPath(new URL('../../../../', import.meta.url))
  const cacheDir = await mkdtemp(join(tmpdir(), 'acc-board-owner-'))
  const requests = [], held = [], errors = [], taskReads = [], eventCursors = []
  const delayedTasks = []
  const eventReplies = []
  let delayTaskPage = false, holdEventReplay = false
  let bindingStatus = 200
  let status = 200, hold = false, server, browser
  const entry = `
    import {createApp,h,ref,nextTick} from 'vue';
    import {createPinia,setActivePinia} from 'pinia';
    import {createRouter,createMemoryHistory,RouterView} from 'vue-router';
    import {createI18n} from 'vue-i18n';
    import {useAuthStore} from '/src/stores/auth.ts';
    import {setAccBase,setAccToken} from '/src/api/accApi.ts';
    import Board from '/src/features/acc-board/TaskBoardView.vue';
    import UnifiedTasks from '/src/features/acc-board/UnifiedTasksView.vue';
    const pinia=createPinia();setActivePinia(pinia);const auth=useAuthStore();
    auth.$patch({token:'pocket-a',user:'Alice',userId:'alice',workspaceId:'one',activationStatus:'ready'});
    setAccBase(location.origin+'/acc-a');setAccToken('acc-fixture-a');
    localStorage.setItem('op_redclaw_portal_base',location.origin);
    ${legacy ? "localStorage.setItem('acc_board_last_runs',JSON.stringify({savedAt:new Date().toISOString(),items:[{run:{runId:'legacy-private'}}]}));" : ''}
    const visible=ref(true);
    const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:Board},
      {path:'/acc/tasks',name:'acc-unified-tasks',component:UnifiedTasks}]});
    await router.push('/');await router.isReady();
    window.boardAction=async kind=>{
      if(kind==='workspace')auth.workspaceId='two';
      if(kind==='account')auth.$patch({token:'pocket-b',user:'Bob',userId:'bob'});
      if(kind==='credential')setAccToken('acc-fixture-b');
      if(kind==='base')setAccBase(location.origin+'/acc-b');
      if(kind==='aba'){auth.token='pocket-b';auth.token='pocket-a';}
      if(kind==='hide')visible.value=false;if(kind==='show')visible.value=true;
      await nextTick();
    };
    createApp({setup:()=>()=>visible.value?h(RouterView):h('div','Away')})
      .use(pinia).use(router).use(createI18n({legacy:false,locale:'en',messages:{en:{}}})).mount('#app');
    window.boardReady=true;
  `
  t.after(async () => {
    for (const res of held) res.end()
    for (const reply of delayedTasks) reply()
    for (const { res } of eventReplies) res.end()
    await browser?.close(); await server?.close()
    await rm(cacheDir, { recursive: true, force: true })
  })
  const reply = (res, code = status, id = 'private-run-a') => {
    res.writeHead(code, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify(code === 200 ? { runs: [{ run_id: id, status: 'running', nodes: [] }] } : { message: 'fixture failure' }))
  }
  server = await createServer({ root, configFile: false, cacheDir,
    resolve: { alias: { '@': `${root}src` } },
    optimizeDeps: { noDiscovery: true, include: ['vue', 'pinia', 'vue-router', 'vue-i18n'] },
    plugins: [vue(), { name: 'board-owner-fixture', enforce: 'pre',
      resolveId(source) {
        if (source === 'virtual:board-owner') return '\0board-owner'
        if (/\/native\/local-db(?:\.ts)?$/.test(source)) return '\0board-no-db'
      },
      load(id) {
        if (id === '\0board-owner') return entry
        if (id === '\0board-no-db') return "export const localDB=new Proxy({},{get(){throw new Error('Unexpected DB IO')}})"
      },
      configureServer(vite) { vite.middlewares.use((req, res, next) => {
        if (req.url === '/__board_owner__') {
          res.setHeader('Content-Type', 'text/html')
          res.end('<div id="app"></div><script type="module" src="/@id/__x00__board-owner"></script>')
        } else if (/\/api\/v1\/portal\/workspaces\/[^/]+\/binding$/.test(req.url)) {
          const ws = req.url.split('/').at(-2)
          const user = req.headers.authorization === 'Bearer pocket-b' ? 'bob' : 'alice'
          res.setHeader('Content-Type', 'application/json')
          if (bindingStatus !== 200) { res.statusCode = bindingStatus; res.end('{}'); return }
          res.end(JSON.stringify({ binding: 'fixture-assertion', workspace_id: ws, user_id: user,
            tenant_id: ws === 'two' ? 'tenant-other' : `tenant-${user}`, expires_at: Math.floor(Date.now() / 1000) + 60 }))
        } else if (/\/api\/v2\/(auth\/session|tasks(?:[/?]|$)|orchestration\/runs\/)/.test(req.url)) {
          const url = new URL(req.url, 'http://localhost')
          const path = url.pathname.replace(/^\/acc-[ab]/, '')
          const user = req.headers.authorization === 'Bearer acc-fixture-b' ? 'bob' : 'alice'
          taskReads.push([user, path])
          const json = value => {
            res.setHeader('Content-Type', 'application/json')
            res.end(JSON.stringify(value))
          }
          const task = { task_id: 'task-' + user, run_id: 'run-' + user, title: '联跑任务 ' + user, status: 'running' }
          if (path === '/api/v2/auth/session') json({ authenticated: true, user_id: user, tenant_id: `tenant-${user}` })
          else if (path === '/api/v2/tasks') {
            const offset = Number(url.searchParams.get('offset'))
            const item = offset ? { ...task, task_id: task.task_id + '-next', title: '下一页 ' + user } : task
            const reply = () => json({ tasks: [item], pagination: { offset, limit: 20, total: 2, has_more: !offset } })
            if (delayTaskPage) { delayTaskPage = false; delayedTasks.push(reply) } else reply()
          } else if (path.endsWith('/progress')) {
            json({ task_id: task.task_id, status: 'review', progress: { completed: 1, total: 2, percentage: 50, review: 1 } })
          } else if (path === '/api/v2/tasks/' + task.task_id) json(task)
          else if (path.endsWith('/trajectory')) json({ view: { scope: 'run', run_id: task.run_id, segments: [
            { kind: 'dispatch_tree', items: [{ task_id: task.task_id, title: '实现', status: 'succeeded' }, { task_id: 'review', title: '审查', status: 'review' }] },
            { kind: 'blocking_dependencies', items: [{ from: task.task_id, to: 'review' }] },
          ] } })
          else if (path.endsWith('/events/stream')) {
            res.setHeader('Content-Type', 'text/event-stream')
            res.end(`id: 1\ndata: ${JSON.stringify({ sequence: 1, event_type: 'run.terminal', event_id: 'evt-1', run_id: task.run_id, payload: { text: '已读取派发回执' } })}\n\n`)
          } else if (path.endsWith('/events')) {
            eventCursors.push([url.pathname, url.searchParams.get('after')])
            const events = Number(url.searchParams.get('after')) < 2
              ? [{ sequence: 2, event_type: 'task.review.approved', event_id: 'evt-2', run_id: task.run_id, payload: { text: '已续读评审回执' } }] : []
            if (holdEventReplay) {
              holdEventReplay = false
              res.writeHead(200, { 'Content-Type': 'application/json' })
              res.write('{"events":')
              const pending = { res, closed: false }
              res.on('close', () => { pending.closed = true })
              eventReplies.push(pending)
            } else json({ events })
          } else if (path === '/api/v2/orchestration/runs/' + task.run_id) json({ run: { run_id: task.run_id, status: 'failed' } })
          else { res.statusCode = 404; json({ error: 'unknown fixture route' }) }
        } else if (/\/api\/v2\/sop\/runs$/.test(req.url)) {
          requests.push({ url: req.url, auth: req.headers.authorization })
          if (hold) held.push(res); else reply(res)
        } else if (req.url.endsWith('/api/v2/sop/templates')) {
          res.setHeader('Content-Type', 'application/json'); res.end('[]')
        } else next()
      }) },
    }], server: { host: '127.0.0.1', port: 0, hmr: false } })
  await server.listen(); browser = await chromium.launch({ headless: true })
  const page = await browser.newPage()
  page.setDefaultTimeout(5000)
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__board_owner__`)
  await page.waitForFunction(() => window.boardReady)
  return { page, requests, errors, held, reply, taskReads, eventCursors, delayedTasks, eventReplies,
    holdTaskPage() { delayTaskPage = true },
    holdEventReplay() { holdEventReplay = true },
    denyBinding() { bindingStatus = 403 },
    setStatus(value) { status = value }, hold(value = true) { hold = value },
    action: kind => page.evaluate(kind => window.boardAction(kind), kind),
    text: () => page.locator('.page').innerText(),
  }
}

test('new ACC credential 401 cannot replay an earlier authority cache', { timeout: 30_000 }, async t => {
  const f = await fixture(t)
  await f.page.getByText('private-run-a', { exact: true }).waitFor()
  f.setStatus(401); await f.action('credential')
  await f.page.getByRole('button', { name: '刷新', exact: true }).click()
  await f.page.waitForFunction(() => !document.querySelector('.state')?.textContent?.includes('加载中'))
  assert.ok(!(await f.text()).includes('private-run-a'))
  assert.equal(await f.page.evaluate(() => localStorage.getItem('acc_board_last_runs')), null)
  assert.deepEqual(f.errors, [])
})

for (const kind of ['workspace', 'account', 'aba', 'base']) {
  test(`owner change (${kind}) clears visible rows and fences delayed responses`, { timeout: 30_000 }, async t => {
    const f = await fixture(t)
    await f.page.getByText('private-run-a', { exact: true }).waitFor()
    f.hold(); await f.page.getByRole('button', { name: '刷新', exact: true }).click()
    await f.page.waitForTimeout(50)
    assert.ok(f.held.length)
    await f.action(kind)
    assert.ok(!(await f.text()).includes('private-run-a'))
    f.reply(f.held.shift(), 200, 'stale-private-result')
    await f.page.waitForTimeout(80)
    assert.ok(!(await f.text()).includes('stale-private-result'))
    assert.deepEqual(f.errors, [])
  })
}

test('same session remount may use its memory snapshot; legacy cache is discarded', { timeout: 30_000 }, async t => {
  const f = await fixture(t, true)
  await f.page.getByText('private-run-a', { exact: true }).waitFor()
  assert.equal(await f.page.evaluate(() => localStorage.getItem('acc_board_last_runs')), null)
  f.setStatus(503); await f.action('hide'); await f.action('show')
  await f.page.locator('.source-note').filter({ hasText: '离线缓存' }).waitFor()
  assert.ok((await f.text()).includes('private-run-a'), await f.text())
  assert.ok((await f.text()).includes('离线缓存'))
  assert.deepEqual(f.errors, [])
})

test('unified tasks show authorized progress and replay the same run without crossing account boundaries', { timeout: 30_000 }, async t => {
  const f = await fixture(t)
  await f.page.getByRole('link', { name: '统一任务', exact: true }).click()
  await f.page.getByRole('button', { name: /联跑任务 alice/ }).click()
  await f.page.getByText('Run run-alice', { exact: true }).waitFor()
  const detail = f.page.getByRole('region', { name: '统一任务进度' })
  await detail.getByText('review · 1/2 个节点完成', { exact: true }).waitFor()
  await detail.getByText('task-alice → review', { exact: true }).waitFor()
  await detail.getByText('已读取派发回执', { exact: true }).waitFor()
  await detail.getByText('已续读评审回执', { exact: true }).waitFor()
  assert.ok(f.eventCursors.some(([path, after]) => path.includes('/runs/run-alice/') && after === '1'))
  await f.page.getByLabel('Run ID', { exact: true }).fill('run-alice')
  await f.page.getByRole('button', { name: '查看 Run', exact: true }).click()
  await detail.getByRole('heading', { name: 'run-alice', exact: true }).waitFor()
  await detail.getByText('已续读评审回执', { exact: true }).waitFor()
  await f.page.getByRole('button', { name: '刷新', exact: true }).click()
  await detail.getByRole('heading', { name: 'run-alice', exact: true }).waitFor()
  await detail.getByText('已续读评审回执', { exact: true }).waitFor()
  f.holdTaskPage()
  await f.page.getByRole('button', { name: '更多任务', exact: true }).click()
  for (let i = 0; !f.delayedTasks.length && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(f.delayedTasks.length, 1)
  await f.action('account')
  await f.page.getByText('ACC 凭据与当前账号或租户不一致', { exact: true }).waitFor()
  f.delayedTasks.shift()()
  assert.ok(!(await f.text()).includes('联跑任务 alice'))
  assert.ok(!(await f.text()).includes('已读取派发回执'))
  assert.ok(!(await f.text()).includes('下一页 alice'))
  await f.action('credential')
  await f.page.getByRole('button', { name: /联跑任务 bob/ }).waitFor()
  await f.page.getByRole('button', { name: '更多任务', exact: true }).click()
  await f.page.getByRole('button', { name: /下一页 bob/ }).waitFor()
  const listCount = f.taskReads.filter(([owner, path]) => owner === 'bob' && path === '/api/v2/tasks').length
  await f.action('workspace')
  await f.page.getByText('ACC 凭据与当前账号或租户不一致', { exact: true }).waitFor()
  assert.equal(f.taskReads.filter(([owner, path]) => owner === 'bob' && path === '/api/v2/tasks').length, listCount)
  assert.deepEqual(f.errors, [])
})

test('expiring the verified binding clears the task view and cancels an in-flight receipt replay', { timeout: 30_000 }, async t => {
  const f = await fixture(t)
  await f.page.clock.install()
  await f.page.getByRole('link', { name: '统一任务', exact: true }).click()
  f.holdEventReplay()
  await f.page.getByRole('button', { name: /联跑任务 alice/ }).click()
  await f.page.getByText('已读取派发回执', { exact: true }).waitFor()
  for (let i = 0; !f.eventReplies.length && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(f.eventReplies.length, 1)
  assert.equal(f.eventReplies[0].closed, false)
  await f.page.clock.fastForward(61_000)
  await f.page.getByText('工作区或 ACC 授权已失效，请刷新后重新核验。', { exact: true }).waitFor()
  for (let i = 0; !f.eventReplies[0].closed && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(f.eventReplies[0].closed, true)
  assert.equal(f.eventCursors.length, 1)
  assert.ok(!(await f.text()).includes('联跑任务 alice'))
  assert.ok(!(await f.text()).includes('已读取派发回执'))
  assert.deepEqual(f.errors, [])
})

test('a denied workspace binding renewal clears previously admitted task data', { timeout: 30_000 }, async t => {
  const f = await fixture(t)
  await f.page.getByRole('link', { name: '统一任务', exact: true }).click()
  await f.page.getByRole('button', { name: /联跑任务 alice/ }).click()
  await f.page.getByText('已续读评审回执', { exact: true }).waitFor()
  f.denyBinding()
  const bindingDependentReads = () => f.taskReads.filter(([, path]) => !path.includes('/events')).length
  const reads = bindingDependentReads()
  await f.page.getByRole('button', { name: '更多任务', exact: true }).click()
  await f.page.getByText('工作区或 ACC 授权已失效，请刷新后重新核验。', { exact: true }).waitFor()
  assert.equal(bindingDependentReads(), reads)
  assert.ok(!(await f.text()).includes('联跑任务 alice'))
  assert.ok(!(await f.text()).includes('已续读评审回执'))
  assert.deepEqual(f.errors, [])
})
