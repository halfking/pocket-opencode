// verify-vault-writepath.mjs —— 真机验证密码箱写路径（走 Web Crypto 降级，非 Keystore 插件）
//
// 目的：坐实「Keystore 原生插件缺失不是密码箱的功能死锁」——
//       密码箱在 secure context 下用 Web Crypto 正常工作。
// 判据：解锁 -> 新增表单 -> 填表保存 -> 条目上屏 -> 重新加载后仍在（落库）。
import { execFileSync } from 'node:child_process';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9292';
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
const clickText=(t)=>`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button'));for(var i=0;i<b.length;i++){if((b[i].textContent||'').trim()===${JSON.stringify(t)}){b[i].click();return 1}}return 0})()`;
// 精确点击「解锁」：不能用包含匹配 ——「指纹/面容解锁」排在前面且含「解锁」二字，
// 包含匹配会点到生物识别分支（无绑定凭据时静默失败，表现为 unlocked 永远 false）。
const clickVaultUnlock=clickText('解锁');
// 「➕ 新增」带 emoji，精确匹配对不上；用包含匹配取第一个（工具栏按钮先于 EmptyState 的「新增条目」）。
const clickContains=(t)=>`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button'));for(var i=0;i<b.length;i++){if((b[i].textContent||'').indexOf(${JSON.stringify(t)})>=0){b[i].click();return 1}}return 0})()`;
const fillByPlaceholder=(ph,val)=>`(function(){var e=document.querySelector('input[placeholder*=${JSON.stringify(ph)}],textarea[placeholder*=${JSON.stringify(ph)}]');if(!e)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(val)});e.dispatchEvent(new Event('input',{bubbles:true}));return 'ok'})()`;
// 主密码解锁全局 crypto（crypto key 只在内存，reload 后必须重来）
const MASTER=process.env.POCKET_MASTER||'PocketTest2026';
async function unlockMasterCrypto(){
  if(!(await ev(`!!document.querySelector('input[placeholder*="主密码"]')`))) return 'no-prompt';
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
  await sleep(700);
  await ev(clickText('解锁'));
  await sleep(5000);
  return 'ok';
}

await ev(`location.hash='#/vault'`);
let dl=Date.now()+20000; while(Date.now()<dl && (await ev('location.hash'))!=='#/vault') await sleep(300);
await sleep(2500);
// 若被重定向到主密码解锁页，先解锁全局 crypto
if((await ev('location.hash')).indexOf('unlock=1')>=0){ await ev(`location.hash='#/vault'`); await sleep(2000); await unlockMasterCrypto(); await ev(`location.hash='#/vault'`); await sleep(1500); }

// 1) 确保密码箱处于解锁态。已解锁则不重复点（此时没有「解锁」按钮）。
let unlockedNow=await ev(`!!document.querySelector('.vault-unlocked')`);
if(!unlockedNow){ await ev(clickVaultUnlock); await sleep(2000); }
const unlocked=await ev(`!!document.querySelector('.vault-unlocked')`);
check('密码箱可解锁（Web Crypto 降级路径，非 Keystore 插件）', unlocked===true, `unlocked=${unlocked}`);

// 2) 打开新增表单
const clickedAdd=await ev(clickContains('新增'));
await sleep(1200);
const formOpen=await ev(`!!document.querySelector('.add-form')`);
check('可打开「新增」表单', formOpen===true, `add=${clickedAdd} form=${formOpen}`);

// 3) 填表并保存
const title='Vault回归-'+String(Date.now()).slice(-5);
const pwd='P@ss-'+String(Date.now()).slice(-5);
await ev(fillByPlaceholder('标题', title));
await ev(fillByPlaceholder('用户名', 'regress-user'));
await ev(fillByPlaceholder('密码', pwd));
await sleep(600);
await ev(clickText('保存'));
await sleep(2500);

// 4) 条目上屏
const listed=await ev(`(function(){var cs=document.querySelectorAll('.entry-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(title)})>=0)return true}return false})()`);
check('保存后条目出现在密码箱列表（写路径通）', listed===true, `listed=${listed}`);

// 5) 重新加载后仍在（落库）。reload 后 crypto key 丢失，需重新做主密码解锁。
await ev('location.reload()');
let rdy=null; const dl2=Date.now()+30000;
while(Date.now()<dl2){await sleep(1000);rdy=await ev('location.origin');if(rdy&&rdy!=='null')break;}
await sleep(2000);
await ev(`location.hash='#/vault'`); let dl3=Date.now()+15000; while(Date.now()<dl3&&(await ev('location.hash'))!=='#/vault')await sleep(300);
await sleep(2000);
if((await ev('location.hash')).indexOf('unlock=1')>=0){ await ev(`location.hash='#/vault'`); await sleep(1500); await unlockMasterCrypto(); await ev(`location.hash='#/vault'`); await sleep(1500); }
if(!(await ev(`!!document.querySelector('.vault-unlocked')`))){ await ev(clickVaultUnlock); await sleep(2000); }
const stillThere=await ev(`(function(){var cs=document.querySelectorAll('.entry-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(title)})>=0)return true}return false})()`);
check('重新加载后条目仍在（Web Crypto 加密落库 + 解密回显）', stillThere===true, `stillThere=${stillThere}`);

const passed=checks.filter(c=>c.pass).length;
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`);
process.exit(passed===checks.length?0:1);
