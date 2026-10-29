import { createServer } from 'vite'
import { fileURLToPath } from 'node:url'
import vue from '@vitejs/plugin-vue'

/** Real mail store SQL and lease implementation; SQL.js supplies the isolated storage driver. */
export async function createEmailOwnershipBrowserFixture() {
  const entry = `
    import {createApp, h, KeepAlive, defineComponent, reactive, ref, onMounted, nextTick} from 'vue';
    import {createRouter,createMemoryHistory} from 'vue-router';
    import {createI18n} from 'vue-i18n';
    import App from '/src/app/App.vue';
    import {fixtureDb,activate} from 'virtual:email-owned-db';
    import * as emails from '/src/features/email/emails-store.ts';
    import * as folders from '/src/features/email/email-folders-store.ts';
    import * as invoices from '/src/features/email/invoices-store.ts';
    import * as cache from '/src/features/email/email-body-cache.ts';
    const auth=reactive({activationStatus:'ready',activationVersion:0,userId:'alice',workspaceId:'one'});
    window.__mailFixtureAuth=auth;
    const mailView=(name,read)=>defineComponent({name,setup(){
      const rows=ref([]),selection=ref('');
      onMounted(async()=>{rows.value=await read()});
      return ()=>h('section',{id:'cached-mail-view'},[
        h('pre',{id:'cached-mail-rows'},JSON.stringify(rows.value)),
        h('button',{id:'select-preview',onClick:()=>selection.value='Alice private preview selection'},'Select preview'),
        h('pre',{id:'cached-mail-selection'},selection.value),
      ]);
    }});
    const Inbox=mailView('EmailInboxView',async()=>(await emails.listEmails({folder:'__all__'})).map(row=>row.subject));
    const Invoices=mailView('InvoiceListView',async()=>(await invoices.listLocalPage({})).rows.map(row=>row.title));
    const Other=defineComponent({name:'MeetingListView',setup(){const draft=ref('');return()=>h('input',{id:'other-draft',value:draft.value,onInput:e=>draft.value=e.target.value})}});
    const router=createRouter({history:createMemoryHistory(),routes:[{path:'/email',component:Inbox},{path:'/email/invoices',component:Invoices},{path:'/other',component:Other}]});
    let routesApp;
    async function mountRoutes(){if(routesApp)return;routesApp=createApp(App);await router.push('/email');await router.isReady();routesApp.use(router).use(createI18n({legacy:false,locale:'en',messages:{en:{}}})).mount('#routes')}
    document.querySelector('#route-mail').onclick=()=>router.push('/email');
    document.querySelector('#route-invoice').onclick=()=>router.push('/email/invoices');
    document.querySelector('#route-other').onclick=()=>router.push('/other');
    async function inspect(){
      const accounts=await emails.listAccounts(), messages=await emails.listEmails({folder:'__all__'});
      const rows=(await invoices.listLocalPage({})).rows;
      document.querySelector('#result').textContent=JSON.stringify({accounts:accounts.map(x=>x.displayName),messages:messages.map(x=>x.subject),pending:await folders.countPendingOps(),body:await cache.readEmailBodyLocal('same-mail'),invoices:rows.map(x=>x.title)});
    }
    async function login(userId,workspaceId){auth.activationStatus='pending';activate({userId,workspaceId});auth.userId=userId;auth.workspaceId=workspaceId;auth.activationVersion++;auth.activationStatus='ready';document.querySelector('#owner').textContent=userId+'/'+workspaceId;await nextTick();await nextTick();await inspect()}
    document.querySelector('#alice').onclick=()=>login('alice','one');
    document.querySelector('#bob').onclick=()=>login('bob','one');
    document.querySelector('#workspace').onclick=()=>login('alice','two');
    document.querySelector('#seed').onclick=async()=>{
      const id=await emails.saveAccount({displayName:'Alice private account',emailAddress:'alice@example.com',imapHost:'imap.example.com',password:'TEST-ONLY'});
      await emails.upsertEmail({id:'same-mail',accountId:id,fromAddress:'sender@example.com',date:1,subject:'Alice private mail'});
      await folders.recordOpsEntry({accountId:id,emailId:'same-mail',uid:1,action:'delete',targetFolder:'',subject:'Alice private mail'});
      await invoices.upsertFromServer([{id:'same-invoice',emailId:'same-mail',accountId:id,kind:'bill',category:'other',title:'Alice private invoice',seller:'seller',amount:3,currency:'CNY',subject:'private',status:'new',extractedBy:'rule',createdAt:1,updatedAt:1}]);
      await cache.writeEmailBodyLocal('same-mail','Alice private body');await inspect();mountRoutes();
    };
    window.emailOwnership={inspect,fixtureDb};await login('alice','one');
  `
  const db = `
    import initSqlJs from 'sql.js';
    import {SCHEMA_SQL} from '/src/native/schema.ts';
    import {LocalDbLeases} from '/src/native/local-db-leases.ts';
    const SQL=await initSqlJs({locateFile:()=>'/node_modules/sql.js/dist/sql-wasm.wasm'});
    export const fixtureDb=new SQL.Database();const leases=new LocalDbLeases();
    fixtureDb.run(SCHEMA_SQL.replace(/CREATE VIRTUAL TABLE[^;]+;/g,'').replace(/CREATE TRIGGER[^]*?END;/g,''));
    function query(sql,values=[]){const stmt=fixtureDb.prepare(sql);try{stmt.bind(values);const rows=[];while(stmt.step())rows.push(stmt.getAsObject());return rows}finally{stmt.free()}}
    export const activate=owner=>leases.activateOwner(leases.prepareOwner(owner),()=>undefined);
    export const localDB={captureOperation:options=>leases.capture(options),bindOperation:lease=>({owner:lease.owner.principal,assertCurrent:()=>leases.assertCurrent(lease),
      query:async(...args)=>{leases.assertCurrent(lease);return query(...args)},queryOne:async(...args)=>{leases.assertCurrent(lease);return query(...args)[0]??null},run:async(sql,values=[])=>{leases.assertCurrent(lease);fixtureDb.run(sql,values);return fixtureDb.getRowsModified()}})};
  `
  const server = await createServer({ root: fileURLToPath(new URL('../../../../../', import.meta.url)), configFile: false,
    optimizeDeps: { noDiscovery: true, include: ['sql.js', 'vue', 'pinia', 'vue-router', 'vue-i18n'] },
    plugins: [vue(), { name: 'email-ownership-browser', enforce: 'pre',
      resolveId(source, importer) {
        if (importer?.endsWith('/app/App.vue')) {
          if (source === '../stores/auth') return '\0mail-app-auth'
          if (source === './AppLayout.vue') return '\0mail-app-layout'
          if (source.endsWith('.vue')) return '\0mail-empty-view'
          if (source.endsWith('/useSwipeBack')) return '\0mail-swipe'
          if (source.endsWith('/useStatusBar')) return '\0mail-status'
          if (source.endsWith('/useKeyboardInset')) return '\0mail-keyboard'
        }
        if (source === 'virtual:email-owned-entry') return '\0email-owned-entry'
        if (source === 'virtual:email-owned-db' || source.endsWith('/native/local-db')) return '\0email-owned-db'
        if (source.endsWith('/native/crypto')) return '\0email-owned-crypto'
      }, load(id) {
        if (id === '\0mail-app-auth') return 'export const useAuthStore=()=>window.__mailFixtureAuth'
        if (id === '\0mail-app-layout') return "import{h}from'vue';export default{setup(_,{slots}){return()=>h('main',{id:'main'},slots.default?.())}}"
        if (id === '\0mail-empty-view') return "import{h}from'vue';export default{setup(){return()=>h('span')}}"
        if (id === '\0mail-swipe') return 'export const useSwipeBack=()=>{}'
        if (id === '\0mail-status') return 'export const useStatusBar=()=>({start(){},stop(){}})'
        if (id === '\0mail-keyboard') return 'export const installKeyboardInset=()=>{}'
        if (id === '\0email-owned-entry') return entry
        if (id === '\0email-owned-db') return db
        if (id === '\0email-owned-crypto') return 'export const encryptString=async s=>s'
      }, configureServer(server) {
        server.middlewares.use((req,res,next)=>{
          if(!req.url.startsWith('/__email_ownership__')) return next()
          res.setHeader('Content-Type','text/html; charset=utf-8')
          res.end('<h1>Mail ownership verification</h1><div id="owner"></div><button id="alice">Alice</button><button id="bob">Bob</button><button id="workspace">Alice workspace two</button><button id="seed">Create Alice mail</button><pre id="result">Loading</pre><button id="route-mail">Email route</button><button id="route-invoice">Invoice route</button><button id="route-other">Other route</button><div id="routes"></div><script type="module" src="/@id/__x00__email-owned-entry"></script>')
        })
      },
    }], server: {host:'127.0.0.1',port:0,hmr:false} })
  await server.listen()
  return {server,url:`http://127.0.0.1:${server.httpServer.address().port}/__email_ownership__`}
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const fixture=await createEmailOwnershipBrowserFixture()
  console.log(fixture.url)
}
