import {createServer} from 'vite'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath} from 'node:url'

/** Actual settings confirmation/failure/retry UI; network and policy boundaries are fixtures. */
export async function createPurgeBrowserFixture(){
 const entry=`
 import{createApp,h}from'vue';import{createPinia}from'pinia';import{createI18n}from'vue-i18n';
 import View from'/src/features/settings/StorageSyncSection.vue';import zh from'/src/locales/zh-CN.json';
 import{state}from'virtual:purge-policy';import{portalNotesSyncStatus as status}from'/src/native/portal-sync/status.ts';
 window.purgeFixture={calls:[],fail:true,localOriginal:'retained private original',state,status};
 document.querySelector('#allow').onclick=()=>{window.purgeFixture.fail=false};
 document.querySelector('#unknown').onclick=()=>{status.blocked=1;status.resetPending=false;status.resetBlocked=false;status.epochUnknown=true};
 createApp({render:()=>h(View)}).use(createPinia()).use(createI18n({legacy:false,locale:'zh-CN',messages:{'zh-CN':zh}})).mount('#app');
 `
 const policy=`import{reactive}from'vue';export const state=reactive({modules:{'ai-chat':'local',email:'cloud',rss:'cloud',notes:'cloud',meetings:'cloud',tasks:'local'},revision:0,generation:1,loaded:true,allowOverride:true,activationReady:true,activating:false,saving:false,pendingLocal:[],lastSyncReport:[],setModule:async(module,mode)=>{state.modules[module]=mode;return true}});export const useSyncPolicyStore=()=>state;export const readCachedModuleMode=module=>state.modules[module];export const recordSyncReportEntry=()=>{}`
 const server=await createServer({root:fileURLToPath(new URL('../../../../',import.meta.url)),configFile:false,optimizeDeps:{noDiscovery:true},plugins:[vue(),{
 name:'notes-purge-browser-fixture',enforce:'pre',
 resolveId(source,importer){
  if(source==='virtual:purge-entry')return '\0purge-entry';if(source==='virtual:purge-policy'||/\/stores\/syncPolicyStore(?:\.ts)?$/.test(source))return '\0purge-policy';
  if(importer?.endsWith('StorageSyncSection.vue')&&source==='../../api/syncPolicy')return '\0purge-api';
  if(source.endsWith('/composables/useToast'))return '\0purge-toast';
  if(importer?.endsWith('StorageSyncSection.vue')&&source.endsWith('/email-fetch-native'))return '\0purge-native';
 },
 load(id){
  if(id==='\0purge-entry')return entry;if(id==='\0purge-policy')return policy;
  if(id==='\0purge-api')return `import{portalNotesSyncStatus as status}from'/src/native/portal-sync/status.ts';export const purgeCloudCopy=async modules=>{const f=window.purgeFixture;f.calls.push(modules);if(modules.includes('notes'))status.resetPending=true;if(f.fail)throw new Error('response lost');if(modules.includes('notes')){status.resetPending=false;status.resetBlocked=true;status.blocked=1}return{purged:Object.fromEntries(modules.map(m=>[m,0]))}}`;
  if(id==='\0purge-toast')return `export const useToast=()=>({error:m=>document.querySelector('#result').textContent=m,success:m=>document.querySelector('#result').textContent=m,warning:m=>document.querySelector('#result').textContent=m})`;
  if(id==='\0purge-native')return `export const nativeEmailStopState={unconfirmed:false,retrying:false};export const cancelNativeEmailFetch=async()=>true;export const retryNativeEmailFetchCancellation=async()=>true`;
 },
 configureServer(server){server.middlewares.use((req,res,next)=>{if(!req.url.startsWith('/__purge_ui'))return next();res.setHeader('Content-Type','text/html; charset=utf-8');res.end('<h1>可信清云确认与失败验收</h1><button id="allow">允许清云成功</button><button id="unknown">显示首次初始化等待</button><output id="result"></output><div id="app"></div><script type="module" src="/@id/__x00__purge-entry"></script>')})}
 }],server:{host:'127.0.0.1',port:0,hmr:false}})
 await server.listen();return {server,url:`http://127.0.0.1:${server.httpServer.address().port}/__purge_ui`}
}
if(process.argv[1]===fileURLToPath(import.meta.url)){const fixture=await createPurgeBrowserFixture();process.stdout.write(fixture.url+'\n')}
