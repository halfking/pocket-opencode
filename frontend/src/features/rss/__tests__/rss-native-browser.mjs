import{createServer}from'vite'
import vue from'@vitejs/plugin-vue'
import{fileURLToPath}from'node:url'

export async function createNativeRssBrowserFixture(){
 const state=`
 import initSqlJs from'sql.js';import{reactive}from'vue';import{LocalDbLeases}from'/src/native/local-db-leases.ts';
 const SQL=await initSqlJs({locateFile:()=>'/node_modules/sql.js/dist/sql-wasm.wasm'}),raw=new SQL.Database(),leases=new LocalDbLeases();
 export const auth=reactive({activationStatus:'ready',activationVersion:0,userId:'A',workspaceId:'one',token:'token-A'}),requests=[],events=new Map(),status=reactive({failed:false,running:false,blocked:0,resetPending:false,resetBlocked:false,epochUnknown:false});
 function query(sql,values=[]){const s=raw.prepare(sql);try{s.bind(values);const rows=[];while(s.step())rows.push(s.getAsObject());return rows}finally{s.free()}}
 export function activate(user){auth.activationStatus='pending';leases.activateOwner(leases.prepareOwner({userId:user,workspaceId:'one'}),()=>undefined);leases.activatePolicy(leases.preparePolicy('rss','cloud'),()=>undefined);auth.userId=user;auth.token='token-'+user;auth.activationVersion++;auth.activationStatus='ready'}
 export const localDB={captureOperation:o=>leases.capture(o),bindOperation:lease=>({schemaConnection:raw,owner:lease.owner.principal,assertCurrent:()=>leases.assertCurrent(lease),run:async(sql,v=[])=>{leases.assertCurrent(lease);raw.run(sql,v)},query:async(sql,v)=>{leases.assertCurrent(lease);return query(sql,v)},queryOne:async(sql,v)=>{leases.assertCurrent(lease);return query(sql,v)[0]??null},runInTransaction:async list=>{leases.assertCurrent(lease);raw.run('BEGIN');try{for(const s of list){leases.assertCurrent(lease);raw.run(s.statement,s.values)}raw.run('COMMIT')}catch(e){raw.run('ROLLBACK');throw e}}})};activate('A');
 export const useAuthStore=()=>auth;export const captureAuthSession=()=>auth.activationVersion;export function assertAuthSession(_a,version){if(version!==auth.activationVersion||auth.activationStatus!=='ready')throw new DOMException('Former RSS account','AbortError')}
 let pause,release;export function pauseDiscovery(){window.nativeDiscoveryStarted=false;pause=new Promise(r=>release=r)}export const releaseDiscovery=()=>release?.();
 export async function http(path,options={}){options.assertCurrent?.();const owner=auth.userId;requests.push({path,owner,body:options.body});
 if(path.includes('/starter'))return{feeds:[],categories:[]};
 if(path.includes('/discover')){if(pause){const waiting=pause;pause=null;window.nativeDiscoveryStarted=true;await waiting}options.assertCurrent?.();return{candidates:[{url:JSON.parse(options.body).url,title:'Native '+owner+' feed'}]}};
 if(path==='/api/rss/sources'&&options.method==='POST'){const body=JSON.parse(options.body),list=events.get(owner)??[];const seq=list.length+1;list.push({sequence:seq,kind:'source',ext_id:'pocketd-source:'+owner+'-'+seq,title:body.title||'Native '+owner+' source',payload:{url:body.url,category:''},client_updated_at:new Date().toISOString(),op_id:'pocketd-rss:00000000-0000-4000-8000-'+String(seq).padStart(12,'0'),epoch:body.portalEpoch,deleted:false,intent:'insert',fields:['title','url','category'],sync_error:''});events.set(owner,list);return{id:owner+'-'+seq,...body}};
 if(path.includes('/portable/changes')){const cursor=Number(new URL(path,'https://fixture').searchParams.get('cursor'));const changes=(events.get(owner)??[]).filter(r=>r.sequence>cursor);return{user_id:owner,workspace_id:'one',changes,next_cursor:changes.at(-1)?.sequence??cursor}};
 throw new Error('Unexpected native RSS fixture request '+path)}
 `
 const entry=`
 import{createApp,h}from'vue';import{createI18n}from'vue-i18n';import{createRouter,createMemoryHistory}from'vue-router';import messages from'/src/locales/en-US.json';import Add from'/src/features/rss/RssAddSource.vue';import Panel from'/src/features/rss/RssPortalPanel.vue';import Confirm from'/src/components/base/ConfirmDialog.vue';import{auth,activate,localDB,status,requests,pauseDiscovery,releaseDiscovery}from'virtual:native-rss';import{rssPortalStore}from'/src/features/rss/rss-sync-store.ts';import{ingestNativeRssChanges,nativeRssBlockedCount}from'/src/features/rss/rss-native-ingest.ts';
 const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:Add,name:'rss-add'},{path:'/rss',component:Panel,name:'rss'}]});await router.push('/');
 async function epoch(){await rssPortalStore(localDB.bindOperation(localDB.captureOperation({kind:'local-edit'}))).observeEpoch(0)}await epoch();
 createApp({render:()=>h('div',[h(Add),h(Panel),h(Confirm)])}).use(createI18n({legacy:false,locale:'en-US',messages:{'en-US':messages}})).use(router).mount('#app');
 async function ingest(){status.running=true;try{await ingestNativeRssChanges();const db=localDB.bindOperation(localDB.captureOperation({kind:'local-edit'}));status.blocked=await nativeRssBlockedCount(db);window.nativeSnapshot=await db.query("SELECT item_json FROM local_portal_record_outbox");status.failed=false}catch(e){status.failed=true;window.nativeError=String(e)}finally{status.running=false}}
 document.getElementById('pull').onclick=ingest;document.getElementById('alice').onclick=async()=>{activate('A');await epoch()};document.getElementById('bob').onclick=async()=>{activate('B');await epoch()};document.getElementById('pause').onclick=pauseDiscovery;document.getElementById('release').onclick=releaseDiscovery;
 window.nativeRssFixture={auth,requests,localDB,ingest};
 `
 const root=fileURLToPath(new URL('../../../../',import.meta.url));const server=await createServer({root,configFile:false,optimizeDeps:{noDiscovery:true,include:['sql.js','vue','pinia','vue-i18n']},plugins:[vue(),{name:'native-rss-fixture',enforce:'pre',resolveId(source,importer){
 if(source==='./http'&&importer?.endsWith('/api/rss.ts'))return'\0native-rss-state';
 if(source==='virtual:native-rss'||/\/native\/local-db(?:\.ts)?$/.test(source)||/\/stores\/auth(?:\.ts)?$/.test(source)||/\/api\/http(?:\.ts)?$/.test(source))return'\0native-rss-state';
 if(source==='virtual:native-entry')return'\0native-rss-entry';if(source==='./rss-sync-runtime')return'\0native-rss-runtime';if(/\/stores\/crypto-config(?:\.ts)?$/.test(source))return'\0native-rss-crypto';
 },load(id){if(id==='\0native-rss-state')return state;if(id==='\0native-rss-entry')return entry;if(id==='\0native-rss-runtime')return"import{status}from'virtual:native-rss';export const rssSyncRuntime={status,syncNow:async()=>0}";if(id==='\0native-rss-crypto')return'export const useCryptoConfig=()=>({shouldEncryptField:()=>false})'},configureServer(server){server.middlewares.use((req,res,next)=>{if(!req.url.startsWith('/__native_rss'))return next();res.setHeader('Content-Type','text/html');res.end('<button id="alice">Alice</button><button id="bob">Bob</button><button id="pull">Pull native changes</button><button id="pause">Pause discovery</button><button id="release">Release discovery</button><div id="app"></div><script>window.addEventListener("error",e=>document.body.append("ERROR: "+e.message));window.addEventListener("unhandledrejection",e=>document.body.append("REJECTION: "+String(e.reason)))</script><script type="module" src="/@id/__x00__native-rss-entry"></script>')})}}],server:{host:'127.0.0.1',port:0,hmr:false}})
 await server.listen();return{server,url:`http://127.0.0.1:${server.httpServer.address().port}/__native_rss`}
}
if(process.argv[1]===fileURLToPath(import.meta.url)){const f=await createNativeRssBrowserFixture();process.stdout.write(f.url+'\n')}
