// probe-login-paths.mjs —— 直接对 /api/auth/login 打两发，比较 auth_method 与 workspace_id。
// 目的：确认「dev-bypass 返回 default / legacy 返回 ws_user-admin」这条分歧在当前后端是否可复现。
import { execFileSync } from 'node:child_process';
import { requireDevPass } from './lib/dev-pass.mjs'
import http from 'node:http';

const devPass = requireDevPass()
function post(path,body){
  return new Promise((res)=>{
    const payload=JSON.stringify(body);
    const req=http.request({host:'127.0.0.1',port:8088,path,method:'POST',headers:{'Content-Type':'application/json','Content-Length':Buffer.byteLength(payload)},timeout:15000},(r)=>{let b='';r.on('data',c=>b+=c);r.on('end',()=>res({status:r.statusCode,body:b}));});
    req.on('error',e=>res({status:0,body:String(e)}));
    req.write(payload);req.end();
  });
}
// 1) admin / devPass
let r=await post('/api/auth/login',{username:'admin',password:devPass});
try{const j=JSON.parse(r.body);console.log('login(admin,devPass): status=',r.status,'auth_method=',j.auth_method,'workspace_id=',j.workspace_id,'user_id=',j.user_id);}catch{console.log('login(admin,devPass): status=',r.status,'body=',r.body.slice(0,150));}
// 2) 连打三次看是否稳定
for(let i=0;i<3;i++){ r=await post('/api/auth/login',{username:'admin',password:devPass}); try{const j=JSON.parse(r.body);console.log(`  #${i+2}: auth_method=${j.auth_method} workspace_id=${j.workspace_id}`);}catch{console.log(`  #${i+2}: ${r.status} ${r.body.slice(0,80)}`);} }
process.exit(0);
