// verify-notes-inputtext.mjs —— 用 CDP 真键盘事件（Input.insertText）驱动 UnifiedComposer 的 v-model。
// §4.43 备选路径：setter 注入触发不了 v-model，Input.insertText 走浏览器输入管线，应能触发。
// 判据：填表后保存按钮由 disabled -> enabled -> 保存 -> 列表回显。
import { execFileSync } from 'node:child_process';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9310';
const MASTER=process.env.POCKET_MASTER||'PocketTest2026';
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});
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
const checks=[];const check=(n,pass,d)=>{checks.push({n,pass});console.log(`${pass?'PASS':'FAIL'}  ${n}${d?'  — '+d:''}`)};
async function ensureUnlocked(){
  if(await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)){
    await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
    await sleep(700);
    await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`);
    await sleep(5000);
  }
}
// 真键盘输入：聚焦元素后用 Input.insertText
async function typeInto(sel,text){
  const box=await ev(`(function(){var e=document.querySelector(${JSON.stringify(sel)});if(!e)return null;var r=e.getBoundingClientRect();return JSON.stringify({x:r.left+r.width/2,y:r.top+r.height/2})})()`);
  if(!box) return 'NF';
  const {x,y}=JSON.parse(box);
  await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});
  await send('Input.dispatchMouseEvent',{type:'mouseReleased',x,y,button:'left',clickCount:1});
  await sleep(300);
  await send('Input.insertText',{text});
  await sleep(500);
  return 'typed';
}
const btnState=()=>ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>/创建|保存/.test(x.textContent||''));return b?((b.textContent||'').trim()+'|disabled='+b.disabled):'NO_BTN'})()`);

await ensureUnlocked();
await ev(`location.hash='#/notes'`); let d=Date.now()+15000;while(Date.now()<d&&(await ev('location.hash'))!=='#/notes')await sleep(300);
await sleep(2000); await ensureUnlocked();
await ev(`location.hash='#/notes'`); await sleep(1500);
await ev(`(function(){var e=document.querySelector('.notes-action[aria-label="新建笔记"]');if(e)e.click();return 1})()`);
await sleep(2500);
const TITLE='NI-'+String(Date.now()).slice(-6);
console.log('note title =', TITLE);
console.log('保存按钮(填表前) =', await btnState());
await typeInto('textarea[placeholder*="一句话概括"]', TITLE);
await typeInto('textarea[placeholder*="全屏编辑"]', 'body-'+TITLE);
const after=await btnState();
console.log('保存按钮(填表后) =', after);
const enabled=after.includes('disabled=false');
check('Input.insertText 触发 v-model（保存按钮 enable）', enabled, after);
if(enabled){
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>/创建|保存/.test(x.textContent||''));if(b)b.click();return 1})()`);
  await sleep(1500);
  await ev(`location.hash='#/notes'`); await sleep(3000);
  const found=await ev(`(function(){var cs=document.querySelectorAll('.note-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0)return true}return false})()`);
  const cards=await ev(`document.querySelectorAll('.note-card').length`);
  check('保存后笔记在列表回显（写路径通）', !!found, `cards=${cards} found=${!!found}`);
}
const passed=checks.filter(c=>c.pass).length;
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`);
// BUG-V19：原本写死 process.exit(0)，哪怕判出 FAIL 退出码也是 0，
// 调用方（CI / 批量 runner）无从分辨「跑过了」与「全绿」。
process.exitCode = checks.some(c=>!c.pass) ? 1 : 0;
