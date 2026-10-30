import { fileURLToPath } from 'node:url'
import { createEmailOwnershipBrowserFixture } from './email-ownership-browser.mjs'

export async function createPortalMailBrowserFixture() {
  return createEmailOwnershipBrowserFixture({
    html: '<h1>Portal mail verification</h1><button id="seed">Create local mail</button><button id="alice">Alice</button><button id="bob">Bob</button><button id="workspace">Other workspace</button><button id="remote">Remote update</button><button id="remote-delete">Remote delete</button><button id="legacy">Use legacy identity</button><button id="native-edit">Edit native summary</button><button id="clock">Correct server clock</button><pre id="recovery"></pre><pre id="source"></pre><div id="metadata"></div>',
    plugins: [{ name: 'portal-mail-ui-boundaries', enforce: 'pre',
      resolveId(source, importer) {
        if (importer?.endsWith('/PortalMailMetadata.vue') && source.endsWith('/stores/auth')) return '\0portal-mail-auth'
        if (source.endsWith('/portal-mail-runtime')) return '\0portal-mail-runtime'
      }, load(id) {
        if (id === '\0portal-mail-auth') return 'export const useAuthStore=()=>window.__mailFixtureAuth'
        if (id === '\0portal-mail-runtime') return "import{reactive}from'vue';export const portalEmailRuntime={status:reactive({failed:false,running:false,blocked:0,resetPending:false})};export const syncPortalMailNow=async()=>0"
      } }],
    entry: `
      import {createApp,reactive,nextTick} from 'vue';
      import {createPinia,setActivePinia} from 'pinia';
      import {createI18n} from 'vue-i18n';
      import messages from '/src/locales/en-US.json';
      import PortalMailMetadata from '/src/features/email/PortalMailMetadata.vue';
      import {localDB,fixtureDb,activate} from 'virtual:email-owned-db';
      import * as emails from '/src/features/email/emails-store.ts';
      import * as cache from '/src/features/email/email-body-cache.ts';
      import {portalMailStore} from '/src/features/email/portal-mail-store.ts';
      import {applyRecordPage} from '/src/native/portal-sync/record-engine.ts';
      const pinia=createPinia();setActivePinia(pinia);
      const auth=reactive({activationStatus:'ready',activationVersion:0,userId:'alice',workspaceId:'one'});window.__mailFixtureAuth=auth;
      const store=()=>portalMailStore(localDB.bindOperation(localDB.captureOperation({kind:'local-edit'})));
      let accountId,sequence=0;
      async function login(userId,workspaceId){auth.activationStatus='pending';await activate({userId,workspaceId});auth.userId=userId;auth.workspaceId=workspaceId;auth.activationVersion++;await store().observeEpoch(0);auth.activationStatus='ready';await nextTick()}
      await login('alice','one');
      const inspect=async()=>{document.querySelector('#source').textContent=JSON.stringify({accounts:(await emails.listAccounts()).length,mail:(await emails.getEmail('mail'))?.subject,body:await cache.readEmailBodyLocal('mail')})};
      document.querySelector('#alice').onclick=()=>login('alice','one').then(inspect);
      document.querySelector('#bob').onclick=()=>login('bob','one').then(inspect);
      document.querySelector('#workspace').onclick=()=>login('alice','two').then(inspect);
      document.querySelector('#seed').onclick=async()=>{accountId=await emails.saveAccount({displayName:'Mailbox A',emailAddress:'a@example.com',imapHost:'private-imap.local',password:'PRIVATE_CREDENTIAL'});await emails.upsertEmail({id:'mail',accountId,fromAddress:'sender@example.com',subject:'Original mail',snippet:'Original summary',date:1,updatedAt:Date.now()});await cache.writeEmailBodyLocal('mail','PRIVATE BODY');await inspect()};
      const recovery=value=>document.querySelector('#recovery').textContent=JSON.stringify(value);
      document.querySelector('#legacy').onclick=async()=>{
        const s=store(),row=await s.byExternalId('summary:mail','summary'),old=await s.decode(row.item_json);
        for(const item of await s.pending())await s.acknowledge(item,{...item,current_epoch:0,outcome:'applied'});
        const legacy={...old,ext_id:'email:mail'};
        fixtureDb.run("UPDATE local_portal_records SET source_id='mail',ext_id=?,item_json=? WHERE owner_user_id='alice' AND owner_workspace_id='one' AND module='email' AND kind='summary'",[legacy.ext_id,await s.encode(legacy)]);
        window.dispatchEvent(new Event('openpocket:portal-email-applied'));recovery({state:'legacy-ready',extId:legacy.ext_id});await inspect();
      };
      document.querySelector('#native-edit').onclick=async()=>{
        await emails.setAiSummary('mail','LEGACY NATIVE EDIT');
        const pending=(await store().pending()).filter(item=>item.kind==='summary');
        recovery({state:'legacy-edited',extIds:pending.map(item=>item.ext_id),count:pending.length});await inspect();
      };
      document.querySelector('#clock').onclick=async()=>{
        const s=store(),old=await s.decode((await s.find('mail')).item_json),authoritative=new Date(Date.parse(old.client_updated_at)-5000).toISOString();
        await s.acknowledge(old,{...old,current_epoch:0,outcome:'rejected',clock_skew:true,client_updated_at:authoritative});
        const retry=(await s.pending()).find(item=>item.ext_id===old.ext_id&&item.op_id!==old.op_id);
        const acknowledged=await s.acknowledge(retry,{...retry,current_epoch:0,outcome:'applied'});
        recovery({state:'clock-acknowledged',acknowledged,originalOp:old.op_id,newOp:retry.op_id,extId:retry.ext_id,authoritative,updatedAt:retry.client_updated_at,contentPreserved:JSON.stringify(old.payload)===JSON.stringify(retry.payload),remaining:(await s.pending()).length});
        window.dispatchEvent(new Event('openpocket:portal-email-applied'));await inspect();
      };
      async function remote(deleted){const row=await store().byExternalId('summary:mail','summary'),old=await store().decode(row.item_json);await applyRecordPage(store(),{epoch:0,next_cursor:String(++sequence),changes:[{...old,title:'Remote mail',payload:deleted?{}:{...old.payload,snippet:'REMOTE SUMMARY'},deleted,client_updated_at:new Date(Date.now()+10000).toISOString(),op_id:'remote-'+sequence,change_seq:sequence}]});window.dispatchEvent(new Event('openpocket:portal-email-applied'));await inspect()}
      document.querySelector('#remote').onclick=()=>remote(false);document.querySelector('#remote-delete').onclick=()=>remote(true);
      createApp(PortalMailMetadata).use(pinia).use(createI18n({legacy:false,locale:'en-US',messages:{'en-US':messages}})).mount('#metadata');
      window.portalMailFixture={fixtureDb,inspect};await inspect();
    `,
  })
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const fixture = await createPortalMailBrowserFixture()
  console.log(fixture.url)
}
