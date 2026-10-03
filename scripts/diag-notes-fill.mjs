// 判定笔记创建是「产品 bug」还是「CDP 填表没触发 UnifiedComposer v-model」：
// 填表后检查创建按钮是否由 disabled 变 enabled。
import { execFileSync } from 'node:child_process';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9302';
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
const MASTER='PocketTest2026';
async function ensureUnlocked(){
  if(await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)){
    await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
    await sleep(700);
    await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`);
    await sleep(5000);
  }
}
await ensureUnlocked();
await ev(`location.hash='#/notes'`); let d=Date.now()+15000;while(Date.now()<d&&(await ev('location.hash'))!=='#/notes')await sleep(300);
await sleep(2000); await ensureUnlocked();
await ev(`location.hash='#/notes'`); await sleep(1500);
await ev(`(function(){var e=document.querySelector('.notes-action[aria-label="新建笔记"]');if(e)e.click();return 1})()`);
await sleep(2500);
console.log('新建页 hash=', await ev('location.hash'));
// 创建按钮初始状态
const before=await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>/创建|保存/.test(x.textContent||''));return b?('text='+(b.textContent||'').trim()+' disabled='+b.disabled):'NO_BTN'})()`);
console.log('填表前 创建/保存 按钮 =', before);
// 填标题（原生 setter + input + change + blur）
await ev(`(function(){var e=document.querySelector('textarea[placeholder*="一句话概括"]');if(!e)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,'ZZTestTitle123');e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));e.dispatchEvent(new Event('blur',{bubbles:true}));return 'ok'})()`);
await sleep(1200);
const after=await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>/创建|保存/.test(x.textContent||''));return b?('text='+(b.textContent||'').trim()+' disabled='+b.disabled):'NO_BTN'})()`);
console.log('填表后 创建/保存 按钮 =', after);
const verdict = after.includes('disabled=true') ? '按钮仍 disabled => v-model 未被 CDP 填表触发（测试夹具问题，非产品 bug）' : '按钮已 enabled => v-model 生效，之前的创建失败另查';
console.log('判定:', verdict);
process.exit(0);
