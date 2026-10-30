import {createServer} from 'vite'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath} from 'node:url'
import {mkdtemp} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'

/** Production panel, adapter, record SQL and leases; only account/platform/network boundaries are isolated. */
export async function createRssPortalBrowserFixture(){
  const db=`
    import initSqlJs from 'sql.js';import{reactive}from'vue';import{LocalDbLeases}from'/src/native/local-db-leases.ts';
    const SQL=await initSqlJs({locateFile:()=>'/node_modules/sql.js/dist/sql-wasm.wasm'});export const raw=new SQL.Database();const leases=new LocalDbLeases();
    export const auth=reactive({activationStatus:'ready',activationVersion:0,userId:'A',user:'A',workspaceId:'one'});
    function query(sql,values=[]){const s=raw.prepare(sql);try{s.bind(values);const rows=[];while(s.step())rows.push(s.getAsObject());return rows}finally{s.free()}}
    export function activate(user,workspace){auth.activationStatus='pending';leases.activateOwner(leases.prepareOwner({userId:user,workspaceId:workspace}),()=>undefined);auth.userId=user;auth.user=user;auth.workspaceId=workspace;auth.activationVersion++;auth.activationStatus='ready'}
    export const localDB={captureOperation:options=>leases.capture(options),bindOperation:lease=>({schemaConnection:raw,owner:lease.owner.principal,assertCurrent:()=>leases.assertCurrent(lease),
      run:async(sql,values=[])=>{leases.assertCurrent(lease);raw.run(sql,values);return raw.getRowsModified()},query:async(...args)=>{leases.assertCurrent(lease);return query(...args)},queryOne:async(...args)=>{leases.assertCurrent(lease);return query(...args)[0]??null},
      runInTransaction:async statements=>{leases.assertCurrent(lease);raw.run('BEGIN');try{for(const s of statements){leases.assertCurrent(lease);raw.run(s.statement,s.values)}raw.run('COMMIT')}catch(e){raw.run('ROLLBACK');throw e}}
    })};activate('A','one');
    export const status=reactive({failed:false,running:false,blocked:0,resetPending:false,resetBlocked:false,epochUnknown:false});
    export const rssSyncRuntime={status,syncNow:async()=>0};
  `
  const entry=`
    import{createApp,h,KeepAlive,ref}from'vue';import{createI18n}from'vue-i18n';import{createRouter,createMemoryHistory}from'vue-router';import messages from'/src/locales/en-US.json';
    import Panel from'/src/features/rss/RssListView.vue';import Detail from'/src/features/rss/RssItemDetail.vue';import Confirm from'/src/components/base/ConfirmDialog.vue';import{pauseNextSourceRead,releaseSourceRead}from'virtual:rss-api';
    import{auth,activate,localDB,status}from'virtual:rss-db';import{rssPortalStore}from'/src/features/rss/rss-sync-store.ts';import{applyRecordPage}from'/src/native/portal-sync/record-engine.ts';
    const show=ref('panel'),Blank={render:()=>h('p','RSS panel cached')};const router=createRouter({history:createMemoryHistory(),routes:[{path:'/article/:id',component:Detail}]});await router.push('/article/fetched-1');
    async function epoch(){await rssPortalStore(localDB.bindOperation(localDB.captureOperation({kind:'local-edit'}))).observeEpoch(0)};await epoch();
    createApp({render:()=>h('main',[h(KeepAlive,null,()=>show.value==='panel'?h(Panel):show.value==='detail'?h(Detail):h(Blank)),h(Confirm)])}).use(router).use(createI18n({legacy:false,locale:'en-US',messages:{'en-US':messages}})).mount('#app');
    for(const[id,user,ws]of[['alice','A','one'],['bob','B','one'],['workspace','A','two']])document.getElementById(id).onclick=async()=>{activate(user,ws);await epoch()};
    document.getElementById('hide').onclick=()=>show.value=show.value==='panel'?'blank':'panel';document.getElementById('detail').onclick=()=>show.value='detail';
    let seq=0;async function remote(deleted=false){const store=rssPortalStore(localDB.bindOperation(localDB.captureOperation({kind:'local-edit'})));await store.observeEpoch(0);seq++;await applyRecordPage(store,{changes:[{module:'rss',kind:'item',ext_id:'remote',title:'REMOTE '+auth.userId+'/'+auth.workspaceId,payload:deleted?{}:{source:'Remote source',summary:'Remote summary',url:''},client_updated_at:new Date(Date.now()+seq).toISOString(),op_id:'remote-'+seq,deleted,epoch:0,change_seq:seq}],next_cursor:String(seq),epoch:0});window.dispatchEvent(new Event('openpocket:portal-rss-applied'))}
    document.getElementById('remote').onclick=()=>remote();document.getElementById('remove').onclick=()=>remote(true);
    document.getElementById('failed').onclick=()=>status.failed=!status.failed;
    window.rssFixture={auth,localDB,remote,pauseNextSourceRead,releaseSourceRead};
  `
  const root=fileURLToPath(new URL('../../../../',import.meta.url))
  const cacheDir=await mkdtemp(join(tmpdir(),'rss-portal-vite-'))
  const server=await createServer({root,configFile:false,cacheDir,optimizeDeps:{noDiscovery:true,include:['sql.js','vue','pinia','vue-i18n']},plugins:[vue(),{
    name:'rss-portable-browser',enforce:'pre',resolveId(source){
      if(source==='virtual:rss-db'||/\/native\/local-db(?:\.ts)?$/.test(source)||/\/stores\/auth(?:\.ts)?$/.test(source)||source==='./rss-sync-runtime')return '\0rss-db'
      if(source==='virtual:rss-entry')return '\0rss-entry'
      if(/\/api\/http(?:\.ts)?$/.test(source))return '\0rss-http'
      if(/\/stores\/crypto-config(?:\.ts)?$/.test(source))return '\0rss-crypto-config'
      if(source.endsWith('AddToLearningButton.vue'))return '\0rss-learning'
      if(source==='virtual:rss-api'||/\/api\/rss(?:\.ts)?$/.test(source))return '\0rss-api'
      if(source.endsWith('LocalOnlyBadge.vue'))return '\0rss-learning'
    },load(id){
      if(id==='\0rss-db')return db+`;export const useAuthStore=()=>auth;export const captureAuthSession=()=>auth.activationVersion;export function assertAuthSession(_auth,stamp){if(stamp!==auth.activationVersion||auth.activationStatus!=='ready')throw new DOMException('Former fixture session','AbortError')}`
      if(id==='\0rss-entry')return entry
      if(id==='\0rss-http')return "export async function http(){throw new Error('Unexpected HTTP request in portable-only RSS fixture')}"
      if(id==='\0rss-crypto-config')return 'export const useCryptoConfig=()=>({shouldEncryptField:()=>false})'
      if(id==='\0rss-learning')return "export default{render:()=>null}"
      if(id==='\0rss-api')return `import{auth}from'virtual:rss-db';let nextPause=null,release;export function pauseNextSourceRead(){window.__rssReadStarted=false;nextPause=new Promise(resolve=>release=resolve)};export const releaseSourceRead=()=>release?.();const read=(id,options)=>{options?.assertCurrent?.();return{id,title:'Fetched '+auth.userId,summary:'Chosen summary',sourceId:'feed-source',url:'https://example.test/article',content:'FULL ARTICLE BODY MUST NOT ENTER PORTAL',status:'read',categories:[]}};export const rssApi={listSources:async options=>{const user=auth.userId;options?.assertCurrent?.();if(nextPause){const wait=nextPause;nextPause=null;window.__rssReadStarted=true;await wait}options?.assertCurrent?.();return[{id:user,title:'Backend '+user+' source',url:'https://example.test/rss',enabled:true}]},listItems:async(_opts,options)=>{options?.assertCurrent?.();return[{id:'backend-'+auth.userId,title:'Backend '+auth.userId+' article',status:'read',categories:[]}]},getItem:async(...args)=>read(...args),markRead:async(...args)=>read(...args),setStarred:async(id,_star,options)=>read(id,options),shareCardUrl:()=>'',share:async()=>({})}`
    },configureServer(server){server.middlewares.use((req,res,next)=>{if(!req.url.startsWith('/__rss_portal'))return next();res.setHeader('Content-Type','text/html; charset=utf-8');res.end('<button id="alice">Alice</button><button id="bob">Bob</button><button id="workspace">Alice other workspace</button><button id="hide">Hide/restore RSS</button><button id="detail">Article detail</button><button id="remote">Remote update</button><button id="remove">Remote delete</button><button id="failed">Sync error</button><div id="app"></div><script type="module" src="/@id/__x00__rss-entry"></script>')})}
  }],server:{host:'127.0.0.1',port:0,hmr:false}})
  await server.listen();return{server,url:`http://127.0.0.1:${server.httpServer.address().port}/__rss_portal`}
}
if(process.argv[1]===fileURLToPath(import.meta.url)){const f=await createRssPortalBrowserFixture();process.stdout.write(f.url+'\n')}
