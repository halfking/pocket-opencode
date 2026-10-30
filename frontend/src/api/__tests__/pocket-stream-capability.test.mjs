import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture } from '../../test-support/notes-sqlite-fixture.mjs'

for (const kind of ['session', 'gateway']) test(`${kind} SSE blocks default credentials on first connect and after a direct-mode reconnect`, () => fixture(async f => {
  const originals=Object.fromEntries(['window','EventSource','setTimeout'].map(key=>[key,Object.getOwnPropertyDescriptor(globalThis,key)]))
  const sources=[],timers=[],errors=[]
  const browser={setInterval,clearInterval,setTimeout:fn=>{timers.push(fn);return 1},clearTimeout,performance,
    location:{origin:'http://127.0.0.1',protocol:'http:',hostname:'127.0.0.1'}}
  class Source {
    static CLOSED=2
    readyState=1
    constructor(url){this.url=String(url);sources.push(this)}
    close(){this.readyState=2}
    addEventListener(){}
  }
  Object.defineProperty(globalThis,'window',{configurable:true,value:browser})
  Object.defineProperty(globalThis,'EventSource',{configurable:true,value:Source})
  let stream
  try {
    const {SessionSSEClient}=await f.loadModule('/src/api/sse.ts')
    const {GatewayLiveClient}=await f.loadModule('/src/api/gateway-live.ts')
    const make=()=>kind==='session'
      ? new SessionSSEClient('session','instance',()=>f.auth.token,{onError:e=>errors.push(e)})
      : new GatewayLiveClient(1,()=>f.auth.token,{onError:e=>errors.push(e)},()=>true)
    f.auth.authMethod='redclaw'
    stream=make();stream.open()
    assert.equal(sources.length,0)
    assert.equal(errors[0].message,'pocket_resource_unavailable')
    stream.close()

    f.auth.authMethod='legacy'
    stream=make();stream.open()
    assert.equal(sources.length,1)
    assert.ok(sources[0].url.includes('token=token-A'))
    Object.defineProperty(globalThis,'setTimeout',{configurable:true,value:fn=>{timers.push(fn);return 1}})
    sources[0].onerror(new Error('network interrupted'))
    assert.equal(timers.length,1)
    f.auth.authMethod='redclaw'
    timers.shift()()
    assert.equal(sources.length,1,'a delayed reconnect must not append the replacement parent token to a URL')
    assert.equal(errors.at(-1).message,'pocket_resource_unavailable')
    assert.equal(f.auth.token,'token-A')
  } finally {
    stream?.close()
    for(const [key,descriptor] of Object.entries(originals)){if(descriptor)Object.defineProperty(globalThis,key,descriptor);else delete globalThis[key]}
  }
}))
