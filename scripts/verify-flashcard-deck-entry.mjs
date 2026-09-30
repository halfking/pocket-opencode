// verify-flashcard-deck-entry.mjs —— 真机验证「有卡组时列表页的建组入口」(BUG-K follow-up)
//
// 判据设计（必须能区分修前/修后）：
//   1. 前置：确保至少 1 个卡组存在（零卡组走的是 BUG-U 内联表单，不是本次入口）
//   2. 列表页有卡组时，[data-testid=deck-create-toggle] 必须存在
//      —— 修前该元素根本不存在（建组入口只在卡片编辑页）
//   3. 点开 toggle -> 表单出现 -> 填名 -> 建组 -> 列表里出现新卡组
//      —— 修前点不到、也建不了第 2 个卡组
import { execFileSync } from 'node:child_process';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9280';
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});

const pid=adb(['-s',SERIAL,'shell',`pidof ${PKG}`]).trim().split(/\s+/)[0];
if(!pid){console.log('APP_NOT_RUNNING');process.exit(2);}
const socks=adb(['-s',SERIAL,'shell',`cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map(l=>l.trim().replace('@','')).filter(Boolean);
adb(['-s',SERIAL,'forward',`tcp:${PORT}`,`localabstract:${socks.find(s=>s.endsWith(`_${pid}`))||socks[socks.length-1]}`]);
const page=(await(await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find(t=>t.type==='page');
if(!page){console.log('NO_PAGE');process.exit(1);}
const ws=new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//,`:${PORT}/`));
let id=0;const pending=new Map();
const send=(m,p={})=>new Promise(r=>{const i=++id;pending.set(i,r);ws.send(JSON.stringify({id:i,method:m,params:p}))});
ws.addEventListener('message',e=>{const m=JSON.parse(e.data);if(m.id&&pending.has(m.id)){pending.get(m.id)(m.result);pending.delete(m.id)}});
await new Promise(r=>ws.addEventListener('open',r));
await send('Runtime.enable');
const ev=async(x)=>(await send('Runtime.evaluate',{expression:x,returnByValue:true,awaitPromise:true}))?.result?.value;

const checks=[];
const check=(n,pass,d)=>{checks.push({n,pass});console.log(`${pass?'PASS':'FAIL'}  ${n}${d?'  — '+d:''}`)};

// 进闪卡列表
await ev(`location.hash='#/flashcards'`);
let dl=Date.now()+20000; while(Date.now()<dl && (await ev('location.hash'))!=='#/flashcards') await sleep(300);
await sleep(3000);

const PANE=`(function(){var ps=document.querySelectorAll('.inner-pane, .outer-pane');for(var i=0;i<ps.length;i++){if(ps[i].offsetParent!==null)return ps[i];}return document.querySelector('.page')||document.body;})()`;
const deckCount=async()=>ev(`(${PANE}).querySelectorAll('[data-testid="flashcards-deck-item"]').length`);

// 若零卡组：先走零态表单建一个（否则看不到本次入口）
let n0=await deckCount();
if(n0===0){
  console.log('零卡组，先经零态表单建 1 个卡组作为前置…');
  const first=String(Date.now()).slice(-4);
  await ev(`(function(){var f=document.querySelector('[data-testid="deck-create-form"]');if(!f)return 'NF';var i=f.querySelector('input');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(i),'value').set;s.call(i,'前置卡组${first}');i.dispatchEvent(new Event('input',{bubbles:true}));return 'ok'})()`);
  await sleep(500);
  await ev(`(function(){var f=document.querySelector('[data-testid="deck-create-form"]');if(!f)return 'NF';f.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));return 'submitted'})()`);
  await sleep(2500);
  n0=await deckCount();
}
check('前置：有卡组存在（否则本次入口不可见）', n0>0, `decks=${n0}`);

// 判据 2：有卡组时 toggle 入口存在
const toggleExists=await ev(`!!(${PANE}).querySelector('[data-testid="deck-create-toggle"]')`);
check('有卡组时列表页存在「新建卡组」入口(deck-create-toggle)', toggleExists===true, `exists=${toggleExists}`);

if(toggleExists){
  // 判据 3：点开 -> 表单 -> 建第 2 个卡组 -> 列表出现
  await ev(`(function(){var b=(${PANE}).querySelector('[data-testid="deck-create-toggle"]');if(b){b.click();return 1}return 0})()`);
  await sleep(800);
  const formShown=await ev(`!!(${PANE}).querySelector('[data-testid="deck-create-form-existing"]')`);
  check('点开入口后建组表单展开', formShown===true, `form=${formShown}`);
  const second='次卡组'+String(Date.now()).slice(-4);
  await ev(`(function(){var f=(${PANE}).querySelector('[data-testid="deck-create-form-existing"]');if(!f)return 'NF';var i=f.querySelector('input');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(i),'value').set;s.call(i,${JSON.stringify(second)});i.dispatchEvent(new Event('input',{bubbles:true}));return 'ok'})()`);
  await sleep(500);
  await ev(`(function(){var f=(${PANE}).querySelector('[data-testid="deck-create-form-existing"]');if(!f)return 'NF';f.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));return 'submitted'})()`);
  await sleep(3000);
  const n1=await deckCount();
  const listed=await ev(`(function(){var cs=(${PANE}).querySelectorAll('[data-testid="flashcards-deck-item"]');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(second)})>=0)return true}return false})()`);
  check('从列表页入口成功建出第 2 个卡组并出现在列表', n1>n0 && listed===true, `decks ${n0} -> ${n1}, listed=${listed}`);
}

const passed=checks.filter(c=>c.pass).length;
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`);
process.exit(passed===checks.length?0:1);
