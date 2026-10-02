// diag-finance-workspace.mjs —— 定位「https 下记账页读不到 API 播种记录」
//
// 疑问：verify-finance-writepath 在 https 下读路径 FAIL（SEED 播种进 PG 但 UI 不显示），
// 但写路径全通。把「App 用的 token 作用域」和「测试播种用的 admin 作用域」并排比，
// 判断这是 App 真 bug（list 过滤错）还是测试跨 workspace 比对的假象。
//
// 手法：
//   1. 读 App localStorage 里的 user / workspace_id / token 的 user_id claim
//   2. 直接 API 登录 admin，拿 user_id / workspace_id
//   3. 用 admin token 播种一条 SEED
//   4. 直接查 PG 看 SEED 的 owner_id / workspace_id
//   5. 在 App 页面上下文里用 App 自己的 token 调 /api/finance，看能不能看到 SEED
//   6. 用 admin token 从 Node 直调 /api/finance，看能不能看到 SEED
// 两者可见性不同 → 作用域不一致（测试假象）；两者都不可见 → 服务端过滤 bug。
import { execFileSync } from 'node:child_process';
import { requireDevPass } from './lib/dev-pass.mjs'
import http from 'node:http';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9263';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 });
const PSQL = process.env.POCKET_PSQL || 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe';
const psql = (sql) => execFileSync(PSQL, ['-h','127.0.0.1','-p','5432','-U','postgres','-d','postgres','-t','-A','-c',sql], { encoding: 'utf8' }).trim();

function api(path, { token, method='GET', body } = {}) {
  return new Promise((res) => {
    const payload = body ? JSON.stringify(body) : '';
    const h = {};
    if (token) h.Authorization = 'Bearer ' + token;
    if (payload) { h['Content-Type']='application/json'; h['Content-Length']=Buffer.byteLength(payload); }
    const req = http.request({ host:'127.0.0.1', port:Number(process.env.POCKET_API_PORT || 8088), path, method, headers:h, timeout:15000 }, (r) => {
      let b=''; r.on('data',(c)=>b+=c); r.on('end',()=>res({ status:r.statusCode, body:b }));
    });
    req.on('error',(e)=>res({ status:0, body:String(e) }));
    req.on('timeout',()=>{ req.destroy(); res({ status:0, body:'timeout' }); });
    if (payload) req.write(payload);
    req.end();
  });
}

const devPass = requireDevPass()

// ---- CDP ----
const pid = adb(['-s',SERIAL,'shell',`pidof ${PKG}`]).trim().split(/\s+/)[0];
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2); }
const socks = adb(['-s',SERIAL,'shell',`cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map(l=>l.trim().replace('@','')).filter(Boolean);
adb(['-s',SERIAL,'forward',`tcp:${PORT}`,`localabstract:${socks.find(s=>s.endsWith(`_${pid}`))||socks[socks.length-1]}`]);
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find(t=>t.type==='page');
if (!page) { console.log('NO_PAGE'); process.exit(1); }
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`));
let id=0; const pending=new Map();
const send=(m,p={})=>new Promise(r=>{const i=++id;pending.set(i,r);ws.send(JSON.stringify({id:i,method:m,params:p}))});
ws.addEventListener('message',(e)=>{const m=JSON.parse(e.data);if(m.id&&pending.has(m.id)){pending.get(m.id)(m.result);pending.delete(m.id)}});
await new Promise(r=>ws.addEventListener('open',r));
await send('Runtime.enable');
const ev = async (x) => (await send('Runtime.evaluate',{expression:x,returnByValue:true,awaitPromise:true}))?.result?.value;

const origin = await ev('location.origin');
console.log('App origin =', origin);

// ---- 1. App 本地作用域 ----
const appScope = await ev(`(function(){
  var t=localStorage.getItem('pocket_token')||'';
  var uid=null;
  try{ uid=JSON.parse(atob(t.split('.')[1].replace(/-/g,'+').replace(/_/g,'/'))).user_id||JSON.parse(atob(t.split('.')[1].replace(/-/g,'+').replace(/_/g,'/'))).sub||null }catch(e){}
  return { user: localStorage.getItem('pocket_user'), workspace_id: localStorage.getItem('pocket_workspace_id'), token_uid: uid, hasToken: !!t };
})()`);
console.log('App 本地作用域 =', JSON.stringify(appScope));

// ---- 2. admin API 登录 ----
const login = await api('/api/auth/login',{method:'POST',body:{username:'admin',password:devPass}});
let adminScope={};
try { const j=JSON.parse(login.body); adminScope={user_id:j.user_id, workspace_id:j.workspace_id, user:j.user}; } catch{}
console.log('admin API 作用域 =', JSON.stringify(adminScope), 'status=', login.status);
const adminToken = JSON.parse(login.body).token;

// ---- 3. admin 播种 ----
const STAMP = Date.now().toString().slice(-6);
const NOTE = `DIAG-SEED-${STAMP}`;
const seed = await api('/api/finance',{token:adminToken,method:'POST',body:{type:'expense',amount:11.11,category:'DIAG',note:NOTE,source:'manual'}});
let seedId=null; try{ seedId=JSON.parse(seed.body).id }catch{}
console.log('SEED =', NOTE, 'id=', seedId, 'status=', seed.status);

// ---- 4. PG 看 SEED 的 owner/workspace ----
const pgRow = psql(`select id||' | owner='||coalesce(owner_id,'NULL')||' | ws='||coalesce(workspace_id,'NULL') from opencode_pocket.finance_transactions where note='${NOTE}';`);
console.log('PG 里 SEED =', pgRow || '(没找到!)');

// ---- 5. App 上下文用自己的 token 调 list ----
const appList = await ev(`(async function(){
  try{
    var t=localStorage.getItem('pocket_token')||'';
    var r=await fetch('/api/finance',{headers:{Authorization:'Bearer '+t}});
    var j=await r.json();
    var arr=(j&&j.transactions)||[];
    return { status:r.status, count:arr.length, hasSeed: arr.some(x=>(x.note||'').indexOf(${JSON.stringify(NOTE)})>=0), notes: arr.slice(0,5).map(x=>x.note) };
  }catch(e){ return { error:String(e) } }
})()`);
console.log('App token 调 list =', JSON.stringify(appList));

// ---- 6. admin token 从 Node 直调 list ----
const adminList = await api('/api/finance',{token:adminToken});
let adminView={status:adminList.status};
try{ const j=JSON.parse(adminList.body); const arr=j.transactions||[]; adminView={status:adminList.status,count:arr.length,hasSeed:arr.some(x=>(x.note||'').indexOf(NOTE)>=0)} }catch{ adminView.body=adminList.body.slice(0,120) }
console.log('admin token 调 list =', JSON.stringify(adminView));

// ---- 结论 ----
console.log('\n=== 判定 ===');
if (appScope.workspace_id && adminScope.workspace_id && appScope.workspace_id !== adminScope.workspace_id) {
  console.log('⚠️ App 与 admin 的 workspace_id 不一致 —— 读路径 FAIL 是测试跨作用域播种的假象，不是 App bug');
} else if (appList.hasSeed) {
  console.log('App 其实能看到 SEED —— 读路径 FAIL 是时序/等待问题，不是作用域问题');
} else {
  console.log('作用域一致但 App 看不到 SEED —— 需要继续查服务端 ListScoped 过滤或 App 的 fetch');
}

// 清理
if (seedId) { const cl=await api(`/api/finance/${seedId}`,{token:adminToken,method:'DELETE'}); console.log('清理 SEED ->', cl.status); }
process.exit(0);
