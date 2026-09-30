import { readFileSync } from 'node:fs';
import http from 'node:http';
const devPass=(readFileSync('backend/internal/server/server_assistant.go','utf8').match(/devPass\s*=\s*"([^"]+)"/)||[])[1]||'';
function req(path,{token,method='GET'}={}){return new Promise((res)=>{const h={};if(token)h.Authorization='Bearer '+token;const r=http.request({host:'127.0.0.1',port:8088,path,method,headers:h,timeout:15000},(x)=>{let b='';x.on('data',c=>b+=c);x.on('end',()=>res({status:x.statusCode,body:b}))});r.on('error',e=>res({status:0,body:String(e)}));r.on('timeout',()=>{r.destroy();res({status:0,body:'timeout'})});r.end()})}
const login=await req('/api/auth/login',{method:'POST'});
// login needs body; do it manually
const doLogin=()=>new Promise((res)=>{const p=JSON.stringify({username:'admin',password:devPass});const r=http.request({host:'127.0.0.1',port:8088,path:'/api/auth/login',method:'POST',headers:{'Content-Type':'application/json','Content-Length':Buffer.byteLength(p)},timeout:15000},(x)=>{let b='';x.on('data',c=>b+=c);x.on('end',()=>res(JSON.parse(b)))});r.write(p);r.end()});
const {token}=await doLogin();
for (const p of ['/api/flashcards?since=0&limit=200','/api/flashcards/notes?since=0','/api/flashcards/decks']) {
  const r=await req(p,{token});
  console.log(`GET ${p}\n  status=${r.status}  body=${r.body.slice(0,220)}\n`);
}
process.exit(0);
