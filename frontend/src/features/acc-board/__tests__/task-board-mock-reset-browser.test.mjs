import assert from 'node:assert/strict'
import { test } from 'node:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { chromium } from 'playwright'

// task-board-mock-reset-browser.test.mjs — 「保存并刷新」必须真的把演示状态
// 复位到种子态。
//
// 为什么需要它（2026-11-16 真机实测）：
//   mock 的演示状态活在**模块内存**里（mock.ts 的 state.runs）。审批一次就把
//   那个 run 从 2/5 推进到 3/5 ⇒ 第二次跑同一条 Maestro flow 时，
//   「2/5 节点完成」这条断言必然红。
//   以前唯一的复位手段是**整页 reload**（saveSettings 里写死的
//   window.location.reload()）。但整页 reload 会把登录态一起打掉 ——
//   这台机的后端验不了 app 持有的 RedClaw token，reload 之后 app 自己就
//   登出了 ⇒「保存」反而把人踢出登录。
//   ⇒ 修法：mock 开关**没变**时不整页刷新，改为进程内复位演示状态 + 刷新
//     本页数据；开关**变了**时仍然整页刷新（整页行为确实换了）。
//
// 这个测试守两件事：
//   ① 复位后那条 run 回到 2/5（种子态），审批痕迹不在了；
//   ② 复位**不触发整页刷新**（reload 会把登录态打掉）—— 用页面不卸载来判定。

async function fixture(t) {
  const root = fileURLToPath(new URL('../../../../', import.meta.url))
  const cacheDir = await mkdtemp(join(tmpdir(), 'acc-board-reset-'))
  const errors = []
  let server, browser

  const entry = `
    import {createApp,h,ref,nextTick} from 'vue';
    import {createPinia,setActivePinia} from 'pinia';
    import {createRouter,createMemoryHistory} from 'vue-router';
    import {createI18n} from 'vue-i18n';
    import {useAuthStore} from '/src/stores/auth.ts';
    import Board from '/src/features/acc-board/TaskBoardView.vue';
    import {mockApproveGate} from '/src/features/acc-board/mock.ts';
    const pinia=createPinia();setActivePinia(pinia);const auth=useAuthStore();
    auth.$patch({token:'pocket-a',user:'Alice',userId:'alice',workspaceId:'one',activationStatus:'ready'});
    localStorage.setItem('acc_board_mock','1');
    const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:{render:()=>null}},
      {path:'/acc/tasks',name:'acc-unified-tasks',component:{render:()=>null}}]});
    await router.push('/');await router.isReady();
    // 记录页面有没有被整页卸载过：reload 会重跑这段入口脚本。
    // ⚠ 计数器必须存在 **sessionStorage** 里：window 上的变量在整页 reload
    //   之后是**新**的（window 整个换了），放 window 上就永远读到 1 ——
    //   那这条断言就是恒真的（实测踩过：把「总是 reload」改回去，测试照样绿）。
    //   sessionStorage 跟着同一个 tab 走，reload 后还在。
    window.pageLoads=Number(sessionStorage.getItem('__board_reset_loads__')||'0')+1;
    sessionStorage.setItem('__board_reset_loads__',String(window.pageLoads));
    window.approve=async()=>{
      await mockApproveGate('run-mock-7c1a','impl',{decision:'approve',approver:'tester'});
    };
    createApp({setup:()=>()=>h(Board)})
      .use(pinia).use(router).use(createI18n({legacy:false,locale:'en',messages:{en:{}}})).mount('#app');
    window.boardReady=true;
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
        name: 'board-reset-fixture',
        enforce: 'pre',
        resolveId(source) {
          if (source === 'virtual:board-reset') return '\0board-reset'
          if (/\/native\/local-db(?:\.ts)?$/.test(source)) return '\0board-no-db'
        },
        load(id) {
          if (id === '\0board-reset') return entry
          if (id === '\0board-no-db') return "export const localDB=new Proxy({},{get(){throw new Error('Unexpected DB IO')}})"
        },
        configureServer(vite) {
          vite.middlewares.use((req, res, next) => {
            if (req.url === '/__board_reset__') {
              res.setHeader('Content-Type', 'text/html')
              res.end('<div id="app"></div><div id="app-header-actions"></div><script type="module" src="/@id/__x00__board-reset"></script>')
            } else if (req.url.endsWith('/api/v2/sop/templates')) {
              res.setHeader('Content-Type', 'application/json')
              res.end('[]')
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
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__board_reset__`)
  await page.waitForFunction(() => window.boardReady)
  return {
    page,
    errors,
    text: () => page.locator('.page').innerText(),
    approve: () => page.evaluate(() => window.approve()),
    pageLoads: () => page.evaluate(() => window.pageLoads),
  }
}

test('「保存并刷新」把演示状态复位到种子态，且不整页刷新', { timeout: 30_000 }, async t => {
  const f = await fixture(t)
  // 种子态：2/5
  await f.page.getByText('2/5 节点完成', { exact: false }).waitFor()

  // 审批一次 ⇒ 推进到 3/5
  await f.approve()
  await f.page.getByRole('button', { name: '刷新', exact: true }).click()
  await f.page.getByText('3/5 节点完成', { exact: false }).waitFor()

  // 「保存并刷新」⇒ 复位到种子态
  await f.page.getByRole('button', { name: 'ACC 连接设置' }).click()
  await f.page.getByRole('button', { name: '保存并刷新' }).click()
  await f.page.getByText('2/5 节点完成', { exact: false }).waitFor()
  assert.ok(!(await f.text()).includes('3/5 节点完成'), await f.text())

  // 复位**没有**整页刷新：reload 会把登录态一起打掉。
  assert.equal(await f.pageLoads(), 1)
  assert.deepEqual(f.errors, [])
})
