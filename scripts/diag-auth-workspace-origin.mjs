// diag-auth-workspace-origin.mjs
// 判别实验：App 的 workspace_id="default" 到底从哪来？
// 做法：清空 localStorage -> reload -> 让 App 走标准 admin 密码登录 ->
//       抓 /api/auth/login 的响应体 + 登录后 token 的 workspace claim。
// 判读：
//   - 登录响应 workspace_id == "ws_user-admin" 且 token claim 也是它
//       => 之前的 "default" 是历史/异常态，App 正常登录不会复现
//   - 登录响应 workspace_id == "default"
//       => 后端某条路径给 admin 铸了 default 工作区（后端 bug）
import { execFileSync } from 'node:child_process';
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9265';
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});
const devPass = requireDevPass()

const pid=adb(['-s',SERIAL,'shell',`pidof ${PKG}`]).trim().split(/\s+/)[0];
if(!pid){console.log('APP_NOT_RUNNING');process.exit(2);}
const socks=adb(['-s',SERIAL,'shell',`cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map(l=>l.trim().replace('@','')).filter(Boolean);
adb(['-s',SERIAL,'forward',`tcp:${PORT}`,`localabstract:${socks.find(s=>s.endsWith(`_${pid}`))||socks[socks.length-1]}`]);
const page=(await(await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find(t=>t.type==='page');
const ws=new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//,`:${PORT}/`));
let id=0;const pending=new Map();const loginReqs=[];
const send=(m,p={})=>new Promise(r=>{const i=++id;pending.set(i,r);ws.send(JSON.stringify({id:i,method:m,params:p}))});
ws.addEventListener('message',e=>{
  const m=JSON.parse(e.data);
  if(m.id&&pending.has(m.id)){pending.get(m.id)(m.result);pending.delete(m.id);return;}
  const p=m.params||{};
  if(m.method==='Network.requestWillBeSent'&&/\/api\/auth\/(login|code|register)/.test(p.request?.url||'')){
    loginReqs.push({url:p.request.url.replace(/https?:\/\/[^/]+/,''),method:p.request.method,postData:p.request.postData,status:null,body:null,reqId:p.requestId});
  }
  if(m.method==='Network.responseReceived'&&/\/api\/auth\/(login|code|register)/.test(p.response?.url||'')){
    const hit=loginReqs.find(r=>r.status===null&&r.url===p.response.url.replace(/https?:\/\/[^/]+/,''));
    if(hit){hit.status=p.response.status;hit._netId=p.requestId;}
  }
  if(m.method==='Network.loadingFinished'&&p.requestId){
    const hit=loginReqs.find(r=>r._netId===p.requestId);
    if(hit&&!hit.body) send('Network.getResponseBody',{requestId:p.requestId}).then(r=>{if(r&&r.result&&typeof r.result.body==='string'){try{hit.body=JSON.parse(r.result.body)}catch{hit.body=r.result.body.slice(0,200)}}});
  }
});
await new Promise(r=>ws.addEventListener('open',r));
await send('Runtime.enable');
await send('Network.enable');
const ev=async(x)=>(await send('Runtime.evaluate',{expression:x,returnByValue:true,awaitPromise:true}))?.result?.value;

console.log('origin =', await ev('location.origin'));
// 1. 清空 localStorage
await ev(`localStorage.clear(); return 1`);
console.log('localStorage 已清空');
// 2. reload
await ev('location.reload()');
let ready=null; const dl=Date.now()+30000;
while(Date.now()<dl){ await sleep(1000); ready=await ev('location.origin'); if(ready&&ready!=='null') break; }
await sleep(2500);
await ev(`location.hash='#/login'`); await sleep(2600);
console.log('当前 hash =', await ev('location.hash'));
console.log('有主密码输入框 =', await ev(`!!document.querySelector('input[placeholder*="主密码"]')`));
console.log('有用户名输入框 =', await ev(`!!document.querySelector('input[placeholder*="用户名"]')`));

// 3. 标准 admin 密码登录
const fillBy=(sel,val)=>`(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`;
console.log('填用户名 =', await ev(fillBy('input[placeholder*="用户名"]','admin')));
console.log('填密码 =', await ev(fillBy('input[type="password"]',devPass)));
await sleep(900);
await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});if(b)b.click();return b?1:0})()`);
console.log('已点登录，等待…');
await sleep(7000);

// 4. 结果
const scope=await ev(`(function(){var t=localStorage.getItem('pocket_token')||'';var c={};try{c=JSON.parse(atob(t.split('.')[1].replace(/-/g,'+').replace(/_/g,'/')))}catch(e){};return {hasToken:!!t,claimWs:c.workspace_id,claimSub:c.sub,claimUserId:c.user_id,localWs:localStorage.getItem('pocket_workspace_id'),localUser:localStorage.getItem('pocket_user'),authMethod:localStorage.getItem('pocket_auth_method')}})()`);
console.log('登录后 App 作用域 =', JSON.stringify(scope));
console.log('hash 现在 =', await ev('location.hash'));
console.log('--- 抓到的 /api/auth/* 响应 ---');
for(const r of loginReqs) console.log(`  ${r.method} ${r.url} status=${r.status} resp=${JSON.stringify(r.body)}`);
process.exit(0);
