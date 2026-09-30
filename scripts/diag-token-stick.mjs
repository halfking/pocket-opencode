// diag-token-stick.mjs —— 注入正确作用域 token 后 reload，看 App 保留还是回弹。
// 保留 ws_user-admin => 之前读路径失败另有原因（缓存/时序）
// 回弹 default      => App 启动逻辑强制重新认证到 default（陈旧 bundle / dev 自动登录）
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import http from 'node:http';

const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9270';
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});
function api(path,{token,method='GET',body}={}){return new Promise((res)=>{const payload=body?JSON.stringify(body):'';const h={};if(token)h.Authorization='Bearer '+token;if(payload){h['Content-Type']='application/json';h['Content-Length']=Buffer.byteLength(payload);}const req=http.request({host:'127.0.0.1',port:8088,path,method,headers:h,timeout:15000},(r)=>{let b='';r.on('data',c=>b+=c);r.on('end',()=>res({status:r.statusCode,body:b}));});req.on('error',e=>res({status:0,body:String(e)}));req.on('timeout',()=>{req.destroy();res({status:0,body:'timeout'})});if(payload)req.write(payload);req.end();});}
const devPass=(readFileSync('backend/internal/server/server_assistant.go','utf8').match(/devPass\s*=\s*"([^"]+)"/)||[])[1]||'';
const claim=(t)=>{try{return JSON.parse(Buffer.from(t.split('.')[1],'base64').toString('utf8')).workspace_id}catch{return '??'}};

const pid=adb(['-s',SERIAL,'shell',`pidof ${PKG}`]).trim().split(/\s+/)[0];
if(!pid){console.log('APP_NOT_RUNNING');process.exit(2);}
const socks=adb(['-s',SERIAL,'shell',`cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map(l=>l.trim().replace('@','')).filter(Boolean);
adb(['-s',SERIAL,'forward',`tcp:${PORT}`,`localabstract:${socks.find(s=>s.endsWith(`_${pid}`))||socks[socks.length-1]}`]);
const page=(await(await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find(t=>t.type==='page');
const ws=new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//,`:${PORT}/`));
let id=0;const pending=new Map();
const send=(m,p={})=>new Promise(r=>{const i=++id;pending.set(i,r);ws.send(JSON.stringify({id:i,method:m,params:p}))});
ws.addEventListener('message',e=>{const m=JSON.parse(e.data);if(m.id&&pending.has(m.id)){pending.get(m.id)(m.result);pending.delete(m.id)}});
await new Promise(r=>ws.addEventListener('open',r));
await send('Runtime.enable');
const ev=async(x)=>(await send('Runtime.evaluate',{expression:x,returnByValue:true,awaitPromise:true}))?.result?.value;

const login=await api('/api/auth/login',{method:'POST',body:{username:'admin',password:devPass}});
const {token,workspace_id,user}=JSON.parse(login.body);
console.log('注入的 token workspace =', workspace_id, '(claim:', claim(token),')');
await ev(`localStorage.clear(); localStorage.setItem('pocket_token',${JSON.stringify(token)}); localStorage.setItem('pocket_user',${JSON.stringify(user)}); localStorage.setItem('pocket_workspace_id',${JSON.stringify(workspace_id)}); return 1`);
const before=await ev(`localStorage.getItem('pocket_token')`);
console.log('reload 前 localStorage claim =', claim(before));
await ev('location.reload()');
let ready=null;const dl=Date.now()+30000;
while(Date.now()<dl){await sleep(1000);ready=await ev('location.origin');if(ready&&ready!=='null')break;}
await sleep(4000);
const after=await ev(`localStorage.getItem('pocket_token')||''`);
console.log('reload 后 localStorage claim =', claim(after), ' localWs=', await ev(`localStorage.getItem('pocket_workspace_id')`));
console.log(claim(after)===workspace_id ? '✅ 保留了正确作用域（ws_user-admin）——之前读路径失败另有原因' : '❌ 回弹到 default —— App 启动强制重认证(陈旧 bundle/dev 自动登录)');
process.exit(0);
