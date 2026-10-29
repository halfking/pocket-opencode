<script setup lang="ts">
import { onActivated, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '../../stores/auth'
import { useConfirm } from '../../composables/useConfirm'
import { toUserMessage } from '../../api/error-message'
import { captureRssPortalOperation, createRssPortalRecord, deleteRssPortalRecord, listRssPortalRecords, saveRssPortalRecord, type RssPortalEntry } from './rss-sync-store'
import { rssSyncRuntime } from './rss-sync-runtime'

const {t}=useI18n(), auth=useAuthStore(), {confirm}=useConfirm()
const records=ref<RssPortalEntry[]>([]), query=ref(''), error=ref(''), notice=ref(''), loading=ref(false), saving=ref(false)
const page=ref(0),hasMore=ref(false)
const editing=ref<RssPortalEntry|null>(null), formOpen=ref(false), kind=ref<'source'|'item'>('source')
const title=ref(''),url=ref(''),category=ref(''),source=ref(''),summary=ref('')
let generation=0,readVersion=0
const active=(stamp:number)=>generation===stamp&&auth.activationStatus==='ready'
function clear(){generation++;readVersion++;page.value=0;hasMore.value=false;records.value=[];editing.value=null;formOpen.value=false;query.value=title.value=url.value=category.value=source.value=summary.value='';error.value=notice.value='';loading.value=saving.value=false}
async function refresh(){
  const stamp=generation,read=++readVersion
  if(auth.activationStatus!=='ready')return
  loading.value=true
  try{const operation=captureRssPortalOperation(),rows=await listRssPortalRecords({query:query.value,offset:page.value*50,limit:51},operation);operation.assertCurrent();if(active(stamp)&&read===readVersion){records.value=rows.slice(0,50);hasMore.value=rows.length>50}}
  catch(cause){if(active(stamp)&&read===readVersion&&!(cause instanceof Error&&cause.name==='AbortError'))error.value=toUserMessage(cause,t,t('errors.loadRssFailed'))}
  finally{if(active(stamp)&&read===readVersion)loading.value=false}
}
function searchRecords(){page.value=0;void refresh()}
function movePage(delta:number){page.value=Math.max(0,page.value+delta);void refresh()}
function open(entry:RssPortalEntry|null=null){
  editing.value=entry;kind.value=entry?.item.kind==='item'?'item':'source';title.value=entry?.item.title??''
  url.value=String(entry?.item.payload.url??'');category.value=String(entry?.item.payload.category??'')
  source.value=String(entry?.item.payload.source??'');summary.value=String(entry?.item.payload.summary??'');formOpen.value=true;error.value=notice.value=''
}
async function save(){
  const stamp=generation, entry=editing.value
  saving.value=true;error.value=notice.value=''
  try{
    const operation=captureRssPortalOperation(),input={kind:kind.value,title:title.value,payload:kind.value==='source'?{url:url.value,category:category.value}:{source:source.value,summary:summary.value,url:url.value}}
    if(entry)await saveRssPortalRecord(entry,input,operation);else await createRssPortalRecord(input,operation)
    operation.assertCurrent();if(!active(stamp))return
    formOpen.value=false;editing.value=null;notice.value=t('rssPortal.localSaved');await refresh();void rssSyncRuntime.syncNow()
  }catch(cause){if(active(stamp)&&!(cause instanceof Error&&cause.name==='AbortError'))error.value=toUserMessage(cause,t,t('errors.operateFailed'))}
  finally{if(active(stamp))saving.value=false}
}
async function remove(entry:RssPortalEntry){
  const stamp=generation
  try{
    const operation=captureRssPortalOperation()
    if(!await confirm({title:t('common.delete'),message:t('rssPortal.confirmDelete'),confirmText:t('common.delete'),danger:true}))return
    operation.assertCurrent();await deleteRssPortalRecord(entry,operation);operation.assertCurrent()
    if(active(stamp)){await refresh();void rssSyncRuntime.syncNow()}
  }catch(cause){if(active(stamp)&&!(cause instanceof Error&&cause.name==='AbortError'))error.value=toUserMessage(cause,t,t('errors.operateFailed'))}
}
watch(()=>[auth.activationVersion,auth.userId,auth.workspaceId,auth.activationStatus],()=>{clear();void refresh()},{flush:'sync'})
const changed=()=>{void refresh()}
onMounted(()=>{void refresh();window.addEventListener('openpocket:portal-rss-applied',changed)})
onActivated(changed)
onBeforeUnmount(()=>{generation++;window.removeEventListener('openpocket:portal-rss-applied',changed)})
</script>

<template>
  <section class="rss-portal-panel" aria-labelledby="rss-portal-heading">
    <div class="portal-toolbar"><h3 id="rss-portal-heading">{{t('rssPortal.title')}}</h3><button type="button" :disabled="saving" @click="open()">{{t('rssPortal.new')}}</button></div>
    <p class="description">{{t('rssPortal.description')}}</p>
    <p v-if="rssSyncRuntime.status.failed" role="alert">{{t('rssPortal.failed')}} <button type="button" :disabled="rssSyncRuntime.status.running" @click="rssSyncRuntime.syncNow()">{{t('common.retry')}}</button></p>
    <p v-if="rssSyncRuntime.status.epochUnknown">{{t('rssPortal.unknown')}}</p>
    <p v-if="rssSyncRuntime.status.resetBlocked||rssSyncRuntime.status.resetPending">{{t('rssPortal.blocked')}}</p>
    <p v-if="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{notice}}</p>
    <form v-if="formOpen" class="portal-form" @submit.prevent="save">
      <label>{{t('rssPortal.source')}} / {{t('rssPortal.item')}}<select id="rss-portal-kind" v-model="kind" :disabled="Boolean(editing)"><option value="source">{{t('rssPortal.source')}}</option><option value="item">{{t('rssPortal.item')}}</option></select></label>
      <label>{{t('rssPortal.titleLabel')}}<input id="rss-portal-title" v-model="title" required /></label>
      <label>{{t('rssPortal.url')}}<input id="rss-portal-url" v-model="url" type="url" :required="kind==='source'" /></label>
      <label v-if="kind==='source'">{{t('rssPortal.category')}}<input id="rss-portal-category" v-model="category" /></label>
      <template v-else><label>{{t('rssPortal.sourceLabel')}}<input id="rss-portal-source" v-model="source" /></label><label>{{t('rssPortal.summary')}}<textarea id="rss-portal-summary" v-model="summary" rows="4" /></label></template>
      <div class="portal-actions"><button type="submit" :disabled="saving">{{t('rssPortal.save')}}</button><button type="button" :disabled="saving" @click="formOpen=false">{{t('common.cancel')}}</button></div>
    </form>
    <form class="portal-search" @submit.prevent="searchRecords"><label>{{t('rssPortal.search')}}<input id="rss-portal-search" v-model="query" type="search" /></label><button type="submit">{{t('common.search')}}</button></form>
    <p v-if="loading">{{t('rssPortal.loading')}}</p><p v-else-if="!records.length">{{t('rssPortal.empty')}}</p>
    <ul v-else class="portal-records"><li v-for="entry in records" :key="entry.id"><div><strong>{{entry.item.title}}</strong><small>{{t(entry.item.kind==='source'?'rssPortal.source':'rssPortal.item')}}</small><p>{{entry.item.payload.url}}</p><p v-if="entry.item.kind==='item'" class="summary">{{entry.item.payload.summary}}</p></div><div class="portal-actions"><button type="button" :disabled="saving" @click="open(entry)">{{t('common.edit')}}</button><button type="button" :disabled="saving" @click="remove(entry)">{{t('common.delete')}}</button></div></li></ul>
    <nav class="portal-actions" :aria-label="t('rssPortal.title')"><button type="button" :disabled="page===0||loading||saving" @click="movePage(-1)">{{t('common.previous')}}</button><button type="button" :disabled="!hasMore||loading||saving" @click="movePage(1)">{{t('common.next')}}</button></nav>
  </section>
</template>

<style scoped>
.rss-portal-panel{border:1px solid var(--border);border-radius:8px;padding:16px;margin-bottom:20px}.portal-toolbar,.portal-actions,.portal-search{display:flex;align-items:center;gap:8px}.portal-toolbar h3{flex:1;margin:0}.description,small{color:var(--text-secondary)}.portal-form{display:grid;gap:10px;margin:12px 0}.portal-form label,.portal-search label{display:grid;gap:4px}.portal-form input,.portal-form textarea,.portal-form select,.portal-search input{border:1px solid var(--border);border-radius:4px;padding:8px;background:var(--bg-elevated);color:inherit}.portal-search{margin-top:12px}.portal-search label{flex:1}button{border:1px solid var(--border);border-radius:4px;padding:7px 10px;background:var(--bg-elevated);color:inherit;cursor:pointer}button:disabled{opacity:.6;cursor:default}.portal-records{list-style:none;padding:0}.portal-records li{display:flex;gap:12px;justify-content:space-between;padding:12px 0;border-top:1px solid var(--border)}.portal-records small{display:block}.portal-records p{margin:4px 0;overflow-wrap:anywhere}.summary{white-space:pre-wrap}.portal-records .portal-actions{align-items:flex-start;flex-shrink:0}@media(max-width:520px){.portal-records li{flex-direction:column}}
</style>
