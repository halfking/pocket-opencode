import {createServer} from 'vite'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath} from 'node:url'

export async function createRssStateBrowserFixture(){
 const state=`
 import initSqlJs from 'sql.js';import{reactive}from'vue';import{LocalDbLeases}from'/src/native/local-db-leases.ts';
 const SQL=await initSqlJs({locateFile:()=>'/node_modules/sql.js/dist/sql-wasm.wasm'}),raw=new SQL.Database(),leases=new LocalDbLeases();
 export const auth=reactive({activationStatus:'ready',activationVersion:0,userId:'A',workspaceId:'one',token:'token-A'}),requests=[],status=reactive({failed:false,running:false,blocked:0,resetPending:false,resetBlocked:false,epochUnknown:false});
 function query(sql,values=[]){const s=raw.prepare(sql);try{s.bind(values);const rows=[];while(s.step())rows.push(s.getAsObject());return rows}finally{s.free()}}
 export function activate(user){auth.activationStatus='pending';leases.activateOwner(leases.prepareOwner({userId:user,workspaceId:'one'}),()=>undefined);leases.activatePolicy(leases.preparePolicy('rss','cloud'),()=>undefined);auth.userId=user;auth.token='token-'+user;auth.activationVersion++;auth.activationStatus='ready'}
 export const localDB={captureOperation:o=>leases.capture(o),bindOperation:lease=>({schemaConnection:raw,owner:lease.owner.principal,assertCurrent:()=>leases.assertCurrent(lease),run:async(sql,v=[])=>{leases.assertCurrent(lease);raw.run(sql,v)},query:async(sql,v)=>{leases.assertCurrent(lease);return query(sql,v)},queryOne:async(sql,v)=>{leases.assertCurrent(lease);return query(sql,v)[0]??null},runInTransaction:async list=>{leases.assertCurrent(lease);raw.run('BEGIN');try{for(const s of list){leases.assertCurrent(lease);raw.run(s.statement,s.values)}raw.run('COMMIT')}catch(e){raw.run('ROLLBACK');throw e}}})};activate('A');
 export const useAuthStore=()=>auth;export const captureAuthSession=()=>auth.activationVersion;export function assertAuthSession(_a,version){if(version!==auth.activationVersion||auth.activationStatus!=='ready')throw new DOMException('Former RSS account','AbortError')}
 let nativeStatus='read';export const makeUnread=()=>nativeStatus='unread';export const article=()=>({id:'article',sourceId:'source',title:'Article '+auth.userId,content:'<p>PRIVATE BODY '+auth.userId+'</p>',url:'https://example.test/article',categories:[],fetchedAt:'2026-01-01T00:00:00Z',relevance:0,matchReasons:[],status:nativeStatus,portalManaged:true});
 let offline=true;export function online(){offline=false}export function disconnect(){offline=true}
 let emailPause=null,emailRelease;export function pauseHubEmail(){window.hubEmailWaiting=false;emailPause=new Promise(resolve=>emailRelease=resolve)}export const releaseHubEmail=()=>emailRelease?.();export async function readHubEmails(){const pause=emailPause;emailPause=null;if(pause){window.hubEmailWaiting=true;await pause}return[]}
 export async function http(path,options={}){options.assertCurrent?.();requests.push({path,owner:auth.userId,body:options.body});
 if(path==='/api/rss/items/article')return article();
 if(path==='/api/rss/sources')return{sources:[]};
 if(path.startsWith('/api/notifications'))return{notifications:[]};
 if(path.startsWith('/api/rss/items?')){const q=new URL(path,'https://fixture').searchParams;return{items:(!q.get('status')||q.get('status')===nativeStatus)&&(!q.getAll('id').length||q.getAll('id').includes('article'))?[article()]:[]}};
 if(path==='/api/rss/portable/item-states'){if(offline)throw new Error('offline');return{user_id:auth.userId,workspace_id:auth.workspaceId,acks:JSON.parse(options.body).items.map(item=>({ext_id:item.ext_id,op_id:item.op_id,epoch:item.epoch,outcome:'applied'}))}}
 throw new Error('Unexpected request '+path)}
 `
 const entry=`
 import{createApp,h,ref}from'vue';import{createI18n}from'vue-i18n';import{createRouter,createMemoryHistory}from'vue-router';import messages from'/src/locales/en-US.json';import Detail from'/src/features/rss/RssItemDetail.vue';import List from'/src/features/rss/RssListView.vue';import{auth,activate,localDB,requests,online,disconnect}from'virtual:rss-state';import{rssPortalStore}from'/src/features/rss/rss-sync-store.ts';import{reconcileRssItemStates}from'/src/features/rss/rss-item-state.ts';import{applyRecordPage}from'/src/native/portal-sync/record-engine.ts';
 import Messages from'/src/features/messages/MessagesHubView.vue';import{makeUnread,pauseHubEmail,releaseHubEmail}from'virtual:rss-state';
 const router=createRouter({history:createMemoryHistory(),routes:[{path:'/rss/:id',component:Detail,name:'rss-item'},{path:'/rss',component:Detail,name:'rss'}]});await router.push('/rss/article');
 async function epoch(){await rssPortalStore(localDB.bindOperation(localDB.captureOperation({kind:'local-edit'}))).observeEpoch(0)}await epoch();
 const show=ref('detail');document.getElementById('list').onclick=()=>show.value='list';document.getElementById('messages').onclick=()=>{makeUnread();show.value='messages'};
 createApp({render:()=>h(show.value==='list'?List:show.value==='messages'?Messages:Detail)}).use(createI18n({legacy:false,locale:'en-US',messages:{'en-US':messages}})).use(router).mount('#app');
 async function snapshot(){const db=localDB.bindOperation(localDB.captureOperation({kind:'local-edit'}));window.stateSnapshot=await db.query("SELECT item_json FROM local_portal_records WHERE owner_user_id=? AND kind='item-state'",[auth.userId])}
 document.getElementById('alice').onclick=async()=>{activate('A');await epoch()};document.getElementById('bob').onclick=async()=>{activate('B');await epoch()};
 document.getElementById('offline').onclick=disconnect;document.getElementById('online').onclick=online;
 document.getElementById('sync').onclick=async()=>{try{await reconcileRssItemStates();document.getElementById('result').textContent='Saved to pocketd'}catch(e){document.getElementById('result').textContent=String(e)}await snapshot()};
 let remoteSequence=0;async function remoteState(status){remoteSequence++;const store=rssPortalStore(localDB.bindOperation(localDB.captureOperation({kind:'local-edit'})));await applyRecordPage(store,{epoch:0,next_cursor:'remote-'+remoteSequence,changes:[{module:'rss',kind:'item-state',ext_id:'pocketd-item:article',title:'',payload:{item_ext_id:'pocketd-item:article',status},client_updated_at:new Date(Date.now()+1000+remoteSequence).toISOString(),op_id:'remote-state-'+remoteSequence,deleted:false,epoch:0,change_seq:remoteSequence}]});window.dispatchEvent(new Event('openpocket:portal-rss-applied'));await snapshot()}
 document.getElementById('remote').onclick=()=>remoteState('starred');document.getElementById('remote-read').onclick=()=>remoteState('read');
 window.rssStateFixture={auth,localDB,requests,snapshot,pauseHubEmail,releaseHubEmail};
 `
 const root=fileURLToPath(new URL('../../../../',import.meta.url))
 const server=await createServer({root,configFile:false,optimizeDeps:{noDiscovery:true,include:['sql.js','vue','vue-i18n','vue-router','dompurify','marked']},plugins:[vue(),{name:'rss-state-fixture',enforce:'pre',resolveId(source,importer){
  if(source==='./http'&&(importer?.endsWith('/api/rss.ts')||importer?.endsWith('/api/notifications.ts')))return'\0rss-state';
  if(source==='virtual:rss-state'||/\/native\/local-db(?:\.ts)?$/.test(source)||/\/stores\/auth(?:\.ts)?$/.test(source)||/\/api\/http(?:\.ts)?$/.test(source))return'\0rss-state';
  if(source==='virtual:rss-state-entry')return'\0rss-state-entry';if(source==='./rss-sync-runtime')return'\0rss-state-runtime';
  if(source==='../../api/rss'&&importer?.endsWith('/messages/MessagesHubView.vue'))return'\0rss-state-hub-api';
  if(source.endsWith('AddToLearningButton.vue'))return'\0rss-state-learning';
  if(source.endsWith('LocalOnlyBadge.vue'))return'\0rss-state-learning';
  if(source.endsWith('HeaderActionsPortal.vue')||source.endsWith('ScrollChromePortal.vue'))return'\0rss-state-slot';
  if(source==='../../components'&&importer?.endsWith('/messages/MessagesHubView.vue'))return'\0rss-state-components';
  if(source.endsWith('/email/emails-store'))return'\0rss-state-emails';
  if(source.endsWith('/stores/notification'))return'\0rss-state-notification';
  if(/\/stores\/crypto-config(?:\.ts)?$/.test(source))return'\0rss-state-crypto';
 },load(id){
 if(id==='\0rss-state')return state;if(id==='\0rss-state-entry')return entry;
 if(id==='\0rss-state-runtime')return"import{status}from'virtual:rss-state';export const rssSyncRuntime={status,syncNow:async()=>0}";
 if(id==='\0rss-state-hub-api')return"import{rssApi as actual}from'/src/api/rss.ts';export const rssApi={...actual,async listItems(...args){window.hubRssListReady=false;const result=await actual.listItems(...args);window.hubRssListReady=true;return result}}";
 if(id==='\0rss-state-learning')return"export default {render:()=>null}";
 if(id==='\0rss-state-slot')return"export default {setup(_props,{slots}){return()=>slots.default?.()}}";
 if(id==='\0rss-state-components')return"export const EmptyState={render:()=>null},Skeleton={render:()=>null}";
 if(id==='\0rss-state-emails')return"import{readHubEmails}from'virtual:rss-state';export const listEmails=readHubEmails,markRead=async()=>{}";
 if(id==='\0rss-state-notification')return"const store={inbox:[{id:'A-notice',user_id:'A',workspace_id:'one',source:'task',kind:'info',title:'PRIVATE A NOTIFICATION',body:'',created_at:1,read_at:1,priority:'normal'}],loadInbox:async()=>{},markRead:async()=>{}};export const useNotificationStore=()=>store";
 if(id==='\0rss-state-crypto')return'export const useCryptoConfig=()=>({shouldEncryptField:()=>false})'
 },configureServer(server){server.middlewares.use((req,res,next)=>{if(!req.url.startsWith('/__rss_state'))return next();res.setHeader('Content-Type','text/html');res.end('<button id="alice">Alice</button><button id="bob">Bob</button><button id="offline">Offline</button><button id="online">Online</button><button id="sync">Sync native</button><button id="remote">Remote star</button><button id="remote-read">Remote read</button><button id="list">List view</button><button id="messages">Messages view</button><output id="result"></output><div id="app"></div><script>window.addEventListener("error",e=>document.body.append("ERROR: "+e.message));window.addEventListener("unhandledrejection",e=>document.body.append("REJECTION: "+String(e.reason)))</script><script type="module" src="/@id/__x00__rss-state-entry"></script>')})}}],server:{host:'127.0.0.1',port:0,hmr:false}})
 await server.listen();return{server,url:`http://127.0.0.1:${server.httpServer.address().port}/__rss_state`}
}
