// diag-auth-token-source.mjs
// App 自动登录（dev-bypass）到底调了哪个端点拿 token？抓全量网络。
// 做法：清 localStorage -> reload -> 记录所有 /api/ 请求的 URL + 响应体，
//       找出返回 {token, auth_method} 的那次调用。
import { execFileSync } from 'node:child_process';

const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9266';
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});

const pid=adb(['-s',SERIAL,'shell',`pidof ${PKG}`]).trim().split(/\s+/)[0];
if(!pid){console.log('APP_NOT_RUNNING');process.exit(2);}
const socks=adb(['-s',SERIAL,'shell',`cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map(l=>l.trim().replace('@','')).filter(Boolean);
adb(['-s',SERIAL,'forward',`tcp:${PORT}`,`localabstract:${socks.find(s=>s.endsWith(`_${pid}`))||socks[socks.length-1]}`]);
const page=(await(await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find(t=>t.type==='page');
const ws=new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//,`:${PORT}/`));
let id=0;const pending=new Map();const reqs=[];const bodies=new Map();
const send=(m,p={})=>new Promise(r=>{const i=++id;pending.set(i,r);ws.send(JSON.stringify({id:i,method:m,params:p}))});
ws.addEventListener('message',e=>{
  const m=JSON.parse(e.data);
  if(m.id&&pending.has(m.id)){pending.get(m.id)(m.result);pending.delete(m.id);return;}
  const p=m.params||{};
  if(m.method==='Network.requestWillBeSent'){
    reqs.push({url:(p.request?.url||'').replace(/https?:\/\/[^/]+/,''),method:p.request?.method,reqId:p.requestId,status:null,body:null});
  }
  if(m.method==='Network.responseReceived'){
    const hit=reqs.find(r=>r.reqId===p.requestId);
    if(hit) hit.status=p.response?.status;
  }
  if(m.method==='Network.loadingFinished'){
    send('Network.getResponseBody',{requestId:p.requestId}).then(r=>{ if(r&&r.result&&typeof r.result.body==='string') bodies.set(p.requestId,r.result.body); });
  }
});
await new Promise(r=>ws.addEventListener('open',r));
await send('Runtime.enable');
await send('Network.enable');
const ev=async(x)=>(await send('Runtime.evaluate',{expression:x,returnByValue:true,awaitPromise:true}))?.result?.value;

console.log('清 localStorage 并 reload…');
await ev(`localStorage.clear(); return 1`);
await ev('location.reload()');
let ready=null;const dl=Date.now()+30000;
while(Date.now()<dl){await sleep(1000);ready=await ev('location.origin');if(ready&&ready!=='null')break;}
await sleep(9000); // 给自动登录足够时间

const scope=await ev(`(function(){var t=localStorage.getItem('pocket_token')||'';var c={};try{c=JSON.parse(atob(t.split('.')[1].replace(/-/g,'+').replace(/_/g,'/')))}catch(e){};return {hasToken:!!t,claimWs:c.workspace_id,authMethod:localStorage.getItem('pocket_auth_method'),localWs:localStorage.getItem('pocket_workspace_id')}})()`);
console.log('最终 App 作用域 =', JSON.stringify(scope));
console.log('hash =', await ev('location.hash'));
console.log('\n--- 所有 /api/ 请求 ---');
for(const r of reqs.filter(r=>r.url.includes('/api/'))){
  let b=bodies.get(r.reqId);
  let short='';
  if(b){ try{const j=JSON.parse(b); if(j&&(j.token||j.auth_method)) short=' <== 含 token! auth_method='+j.auth_method+' workspace_id='+j.workspace_id; }catch{} }
  console.log(`  ${r.method} ${r.url} status=${r.status}${short}`);
}
process.exit(0);
