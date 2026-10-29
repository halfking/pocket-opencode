import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

/** Actual notice component and retry handler; no service or private account is used. */
export async function createNotesSyncNoticeFixture() {
  const entry = `
    import {createApp,h} from 'vue';import {createPinia} from 'pinia';
    import {createI18n} from 'vue-i18n';
    import Notice from '/src/features/settings/PortalNotesSyncNotice.vue';
    import {portalNotesSyncStatus as status} from '/src/native/portal-sync/runtime.ts';
    import zh from '/src/locales/zh-CN.json';
    status.failed=true;
    createApp({render:()=>h(Notice)}).use(createPinia()).use(createI18n({legacy:false,locale:'zh-CN',messages:{'zh-CN':zh}})).mount('#app');
    document.querySelector('#busy').onclick=()=>{status.running=!status.running};
    document.querySelector('#clear').onclick=()=>{status.failed=false;status.blocked=0};
    let clicks=0;document.addEventListener('click',event=>{if(event.target.closest('[data-testid="portal-notes-sync-retry"]'))document.querySelector('#clicks').textContent=String(++clicks)});
  `
  const server = await createServer({
    root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    optimizeDeps: { noDiscovery: true }, plugins: [vue(), {
      name: 'notes-sync-notice-fixture',
      resolveId(id) { if (id === 'virtual:notes-notice') return '\0notes-notice' },
      load(id) { if (id === '\0notes-notice') return entry },
      configureServer(server) {
        server.middlewares.use((req,res,next) => {
          if (!req.url.startsWith('/__notes_sync_notice')) return next()
          res.setHeader('Content-Type','text/html; charset=utf-8')
          res.end('<h1>笔记同步提示验收</h1><button id="busy">切换同步进行中</button><button id="clear">清除故障</button><output id="clicks">0</output><div id="app"></div><script type="module" src="/@id/__x00__notes-notice"></script>')
        })
      },
    }], server: { host:'127.0.0.1',port:0,hmr:false },
  })
  await server.listen()
  return {server,url:`http://127.0.0.1:${server.httpServer.address().port}/__notes_sync_notice`}
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const fixture=await createNotesSyncNoticeFixture()
  process.stdout.write(`${fixture.url}\n`)
}
