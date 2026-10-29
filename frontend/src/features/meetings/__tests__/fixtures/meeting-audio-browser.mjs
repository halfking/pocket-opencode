import { createServer } from 'vite'
import { fileURLToPath } from 'node:url'

const entry = `
import {saveMeetingAudio,loadMeetingAudio,deleteMeetingAudio} from '/src/native/meeting-audio.ts';
let user='A',workspace='one',generation=0,hold=false,release=null;
const originalOpen=indexedDB.open.bind(indexedDB);
indexedDB.open=(...args)=>{const req=originalOpen(...args);return {get result(){return req.result},get error(){return req.error},set onupgradeneeded(fn){req.onupgradeneeded=fn},set onerror(fn){req.onerror=fn},set onsuccess(fn){req.onsuccess=e=>{if(hold){document.querySelector('#waiting').textContent='waiting';release=()=>fn(e)}else fn(e)}}}};
const owner=()=>({owner:user?{userId:user,workspaceId:workspace}:null,assertCurrent:(()=>{const version=generation;return()=>{if(version!==generation)throw Object.assign(new Error('owner expired'),{name:'AbortError'})}})()});
const report=value=>document.querySelector('#result').textContent=String(value);
const action=fn=>async()=>{try{await fn()}catch(error){report(error.name+':'+error.message)}};
document.querySelector('#save').onclick=action(async()=>{const op=owner(),body=user||'unknown';await saveMeetingAudio('same-id',new Blob([body]),op);report('saved '+body)});
document.querySelector('#read').onclick=action(async()=>{const url=await loadMeetingAudio('same-id',owner());try{report(url?await (await fetch(url)).text():'empty')}finally{if(url)URL.revokeObjectURL(url)}});
document.querySelector('#remove').onclick=action(async()=>{await deleteMeetingAudio('same-id',owner());report('deleted')});
document.querySelector('#account').onclick=()=>{user=user==='A'?'B':'A';generation++;document.querySelector('#owner').textContent=user+':'+workspace;report('switched')};
document.querySelector('#workspace').onclick=()=>{workspace=workspace==='one'?'two':'one';generation++;document.querySelector('#owner').textContent=user+':'+workspace;report('switched')};
document.querySelector('#pause').onclick=()=>{hold=true;report('pause armed')};
document.querySelector('#release').onclick=()=>{hold=false;release?.();release=null;document.querySelector('#waiting').textContent='released'};
document.querySelector('#legacy').onclick=action(async()=>{const req=originalOpen('pocket-meeting-audio');req.onupgradeneeded=()=>req.result.createObjectStore('recordings');await new Promise((resolve,reject)=>{req.onsuccess=()=>resolve();req.onerror=()=>reject(req.error)});const db=req.result;try{await new Promise((resolve,reject)=>{const tx=db.transaction('recordings','readwrite');tx.objectStore('recordings').put(new Blob(['legacy private']),'same-id');tx.oncomplete=resolve;tx.onerror=()=>reject(tx.error)})}finally{db.close()}report('legacy retained')});
window.audioFixture={owner,get user(){return user},get workspace(){return workspace},async legacyExists(){const req=originalOpen('pocket-meeting-audio');await new Promise(r=>req.onsuccess=r);const db=req.result;try{return await new Promise(r=>{const get=db.transaction('recordings').objectStore('recordings').get('same-id');get.onsuccess=()=>r(Boolean(get.result))})}finally{db.close()}}};
document.querySelector('#ready').textContent='ready';
`
export async function createMeetingAudioFixture() {
  const root = fileURLToPath(new URL('../../../../../', import.meta.url))
  const server = await createServer({
    root,
    configFile: false,
    optimizeDeps: { noDiscovery: true },
    plugins: [
      {
        name: 'meeting-audio-owner-fixture',
        resolveId(id) {
          if (id === 'virtual:meeting-audio') return '\0meeting-audio-entry'
        },
        load(id) {
          if (id === '\0meeting-audio-entry') return entry
        },
        configureServer(server) {
          server.middlewares.use((req, res, next) => {
            if (!req.url.startsWith('/__meeting_audio_owner')) return next()
            res.setHeader('Content-Type', 'text/html; charset=utf-8')
            res.end(
              '<h1>会议音频归属</h1><span id="ready"></span><output id="owner">A:one</output><button id="save">保存同 ID 音频</button><button id="read">读取音频</button><button id="remove">删除音频</button><button id="account">切换账号</button><button id="workspace">切换工作区</button><button id="pause">暂停打开数据库</button><button id="release">继续</button><button id="legacy">放入旧版音频</button><output id="waiting"></output><pre id="result"></pre><script type="module" src="/@id/__x00__meeting-audio-entry"></script>',
            )
          })
        },
      },
    ],
    server: { host: '127.0.0.1', port: 0, hmr: false },
  })
  await server.listen()
  return { server, url: `http://127.0.0.1:${server.httpServer.address().port}/__meeting_audio_owner` }
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const f = await createMeetingAudioFixture()
  process.stdout.write(f.url + '\n')
}
