// verify-notes-writepath.mjs —— 真机笔记写路径（创建 -> 列表回显），稳健版。
// 相比 redmi-write-ops.mjs：处理主密码解锁（crypto 只在内存，重启/刷新会丢），
// 并用轮询等待列表回显，避免时序误判。
import { execFileSync } from 'node:child_process';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9300';
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
const fill=(sel,val)=>`(function(){var e=document.querySelector(${JSON.stringify(sel)});if(!e)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(val)});e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`;
const clickText=(t,exact)=>`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return ${exact===false?`(x.textContent||'').indexOf(${JSON.stringify(t)})>=0`:`(x.textContent||'').trim()===${JSON.stringify(t)}`}});if(!b)return 'NO_BTN';if(b.disabled)return 'DISABLED';b.click();return 'clicked'})()`;
async function waitFor(expr,timeout=18000){const t0=Date.now();while(Date.now()-t0<timeout){const v=await ev(expr);if(v)return v;await sleep(600)}return null}

// 确保已解锁（若被 route guard 拦到 unlock=1，用主密码解锁全局 crypto）
async function ensureUnlocked(){
  if((await ev('location.hash')).includes('unlock=1')||(await ev(`!!document.querySelector('input[placeholder*="主密码"]')`))){
    await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');if(!e)return 0;var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
    await sleep(700);
    await ev(clickText('解锁',true));
    await sleep(5000);
  }
  return await ev(`location.hash`);
}

const TITLE='Notes回归-'+String(Date.now()).slice(-6);
console.log('note title =', TITLE);
await ensureUnlocked();
await ev(`location.hash='#/notes'`);
let dl=Date.now()+20000; while(Date.now()<dl && (await ev('location.hash'))!=='#/notes') await sleep(300);
await sleep(2500);
await ensureUnlocked();
await ev(`location.hash='#/notes'`); await sleep(1500);
const onList=(await ev('location.hash'))==='#/notes';
check('进入笔记列表', onList, 'hash='+await ev('location.hash'));

// 打开新建
// 新建按钮：class=notes-action + aria-label="新建笔记"（不是 id；文字只是图标 add）
const openCreate=await ev(`(function(){var e=document.querySelector('.notes-action[aria-label="新建笔记"]');if(e){e.click();return 1}return 0})()`);
await waitFor(`!!document.querySelector('textarea[placeholder*="一句话概括"]')`,15000);
const formOpen=await ev(`!!document.querySelector('textarea[placeholder*="一句话概括"]')`);
check('打开笔记新建页', formOpen===true, `open=${openCreate} form=${formOpen}`);

if(formOpen){
  await ev(fill('textarea[placeholder*="一句话概括"]', TITLE));
  await ev(fill('textarea[placeholder*="全屏编辑"]', '正文-'+TITLE));
  await sleep(700);
  const clickedCreate=await ev(clickText('创建',false));
  await sleep(1500);
  // 轮询等列表回显
  const found=await waitFor(`(function(){var cs=document.querySelectorAll('.note-card,.note-title');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0)return true}return false})()`,18000);
  const listCards=await ev(`document.querySelectorAll('.note-card').length`);
  check('创建后笔记在列表回显（写路径通）', !!found, `create=${clickedCreate} cards=${listCards} found=${!!found}`);
}
const passed=checks.filter(c=>c.pass).length;
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`);
process.exit(passed===checks.length?0:1);
