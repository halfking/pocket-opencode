import {createServer} from 'vite'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath} from 'node:url'

/** Actual cached list components and owner view gates; only account/data boundaries are isolated. */
const shared = `
import {reactive,ref} from 'vue';
export const auth=reactive({userId:'A',user:'A',workspaceId:'wsA',activationVersion:1,activationStatus:'ready',token:'fixture'});
export const recording=ref(false);
const data=new Map();
const key=()=>auth.userId+':'+auth.workspaceId;
export async function listNotes(opts={}) { const k=auth.userId+':'+(opts.workspaceId||auth.workspaceId); if(!data.has(k))data.set(k,[{id:k,title:k+' private',content:k+' body',html:k+' body',createdAt:1,updatedAt:1,workspaceId:opts.workspaceId}]); return structuredClone(data.get(k)); }
export function account(user,workspace){auth.activationStatus='pending';auth.userId=user;auth.user=user;auth.workspaceId=workspace;auth.activationVersion++;auth.activationStatus='ready'}
export function remote(deleted=false){data.set(key(),deleted?[]:[{id:key(),title:key()+' private REMOTE',content:'remote body',html:'remote body',createdAt:1,updatedAt:2}]);window.dispatchEvent(new Event('openpocket:portal-notes-applied'))}
`
const entry = `
import {createApp,h,KeepAlive} from 'vue';import {createRouter,createMemoryHistory,RouterView} from 'vue-router';
import Notes from '/src/features/notes/NoteListView.vue';import Pkm from '/src/features/pkm/PkmTodayView.vue';import {auth,account,remote,recording} from 'virtual:view-shared';
const router=createRouter({history:createMemoryHistory(),routes:[{path:'/notes',component:Notes},{path:'/pkm',component:Pkm},{path:'/blank',component:{template:'<p>缓存页面已隐藏</p>'}}]});
await router.push('/notes');
createApp({render:()=>h(RouterView,null,{default:({Component})=>h(KeepAlive,{include:['NoteListView','PkmTodayView']},()=>Component?h(Component):null)})}).use(router).mount('#app');
document.querySelector('#notes').onclick=()=>router.push('/notes');document.querySelector('#pkm').onclick=()=>router.push('/pkm');document.querySelector('#blank').onclick=()=>router.push('/blank');
document.querySelector('#account').onclick=()=>account(auth.userId==='A'?'B':'A',auth.userId==='A'?'wsB':'wsA');document.querySelector('#workspace').onclick=()=>account(auth.userId,'wsC');
document.querySelector('#remote').onclick=()=>remote();document.querySelector('#delete').onclick=()=>remote(true);
window.viewFixture={auth,recording};
`
export async function createViewsBrowserFixture(){
 const root=fileURLToPath(new URL('../../../../',import.meta.url));
 const server=await createServer({root,configFile:false,resolve:{alias:{'@':root+'/src'}},optimizeDeps:{noDiscovery:true},plugins:[vue(),{
 name:'notes-owner-views-fixture',enforce:'pre',
 resolveId(source,importer){
  if(source==='virtual:view-shared')return '\0view-shared';
  if(source==='virtual:view-entry')return '\0view-entry';
  if(/\/stores\/auth(?:\.ts)?$/.test(source))return '\0view-auth';
  if(/\/composables\/useApiError$/.test(source))return '\0view-error';
  if(/\/composables\/useToast$/.test(source))return '\0view-toast';
  if(source.endsWith('/components')&&importer?.endsWith('View.vue'))return '\0view-components';
  if(source.endsWith('LocalOnlyBadge.vue')||source.endsWith('HeaderActionsPortal.vue')||/NoteRecordingStudio|NoteMetaSheet|NoteSearchBrief|VoiceRecorderWidget/.test(source))return '\0view-child';
  if(importer?.endsWith('NoteListView.vue')){
   if(source==='./notes-store')return '\0view-notes';
   if(source==='./useNoteRecording')return '\0view-recording';
   if(source==='./note-search')return '\0view-search';
  }
  if(importer?.endsWith('PkmTodayView.vue')&&source==='./pkm-store')return '\0view-pkm';
 },
 load(id){
  if(id==='\0view-shared')return shared;
  if(id==='\0view-entry')return entry;
  if(id==='\0view-auth')return `export{auth}from'virtual:view-shared';import{auth}from'virtual:view-shared';export const useAuthStore=()=>auth;export const captureAuthAttempt=()=>({});export const assertAuthAttempt=()=>{};export const captureAuthSession=()=>({});export const assertAuthSession=()=>{};export const isCurrentAuthSession=()=>true`;
  if(id==='\0view-error')return 'export const useApiError=()=>((error,fallback)=>fallback)';
  if(id==='\0view-toast')return 'export const useToast=()=>({error(){},success(){},info(){}})';
  if(id==='\0view-child')return `import{h}from'vue';export default{setup(_,{slots}){return()=>h('div',slots.default?.())}}`;
  if(id==='\0view-components')return `import{h}from'vue';const C={props:['title','message'],setup(p){return()=>h('div',p.title||p.message||'')}};export const Skeleton=C,EmptyState=C,DbLockedState=C,WaveformVisualizer=C,ErrorState=C,Loading=C`;
  if(id==='\0view-recording')return `import{ref}from'vue';import{recording}from'virtual:view-shared';export const useNoteRecording=()=>({phase:ref('idle'),recording,transcript:ref(''),error:ref(''),toggle:async()=>null,cancelTranscription(){},consumePendingResult:()=>null})`;
  if(id==='\0view-notes')return `export{listNotes}from'virtual:view-shared';export const listDraftNotes=async()=>[];export const getNote=async()=>null;export const createNote=async()=>null;export const updateNote=async()=>{};export const deleteNote=async()=>{};export const loadFullContent=async n=>n.content;`;
  if(id==='\0view-search')return `import{listNotes}from'virtual:view-shared';export const searchNotesWithIntent=async()=>({results:(await listNotes()).map(note=>({note})),summary:'',offline:true})`;
  if(id==='\0view-pkm')return `export{listNotes}from'virtual:view-shared';import{listNotes}from'virtual:view-shared';export const searchNotes=async()=>listNotes();export const getDailyNote=async()=>null;export const getOrCreateDailyNote=async()=>null;export const dailyKey=()=> '2026-10-30'`;
 },
 configureServer(server){server.middlewares.use(async(req,res,next)=>{if(!req.url.startsWith('/__notes_owner_views'))return next();res.setHeader('Content-Type','text/html; charset=utf-8');res.end('<button id="notes">普通笔记</button><button id="pkm">PKM 笔记</button><button id="blank">隐藏并缓存</button><button id="account">切换账号</button><button id="workspace">切换工作区</button><button id="remote">远端更新</button><button id="delete">远端删除</button><div id="app"></div><script type="module" src="/@id/__x00__view-entry"></script>')})}
 }],server:{host:'127.0.0.1',port:0,hmr:false}});
 await server.listen();return {server,url:'http://127.0.0.1:'+server.httpServer.address().port+'/__notes_owner_views'}
}
if(process.argv[1]===fileURLToPath(import.meta.url)){const f=await createViewsBrowserFixture();process.stdout.write(f.url+'\n')}
