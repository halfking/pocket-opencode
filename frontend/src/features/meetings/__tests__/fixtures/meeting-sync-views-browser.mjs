import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

const shared = `
import{reactive}from'vue';export const auth=reactive({userId:'A',workspaceId:'one',activationVersion:1,activationStatus:'ready'});
const data=new Map();const key=()=>auth.userId+':'+auth.workspaceId;
export async function listMeetings(){if(!data.has(key()))data.set(key(),[{id:key(),title:key()+' private meeting',participants:[],durationMs:1000,startedAt:1,createdAt:1,updatedAt:1,status:'completed',summary:'Minutes'}]);return structuredClone(data.get(key()))}
export function account(user,workspace){auth.activationStatus='pending';auth.userId=user;auth.workspaceId=workspace;auth.activationVersion++;auth.activationStatus='ready'}
export function remote(deleted){data.set(key(),deleted?[]:[{id:key(),title:key()+' REMOTE meeting',participants:[],durationMs:1000,startedAt:1,createdAt:1,updatedAt:2,status:'completed',summary:'Remote minutes'}]);window.dispatchEvent(new Event('openpocket:portal-meetings-applied'))}
export const status=reactive({failed:true,running:false,blocked:1,resetPending:false,resetBlocked:false,epochUnknown:true});let retries=0;export const runtime={status,syncNow:async()=>{document.querySelector('#retry-count').textContent=String(++retries)}};
`
const entry = `
import{createApp,h,KeepAlive}from'vue';import{createRouter,createMemoryHistory,RouterView}from'vue-router';import{createI18n}from'vue-i18n';
import Meetings from'/src/features/meetings/MeetingListView.vue';import Notice from'/src/features/settings/PortalRecordSyncNotice.vue';import zh from'/src/locales/zh-CN.json';import{auth,account,remote,runtime,status}from'virtual:meeting-views-shared';
const router=createRouter({history:createMemoryHistory(),routes:[{path:'/meetings',component:Meetings},{path:'/other',component:{name:'OtherFixtureView',render:()=>h('input',{id:'other-draft',placeholder:'Unsaved draft'})}}]});await router.push('/meetings');
createApp({render:()=>h('div',[h(RouterView,null,{default:({Component})=>h(KeepAlive,{include:['MeetingListView','OtherFixtureView']},()=>Component?h(Component):null)}),h(Notice,{runtime,module:'meetings',nameKey:'settings.storageModuleMeetings'})])}).use(router).use(createI18n({legacy:false,locale:'zh-CN',messages:{'zh-CN':zh}})).mount('#app');
document.querySelector('#meetings').onclick=()=>router.push('/meetings');document.querySelector('#other').onclick=()=>router.push('/other');
document.querySelector('#account').onclick=()=>account(auth.userId==='A'?'B':'A',auth.workspaceId);document.querySelector('#workspace').onclick=()=>account(auth.userId,auth.workspaceId==='one'?'two':'one');
document.querySelector('#remote').onclick=()=>remote(false);document.querySelector('#delete').onclick=()=>remote(true);document.querySelector('#resolve').onclick=()=>{status.failed=false;status.blocked=0};
window.meetingViews={auth,status};
`
export async function createMeetingViewsFixture() {
  const root = fileURLToPath(new URL('../../../../../', import.meta.url))
  const server = await createServer({
    root,
    configFile: false,
    resolve: { alias: { '@': root + '/src' } },
    optimizeDeps: { noDiscovery: true },
    plugins: [
      vue(),
      {
        name: 'meeting-sync-views-fixture',
        enforce: 'pre',
        resolveId(source, importer) {
          if (source === 'virtual:meeting-views-shared') return '\0meeting-views-shared'
          if (source === 'virtual:meeting-views-entry') return '\0meeting-views-entry'
          if (/\/stores\/auth(?:\.ts)?$/.test(source)) return '\0meeting-views-auth'
          if (source.endsWith('HeaderActionsPortal.vue') || source.endsWith('LocalOnlyBadge.vue'))
            return '\0meeting-views-child'
          if (source.endsWith('/components') && importer?.endsWith('MeetingListView.vue'))
            return '\0meeting-views-components'
          if (source.endsWith('/composables/useToast')) return '\0meeting-views-toast'
          if (importer?.endsWith('MeetingListView.vue')) {
            if (source === './meetings-store') return '\0meeting-views-store'
            if (source === './recording-operation.ts') return '\0meeting-views-operation'
            if (source === '../../native/meeting-audio') return '\0meeting-views-audio'
          }
        },
        load(id) {
          if (id === '\0meeting-views-shared') return shared
          if (id === '\0meeting-views-entry') return entry
          if (id === '\0meeting-views-auth')
            return `export{auth}from'virtual:meeting-views-shared';import{auth}from'virtual:meeting-views-shared';export const useAuthStore=()=>auth`
          if (id === '\0meeting-views-child')
            return `import{h}from'vue';export default{setup(_,{slots}){return()=>h('div',slots.default?.())}}`
          if (id === '\0meeting-views-components')
            return `import{h}from'vue';const C={props:['title'],setup(p,{slots}){return()=>h('div',[p.title,slots.default?.()])}};export const Skeleton=C,EmptyState=C,DbLockedState=C,PullToRefresh=C,SwipeableListItem=C`
          if (id === '\0meeting-views-toast')
            return 'export const useToast=()=>({error(){},success(){},warning(){},info(){}})'
          if (id === '\0meeting-views-store')
            return `export{listMeetings}from'virtual:meeting-views-shared';export const createMeeting=async()=>null,deleteMeeting=async()=>{},archiveMeeting=async()=>{},unarchiveMeeting=async()=>{},updateMeeting=async()=>{}`
          if (id === '\0meeting-views-operation')
            return `import{auth}from'virtual:meeting-views-shared';export const captureRecordingDb=()=>{const v=auth.activationVersion;return{assertCurrent(){if(v!==auth.activationVersion)throw Error('expired')}}}`
          if (id === '\0meeting-views-audio') return 'export const deleteMeetingAudio=async()=>{}'
        },
        configureServer(s) {
          s.middlewares.use((req, res, next) => {
            if (!req.url.startsWith('/__meeting_sync_views')) return next()
            res.setHeader('Content-Type', 'text/html; charset=utf-8')
            res.end(
              '<h1>会议同步视图验收</h1><button id="meetings">会议列表</button><button id="other">隐藏并缓存</button><button id="account">切换账号</button><button id="workspace">切换工作区</button><button id="remote">远端修改</button><button id="delete">远端删除</button><button id="resolve">解决同步错误</button><output id="retry-count">0</output><div id="app"></div><script type="module" src="/@id/__x00__meeting-views-entry"></script>',
            )
          })
        },
      },
    ],
    server: { host: '127.0.0.1', port: 0, hmr: false },
  })
  await server.listen()
  return { server, url: `http://127.0.0.1:${server.httpServer.address().port}/__meeting_sync_views` }
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const f = await createMeetingViewsFixture()
  process.stdout.write(f.url + '\n')
}
