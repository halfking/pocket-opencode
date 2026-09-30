// 解锁全局 crypto（主密码），供密码箱写路径测试用。
// crypto key 只在内存，刷新/重启后必须用主密码重新派生。
import { execFileSync } from 'node:child_process';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9293';
const MASTER=process.env.POCKET_MASTER||'PocketTest2026';
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
console.log('hash=', await ev('location.hash'));
// 找主密码输入框（可能是 主密码 / 或留空点认证）
const hasMaster=await ev(`!!document.querySelector('input[placeholder*="主密码"]')`);
console.log('主密码输入框存在 =', hasMaster);
if(hasMaster){
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
  await sleep(800);
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b){b.click();return 1}return 0})()`);
  await sleep(5000);
  console.log('解锁后 hash=', await ev('location.hash'));
} else {
  console.log('当前无主密码框，页面文本=', (await ev('document.body.innerText.slice(0,200)'))?.replace(/\s+/g,' '));
}
process.exit(0);
