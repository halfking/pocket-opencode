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
  const requests = [], held = [], errors = []
  let status = 200, hold = false, server, browser
  const entry = `
    import {createApp,h,ref,nextTick} from 'vue';
    import {createPinia,setActivePinia} from 'pinia';
    import {createRouter,createMemoryHistory} from 'vue-router';
    import {createI18n} from 'vue-i18n';
    import {useAuthStore} from '/src/stores/auth.ts';
    import {setAccBase,setAccToken} from '/src/api/accApi.ts';
    import Board from '/src/features/acc-board/TaskBoardView.vue';
    const pinia=createPinia();setActivePinia(pinia);const auth=useAuthStore();
    auth.$patch({token:'pocket-a',user:'Alice',userId:'alice',workspaceId:'one',activationStatus:'ready'});
    setAccBase(location.origin+'/acc-a');setAccToken('acc-fixture-a');
    ${legacy ? "localStorage.setItem('acc_board_last_runs',JSON.stringify({savedAt:new Date().toISOString(),items:[{run:{runId:'legacy-private'}}]}));" : ''}
    const visible=ref(true);
    const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:{render:()=>null}}]});
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
    createApp({setup:()=>()=>visible.value?h(Board):h('div','Away')})
      .use(pinia).use(router).use(createI18n({legacy:false,locale:'en',messages:{en:{}}})).mount('#app');
    window.boardReady=true;
  `
  t.after(async () => {
    for (const res of held) res.end()
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
  return { page, requests, errors, held, reply,
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
