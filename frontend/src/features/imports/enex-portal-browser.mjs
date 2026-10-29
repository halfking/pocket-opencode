import { fileURLToPath } from 'node:url'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createEmailOwnershipBrowserFixture } from '../email/__tests__/fixtures/email-ownership-browser.mjs'

/** Production import, asset/projection SQL, PKM routing and Markdown renderer. */
export async function createEnexPortalBrowserFixture() {
  const cacheDir = await mkdtemp(join(tmpdir(), 'enex-portal-vite-'))
  const fixture = await createEmailOwnershipBrowserFixture({
    html: '<h1>ENEX sync verification</h1><button id="seed">Import Markdown</button><button id="pkm">Open through PKM</button><button id="remote">Remote edit</button><button id="delete">Remote delete</button><button id="alice">Alice</button><button id="bob">Bob</button><pre id="status">Loading</pre><div id="app"></div>',
    plugins: [{name:'enex-boundaries',enforce:'pre',config(){return{cacheDir,resolve:{alias:{'@':fileURLToPath(new URL('../../',import.meta.url))}}}},resolveId(source,importer) {
      if (source === 'virtual:enex-auth' || /\/stores\/auth(?:\.ts)?$/.test(source)) return '\0enex-auth'
      if ((source === './local-db' || source === '../local-db') && importer?.includes('/native/')) return '\0email-owned-db'
      if (source.endsWith('/native/crypto') || (source === './crypto' && importer?.endsWith('/asset-store.ts')) || (source === '../crypto' && importer?.endsWith('/storage.ts'))) return '\0enex-crypto'
      if (source.endsWith('/native/vector')) return '\0enex-vector'
      if (importer?.endsWith('/NoteDetailView.vue') && source === './notes-store') return '\0enex-note-read'
      if (/AddToLearningButton|LocalOnlyBadge/.test(source)) return '\0enex-child'
      if (/\/composables\/useApiError(?:\.ts)?$/.test(source)) return '\0enex-error'
      if (/\/composables\/useToast(?:\.ts)?$/.test(source)) return '\0enex-toast'
      if (/\/composables\/useConfirm(?:\.ts)?$/.test(source)) return '\0enex-confirm'
    },load(id) {
      if (id === '\0enex-auth') return `import{reactive}from'vue';export const auth=reactive({activationStatus:'ready',activationVersion:0,userId:'alice',user:'alice',workspaceId:'one',token:'fixture'});export const useAuthStore=()=>auth;export const captureAuthSession=()=>({version:auth.activationVersion});export const assertAuthSession=(_a,s)=>{if(s.version!==auth.activationVersion)throw new DOMException('expired','AbortError')};export const captureAuthAttempt=captureAuthSession;export const assertAuthAttempt=assertAuthSession;export const isCurrentAuthSession=(_a,s)=>s.version===auth.activationVersion;export const matchesAuthSession=isCurrentAuthSession;`
      if (id === '\0enex-crypto') return 'export const encryptString=async s=>s;export const decryptString=async s=>s'
      if (id === '\0enex-vector') return 'export const vectorIndex={remove:async()=>{},add:async()=>{},existingChunks:async()=>null,search:async()=>[]}'
      if (id === '\0enex-note-read') return "export{getNote,loadFullContent,deleteNote,updateNote}from'/src/features/notes/notes-persist.ts';export const searchSemantic=async()=>[];"
      if (id === '\0enex-child') return "import{h}from'vue';export default{setup(){return()=>h('span')}}"
      if (id === '\0enex-error') return 'export const useApiError=()=>e=>e.message'
      if (id === '\0enex-toast') return 'export const useToast=()=>({success(){},error(){},info(){}})'
      if (id === '\0enex-confirm') return 'export const useConfirm=()=>({confirm:async()=>true})'
    }}],
    entry: `
      import{createApp,nextTick,h}from'vue';import{createPinia,setActivePinia}from'pinia';import{createI18n}from'vue-i18n';
      import{createRouter,createMemoryHistory,RouterView}from'vue-router';
      import Pkm from '/src/features/pkm/PkmNoteView.vue';import Note from '/src/features/notes/NoteDetailView.vue';
      import{localDB,activate}from'virtual:email-owned-db';import{auth}from'virtual:enex-auth';
      import{assetStore}from'/src/native/asset-store.ts';import{importEnex}from'/src/features/imports/import-service.ts';
      import{portalNotesStore}from'/src/native/portal-sync/storage.ts';import{applyPage}from'/src/native/portal-sync/apply.ts';import{notePayload}from'/src/native/portal-sync/contract.ts';
      import messages from '/src/locales/en-US.json';
      const pinia=createPinia();setActivePinia(pinia);const store=()=>portalNotesStore(localDB.bindOperation(localDB.captureOperation({kind:'local-edit'})));
      const router=createRouter({history:createMemoryHistory(),routes:[{path:'/notes',component:{render:()=>h('p','No active note')}},{path:'/notes/:id',component:Note},{path:'/pkm/today',component:{render:()=>h('p','PKM list')}},{path:'/pkm/n/:id',component:Pkm}]});
      await activate({userId:'alice',workspaceId:'one'});await store().observeEpoch(0);await router.push('/notes');
      createApp(RouterView).use(pinia).use(router).use(createI18n({legacy:false,locale:'en-US',messages:{'en-US':messages}})).mount('#app');
      let id,seq=0;async function inspect(){document.querySelector('#status').textContent=JSON.stringify({owner:auth.userId,path:router.currentRoute.value.path,attachments:id?(await assetStore.listBlobs(id)).length:0,pending:(await store().pending()).map(i=>({format:i.payload.format,content:i.payload.content,tags:i.payload.tags,time:i.client_updated_at}))})}
      async function login(userId){auth.activationStatus='pending';await activate({userId,workspaceId:'one'});auth.userId=userId;auth.user=userId;auth.activationVersion++;await store().observeEpoch(0);auth.activationStatus='ready';await nextTick();await inspect()}
      document.querySelector('#seed').onclick=async()=>{await importEnex('<en-export><note><title>Imported Markdown</title><content><![CDATA[<en-note><h1>Original Markdown</h1><p><b>Full body</b></p><en-media hash="resource-hash"/></en-note>]]></content><created>20240301T010203Z</created><updated>20240302T010203Z</updated><tag>Smith, John</tag><resource><data encoding="base64">UFJJVkFURSBBVFRBQ0hNRU5U</data><mime>text/plain</mime></resource></note></en-export>','one');id=(await assetStore.search({kind:'note',source:'enex_import',workspaceId:'one'}))[0].id;await inspect()};
      document.querySelector('#pkm').onclick=()=>router.push('/pkm/n/'+id).then(()=>nextTick()).then(inspect);
      async function remote(deleted){const row=await store().find('asset',id),old=await store().decode(row.item_json);await applyPage(store(),{changes:[{...old,title:'Portal Markdown',payload:deleted?{}:notePayload({...old.payload,content:'# Remote Markdown\\n\\n**Exact** body'}),deleted,client_updated_at:new Date(Date.now()+1000).toISOString(),op_id:'remote-'+(++seq),change_seq:seq}],epochs:{notes:0},next_cursor:String(seq)});window.dispatchEvent(new Event('openpocket:portal-notes-applied'));await nextTick();await inspect()}
      document.querySelector('#remote').onclick=()=>remote(false);document.querySelector('#delete').onclick=()=>remote(true);
      document.querySelector('#alice').onclick=()=>login('alice');document.querySelector('#bob').onclick=()=>login('bob');
      window.enexFixture={router,inspect};await inspect();
    `,
  })
  const close = fixture.server.close.bind(fixture.server)
  fixture.server.close = async () => { await close(); await rm(cacheDir, { recursive: true, force: true }) }
  return fixture
}
if(process.argv[1]===fileURLToPath(import.meta.url)){const f=await createEnexPortalBrowserFixture();console.log(f.url)}
