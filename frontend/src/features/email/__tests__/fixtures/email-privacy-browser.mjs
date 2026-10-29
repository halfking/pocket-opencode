import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = fileURLToPath(new URL('../../../../../', import.meta.url))
export const pixel = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'
const body = `<p style="color:rgb(1, 2, 3);padding:7px">保留本地邮件原文</p>
  <img alt="embedded" src="${pixel}"><img src="/__privacy_resource__/image">
  <div class="image-set" style="background-color:rgb(4, 5, 6);background-image:image-set('/__privacy_resource__/set' 1x)">背景文字</div>
  <div style="background-image:u\\72l(\\2f __privacy_resource__/escaped)">转义背景</div>
  <div style="list-style-image:url('/__privacy_resource__/list');cursor:url('/__privacy_resource__/cursor'),auto;filter:url('/__privacy_resource__/filter')">其他资源</div>
  <div><style>@import '/__privacy_resource__/import';.image-set{background-image:image-set('/__privacy_resource__/style-set' 1x)}</style>样式块文字</div>
  <a href="/__privacy_resource__/explicit">用户链接</a>`

/** Real component and real policy actions; only storage/service boundaries are fixtures. */
export async function createPrivacyBrowserFixture() {
  let fixtureBody = body
  const entry = `
    import {createApp,h,nextTick} from 'vue';
    import {createPinia,setActivePinia} from 'pinia';
    import {createRouter,createMemoryHistory} from 'vue-router';
    import {createI18n} from 'vue-i18n';
    import View from '/src/features/email/EmailDetailView.vue';
    import {useSyncPolicyStore,syncPolicyTransport,installSyncPolicyScopeResolver,installSyncPolicyActivationHost} from '/src/stores/syncPolicyStore.ts';
    import {LocalDbLeases} from '/src/native/local-db-leases.ts';
    const pinia=createPinia();setActivePinia(pinia);
    const principal={userId:'privacy-fixture',workspaceId:'privacy-workspace'};
    const scope=JSON.stringify([principal.userId,principal.workspaceId]);
    const leases=new LocalDbLeases();
    leases.activateOwner(leases.prepareOwner(principal),()=>undefined);
    installSyncPolicyScopeResolver(()=>scope);
    installSyncPolicyActivationHost({
      captureContext:()=>({scope,intentScope:scope,workspace:principal.workspaceId,isCurrent:()=>true}),
      activate:async(modes,publish)=>{leases.activateSnapshot(leases.prepareSnapshot(modes),publish)},
    });
    const policy=useSyncPolicyStore();
    const modules={...policy.modules,email:new URL(location.href).searchParams.get('mode')||'local'};
    await policy.applyPolicy({system_default:'local',allow_override:true,modules},Date.now());
    syncPolicyTransport.putModules=async modules=>({system_default:'local',allow_override:true,modules});
    const calls=[];const originalFetch=window.fetch.bind(window);
    window.fetch=(url,options)=>{if(String(url).includes('__privacy_resource__'))calls.push({url:String(url),mode:policy.modules.email});return originalFetch(url,options)};
    const router=createRouter({history:createMemoryHistory(),routes:[{path:'/email/:id',component:View}]});
    await router.push('/email/fixture');
    const i18n=createI18n({legacy:false,locale:'zh-CN',messages:{'zh-CN':{email:{localPrivacyNotice:'仅本地：保留原文，已停用远程图片和自动翻译。'}}}});
    createApp({render:()=>h(View)}).use(pinia).use(router).use(i18n).mount('#app');
    window.privacyHarness={calls,policy,local:async()=>{await policy.setModule('email','local');await nextTick()},cloud:async()=>{await policy.setModule('email','cloud');await nextTick()}};
  `
  const server = await createServer({
    root, configFile: false, optimizeDeps: { noDiscovery: true, entries: [] },
    plugins: [vue(), {
      name: 'email-privacy-browser-fixture', enforce: 'pre',
      resolveId(source, importer) {
        if (source === 'virtual:privacy-entry') return '\0privacy-entry'
        if (source.endsWith('/composables/useToast')) return '\0toast'
        if (!importer?.includes('/features/email/EmailDetailView.vue')) return
        const fixtures = {
          './emails-store': 'mail-store', './email-body-cache': 'body-cache',
          '../../api/email': 'mail-api', '../../api/client': 'api',
          '../contact/contacts-store': 'contacts', './email-folders-store': 'folders',
          '../../composables/list-scene-store': 'dirty', '../../components': 'components',
        }
        if (fixtures[source]) return `\0${fixtures[source]}`
        if (source.endsWith('.vue')) return '\0empty-vue'
      },
      load(id) {
        if (id === '\0privacy-entry') return entry
        if (id === '\0mail-store') return `export const getEmail=async()=>({id:'fixture',fromAddress:'fixture@example.invalid',subject:'Privacy fixture',isRead:true,date:0,aiSummary:'本地摘要'});export const markRead=async()=>{};export const setAiSummary=async()=>{}`
        if (id === '\0body-cache') return `export const captureEmailBodyContext=()=>({key:'fixture',assertCurrent(){}});export const pickEmailDetailBody=(cached,remote)=>remote||cached;export const readEmailBodyLocal=async()=>${JSON.stringify(fixtureBody)};export const writeEmailBodyLocal=async()=>{}`
        if (id === '\0mail-api') return `export const emailApi={getEmailBody:async()=>({body:${JSON.stringify(fixtureBody)}})}`
        if (id === '\0api') return 'export const api={}'
        if (id === '\0contacts') return 'export const findContactByEmail=async()=>null'
        if (id === '\0folders') return 'export const recordOpsEntry=async()=>{}'
        if (id === '\0dirty') return 'export const markListDirty=()=>{}'
        if (id === '\0toast') return 'export const useToast=()=>({error(){},info(){},success(){}})'
        if (id === '\0components') return 'export const ErrorState={template:"<div>Error</div>"}'
        if (id === '\0empty-vue') return `import{h}from'vue';export default{setup(_,{slots}){return()=>h('div',slots.default?.())}}`
      },
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          if (req.url.startsWith('/__privacy_resource__/')) {
            res.setHeader('Content-Type', 'image/gif')
            res.end(Buffer.from(pixel.split(',')[1], 'base64'))
          } else if (req.url.startsWith('/__privacy_fixture__')) {
            res.setHeader('Content-Type', 'text/html; charset=utf-8')
            res.end('<button onclick="window.privacyHarness.local()">切换本地</button><button onclick="window.privacyHarness.cloud()">切换云端</button><div id="app"></div><script type="module" src="/@id/__x00__privacy-entry"></script>')
          } else next()
        })
      },
    }], server: { host: '127.0.0.1', port: 0, hmr: false },
  })
  await server.listen()
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`
  fixtureBody = body.replaceAll('/__privacy_resource__/', `${origin}/__privacy_resource__/`)
  fixtureBody += `<div style="background-image:url(${origin.replace(':', '\\3a ')}/__privacy_resource__/escaped-protocol)">协议转义</div>`
  return { server, url: `${origin}/__privacy_fixture__` }
}
