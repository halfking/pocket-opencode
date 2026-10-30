import { createRecordRuntime, recordRuntimes } from '../../native/portal-sync/record-runtime.ts'
import { rssSyncAdapter } from './rss-sync-adapter'
import { reactive,ref,watch,watchEffect } from 'vue'
import { lobsterReady } from '../../native/lobster-init'
import { useAuthStore } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'
import { captureNativeRssOperation,ingestNativeRssChanges,nativeRssBlockedCount } from './rss-native-ingest'
import { reconcileRssItemStates } from './rss-item-state'

const portable=createRecordRuntime(rssSyncAdapter),nativeFailed=ref(false),nativeBlocked=ref(0),nativeRunning=ref(false)
const status=reactive({...portable.status})
watchEffect(()=>{Object.assign(status,portable.status,{failed:portable.status.failed||nativeFailed.value,blocked:portable.status.blocked+nativeBlocked.value,running:portable.status.running||nativeRunning.value})})
let started=false,flight:Promise<number>|null=null,generation=0
async function syncNow():Promise<number>{
  if(flight)return flight
  const auth=useAuthStore()
  if(!lobsterReady.value||auth.activationStatus!=='ready'||readCachedModuleMode('rss')!=='cloud')return portable.syncNow()
  let operation:ReturnType<typeof captureNativeRssOperation>
  try{operation=captureNativeRssOperation()}catch(error){return Promise.resolve(0)}
  const stamp=generation
  nativeRunning.value=true
  flight=(async()=>{try{
    // Learn the verified module epoch before consuming events. Their recorded
    // epoch remains immutable and is never replaced by this snapshot.
    await portable.syncNow()
    operation.assertCurrent();await ingestNativeRssChanges(operation)
    operation.assertCurrent();await reconcileRssItemStates(operation)
    operation.assertCurrent();await ingestNativeRssChanges(operation)
    const db=operation.db
    const blocked=await nativeRssBlockedCount(db);db.assertCurrent()
    if(stamp===generation){nativeBlocked.value=blocked;nativeFailed.value=false}
    return await portable.syncNow()
  }catch(error){if(stamp===generation&&!(error instanceof Error&&error.name==='AbortError')){nativeFailed.value=true;console.warn('[rss-sync] Native source changes retained for retry:',error)}return 0}
  finally{if(stamp===generation)nativeRunning.value=false;flight=null}})()
  return flight
}
function start(){
  if(started)return;started=true;portable.start()
  const auth=useAuthStore(),kick=()=>{void syncNow()}
  watch(()=>[auth.activationVersion,auth.userId,auth.workspaceId,auth.activationStatus],()=>{generation++;nativeFailed.value=false;nativeBlocked.value=0;nativeRunning.value=false;kick()},{flush:'sync'})
  watch(()=>lobsterReady.value,kick,{immediate:true})
  window.addEventListener('online',kick);window.addEventListener('openpocket:rss-native-changed',kick);window.addEventListener('openpocket:sync-policy-changed',kick);window.setInterval(kick,30000)
}
export const rssSyncRuntime={status,start,syncNow}
export function startRssPortalSync(): void { recordRuntimes.set('rss',rssSyncRuntime);rssSyncRuntime.start() }
