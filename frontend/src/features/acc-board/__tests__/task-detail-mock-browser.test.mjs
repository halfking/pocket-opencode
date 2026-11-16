import assert from 'node:assert/strict'
import { test } from 'node:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { chromium } from 'playwright'

// task-detail-mock-browser.test.mjs — the mock run DETAIL must be
// reachable.
//
// 2026-11-16 真机实测（4c308e2e）发现的缺陷：
//   ACC 看板的**列表**页在 mock 模式下能渲染内置样例 run，但**详情**页
//   永远停在「未找到该 Run」—— 列表里点得开的 run，详情打不开。
//
// 机制：详情页的加载被 ACC 归属门（captureView / currentView）挡住，而归属
// 的成立条件是「已验证的 pocket 身份 + ACC 连接」。mock 模式按定义**不请求
// ACC**（内置样例数据在本地内存里），所以这个门在 mock 模式下没有意义，
// 却把整个 mock 详情挡在了外面。
//
// 列表页早就有对应的旁路（TaskBoardView: `mockActive || boardOwnerCurrent(...)`），
// 详情页漏了 ⇒ 两页对「mock 模式下要不要归属」的判断不一致。
//
// 这个测试的牙齿：
//   ① mock 模式 + 无归属 ⇒ 详情必须渲染出样例 run（2/5 节点完成）；
//   ② **非** mock 模式 + 无归属 ⇒ 详情**必须**仍然打不开
//      （负控：证明修的是 mock 分支，没有把 live 模式的归属围栏一起拆掉）。

async function fixture(t, { mock }) {
  const root = fileURLToPath(new URL('../../../../', import.meta.url))
  const cacheDir = await mkdtemp(join(tmpdir(), 'acc-detail-mock-'))
  const errors = []
  let server, browser

  const entry = `
    import {createApp,h,nextTick} from 'vue';
    import {createPinia,setActivePinia} from 'pinia';
    import {createRouter,createMemoryHistory} from 'vue-router';
    import {createI18n} from 'vue-i18n';
    import {useAuthStore} from '/src/stores/auth.ts';
    import Detail from '/src/features/acc-board/TaskDetailView.vue';
    const pinia=createPinia();setActivePinia(pinia);
    const auth=useAuthStore();
    // 归属（owner）在下面两种情形下都不会成立：
    //   - mock 模式：没有 ACC 连接；
    //   - 非 mock 模式：pocket 身份没有 userId ⇒ 归属为空。
    // 两种情形都靠同一个「无归属」状态表达。
    auth.$patch({token:'pocket-a',user:'Alice',userId:'',workspaceId:'',activationStatus:'ready'});
    ${mock ? "localStorage.setItem('acc_board_mock','1');" : "localStorage.removeItem('acc_board_mock');localStorage.removeItem('acc_board_mock_session');"}
    const router=createRouter({history:createMemoryHistory(),routes:[
      {path:'/acc/runs/:runId',name:'acc-board-run',component:Detail},
    ]});
    await router.push('/acc/runs/run-mock-7c1a');await router.isReady();
    createApp({setup:()=>()=>h(Detail)})
      .use(pinia).use(router).use(createI18n({legacy:false,locale:'en',messages:{en:{}}})).mount('#app');
    window.detailReady=true;
  `

  t.after(async () => {
    await browser?.close()
    await server?.close()
    await rm(cacheDir, { recursive: true, force: true })
  })

  server = await createServer({
    root,
    configFile: false,
    cacheDir,
    resolve: { alias: { '@': `${root}src` } },
    optimizeDeps: { noDiscovery: true, include: ['vue', 'pinia', 'vue-router', 'vue-i18n'] },
    plugins: [
      vue(),
      {
        name: 'detail-mock-fixture',
        enforce: 'pre',
        resolveId(source) {
          if (source === 'virtual:detail-mock') return '\0detail-mock'
          if (/\/native\/local-db(?:\.ts)?$/.test(source)) return '\0detail-no-db'
        },
        load(id) {
          if (id === '\0detail-mock') return entry
          if (id === '\0detail-no-db') return "export const localDB=new Proxy({},{get(){throw new Error('Unexpected DB IO')}})"
        },
        configureServer(vite) {
          vite.middlewares.use((req, res, next) => {
            if (req.url === '/__detail_mock__') {
              res.setHeader('Content-Type', 'text/html')
              res.end('<div id="app"></div><script type="module" src="/@id/__x00__detail-mock"></script>')
            } else next()
          })
        },
      },
    ],
    server: { host: '127.0.0.1', port: 0, hmr: false },
  })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const page = await browser.newPage()
  page.setDefaultTimeout(8000)
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__detail_mock__`)
  await page.waitForFunction(() => window.detailReady)
  return { page, errors, text: () => page.locator('#app').innerText() }
}

test('mock mode opens the run detail without an ACC authority', { timeout: 30_000 }, async t => {
  const f = await fixture(t, { mock: true })
  // The sample run has 5 nodes, 2 of them completed.
  await f.page.getByText('2/5 节点完成', { exact: false }).waitFor()
  const body = await f.text()
  assert.ok(body.includes('run-mock-7c1a'), body)
  assert.ok(!body.includes('未找到该 Run'), body)
  assert.deepEqual(f.errors, [])
})

test('live mode still refuses the detail without an authority (negative control)', { timeout: 30_000 }, async t => {
  const f = await fixture(t, { mock: false })
  await f.page.getByText('未找到该 Run', { exact: false }).waitFor()
  const body = await f.text()
  assert.ok(!body.includes('2/5 节点完成'), body)
  assert.deepEqual(f.errors, [])
})
