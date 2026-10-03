// 判定笔记保存后不显示：查后端 + 重载列表。
import { execFileSync } from 'node:child_process';
import http from 'node:http';
const ADB='C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL=process.env.POCKET_SERIAL||'192.168.31.19:5555';
const PKG='com.kaixuan.opencode.pocket';
const PORT=process.env.POCKET_CDP_PORT||'9311';
const PSQL=process.env.POCKET_PSQL||'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe';
// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
const adb=(a,t=60000)=>execFileSync(ADB,a,{encoding:'utf8',timeout:t,maxBuffer:33554432});
const psql=(sql)=>execFileSync(PSQL,['-h','127.0.0.1','-p','5432','-U','postgres','-d','postgres','-t','-A','-c',sql],{encoding:'utf8'}).trim();
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
// 后端：标题以 NI- 开头的 ASCII 笔记
console.log('PG notes 表里 title like NI-% 的行数 =', psql(`select count(*) from ${SCHEMA}.notes where title like 'NI-%'`));
const recent=psql(`select id||' | '||coalesce(title,'')||' | ws='||coalesce(workspace_id,'NULL') from ${SCHEMA}.notes order by created_at desc limit 4`).replace(/[^\x00-\x7F]/g,'?');
console.log('最近 4 条 =', recent);
// 当前 App hash 与列表
console.log('当前 hash =', await ev('location.hash'));
console.log('当前 .note-card 数 =', await ev(`document.querySelectorAll('.note-card').length`));
// 页面上的提示/错误
console.log('页面提示 =', (await ev(`(function(){var e=document.querySelector('.error,.state,.toast,[role=alert]');return e?e.textContent.replace(/\\s+/g,' ').slice(0,120):'(none)'})()`)));
process.exit(0);
