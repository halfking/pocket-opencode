// diag-finance-samescope.mjs —— 用 App 自己的(default 作用域)token 播种。
// 若 App 能显示该记录 => 记账读路径本身正常，此前 FAIL 是
// 「测试播到 ws_user-admin、App 看 default」的跨桶错配。
import { execFileSync } from 'node:child_process';
import http from 'node:http';

const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9269';
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});
function api(path,{token,method='GET',body}={}){return new Promise((res)=>{const payload=body?JSON.stringify(body):'';const h={};if(token)h.Authorization='Bearer '+token;if(payload){h['Content-Type']='application/json';h['Content-Length']=Buffer.byteLength(payload);}const req=http.request({host:'127.0.0.1',port:Number(process.env.POCKET_API_PORT || 8088),path,method,headers:h,timeout:15000},(r)=>{let b='';r.on('data',c=>b+=c);r.on('end',()=>res({status:r.statusCode,body:b}));});req.on('error',e=>res({status:0,body:String(e)}));req.on('timeout',()=>{req.destroy();res({status:0,body:'timeout'})});if(payload)req.write(payload);req.end();});}

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

// 拿 App 当前的 token（default 作用域）
const appToken=await ev(`localStorage.getItem('pocket_token')||''`);
let claimWs='?'; try{claimWs=JSON.parse(Buffer.from(appToken.split('.')[1],'base64').toString('utf8')).workspace_id;}catch{}
console.log('App 当前 token 的 workspace =', claimWs);

// 用这个 token 播一条（会落进 default 桶）
const STAMP=Date.now().toString().slice(-6);
const NOTE=`SAMESCOPE-${STAMP}`;
const seed=await api('/api/finance',{token:appToken,method:'POST',body:{type:'expense',amount:23.45,category:'DIAG',note:NOTE,source:'manual'}});
let seedId=null; try{seedId=JSON.parse(seed.body).id}catch{}
console.log('同作用域播种 status=',seed.status,' id=',seedId);

// 失败路径也必须删 SEED：这是**共享**开发库，中间任何抛错都会把行留下，
// 而那些行会被另一会话当成真实数据卷进它的基线（= 污染别人的运行）。
// 幂等 + 挂异常钩子；process.on('exit') 不能 await，所以用前两个。
let cleaned=false;
async function cleanupSeed(reason){
  if(!seedId||cleaned) return; cleaned=true;
  try{ const cl=await api(`/api/finance/${seedId}`,{token:appToken,method:'DELETE'});
       console.log(`[cleanup:${reason}] 删除 SEED ${seedId} -> ${cl.status}`); }
  catch(e){ console.error(`[cleanup:${reason}] 删除 SEED ${seedId} 失败：${String(e?.message||e).slice(0,120)}`); }
}
process.on('unhandledRejection',async e=>{ console.error('[未处理的 rejection]',e); await cleanupSeed('rejection'); process.exit(1); });
process.on('uncaughtException',async e=>{ console.error('[未捕获异常]',e); await cleanupSeed('exception'); process.exit(1); });

// 进记账页 + 刷新
await ev(`location.hash='#/finance'`);
const d2=Date.now()+15000;while(Date.now()<d2&&(await ev('location.hash'))!=='#/finance')await sleep(300);
await sleep(1800);
await ev(`(function(){var b=document.querySelector('button[aria-label="刷新"]');if(b){b.click();return 1}return 0})()`);
let found=null;const d3=Date.now()+15000;
while(Date.now()<d3){
  found=await ev(`(function(){var ps=document.querySelectorAll('.inner-pane,.outer-pane');var pane=null;for(var i=0;i<ps.length;i++){if(ps[i].offsetParent!==null){pane=ps[i];break}}if(!pane)pane=document.querySelector('.page')||document.body;var cs=pane.querySelectorAll('.tx-card');for(var j=0;j<cs.length;j++){if((cs[j].textContent||'').indexOf(${JSON.stringify(NOTE)})>=0)return cs[j].textContent}return null})()`);
  if(found)break; await sleep(600);
}
const n=await ev(`document.querySelectorAll('.tx-card').length`);
console.log('同作用域 SEED 是否显示 =', found?('YES -> '+found.replace(/\s+/g,' ').slice(0,70)):('NO (共 '+n+' 张卡)'));
console.log(found?'✅ 读路径正常：此前 FAIL 是跨工作区错配(测试播 ws_user-admin / App 看 default)':'❌ 同作用域仍不显示 -> 读路径确有独立 bug');
await cleanupSeed('normal');
process.exit(0);
