// 运行时把 App 指向本地后端（pocket_api_base 覆盖是 resolveRuntimeApiBase 的第一优先级），
// 并可选直接跳到某页面。构建时 VITE_API_BASE 没注入时靠这个兜底。
import { execFileSync } from 'node:child_process';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9289';
const BASE=process.env.POCKET_API_BASE||'http://127.0.0.1:8088';
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});
const pid=adb(['-s',SERIAL,'shell',`pidof ${PKG}`]).trim().split(/\s+/)[0];
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
await ev(`localStorage.setItem('pocket_api_base', ${JSON.stringify(BASE)}); return 1`);
console.log('pocket_api_base 已设为', BASE, '-> 读取回读:', await ev(`localStorage.getItem('pocket_api_base')`));
await ev('location.reload()');
let ready=null; const dl=Date.now()+30000;
while(Date.now()<dl){ await sleep(1000); ready=await ev('location.origin'); if(ready&&ready!=='null') break; }
await sleep(4000);
console.log('reload 后 hash=', await ev('location.hash'));
process.exit(0);
