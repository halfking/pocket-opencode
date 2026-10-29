import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../test-support/notes-sqlite-fixture.mjs'

const markdown = '  # Full **Markdown**\n\n[link](https://example.test/a)\n[附件: resource-hash]\n  '

test('new ENEX preserves Markdown, tags and source time in an uploadable projection', async () => fixture(async f => {
  await f.setMode('local')
  const originalUpdated = Date.parse('2024-03-02T01:02:03Z')
  const asset = await f.assetStore.upsert({kind:'note',title:'Imported',bodyText:markdown,source:'enex_import',workspaceId:'workspace',metaJson:JSON.stringify({tags:[' Smith, John ', '研究'],originalCreated:originalUpdated-1000,originalUpdated})})
  const store = f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  const [item] = await store.pending()
  assert.ok(item, 'new managed Markdown should be uploadable')
  assert.equal(item.payload.format,'markdown')
  assert.equal(item.payload.content,markdown)
  assert.deepEqual(item.payload.tags,[' Smith, John ', '研究'])
  assert.equal(item.client_updated_at,new Date(originalUpdated).toISOString())
  assert.equal((await f.assetStore.getForWorkspace(asset.id,'workspace')).bodyText,markdown)
}))

test('remote Markdown is an imported asset and edits or tombstones retain local attachments', async () => fixture(async f => {
  await f.setMode('local')
  const store = f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  const { applyPage } = await f.loadModule('/src/native/portal-sync/apply.ts')
  const { notePayload } = await f.loadModule('/src/native/portal-sync/contract.ts')
  const remote = {module:'notes',kind:'note',ext_id:'asset:foreign',title:'Remote import',payload:notePayload({format:'markdown',content:markdown,tags:['one'],created_at:1000}),client_updated_at:'2024-03-02T01:02:03.000001Z',op_id:'remote-1',deleted:false,epoch:0,change_seq:1}
  await applyPage(store,{changes:[remote],epochs:{notes:0},next_cursor:'one'})
  const row = await store.byExternalId(remote.ext_id)
  const asset = await f.assetStore.getForWorkspace(row.source_id,'workspace')
  assert.equal(row.source,'asset')
  assert.equal(asset.source,'enex_import')
  assert.equal((await f.notes.getNote(asset.id,false,'workspace')).content,markdown)
  await f.assetStore.addBlob(asset.id,'PRIVATE-ATTACHMENT',{kind:'application/octet-stream',hash:'local-hash'})
  await applyPage(store,{changes:[{...remote,title:'Edited in portal',payload:notePayload({...remote.payload,content:'# Edited'}),client_updated_at:'2024-03-03T01:02:03.000002Z',op_id:'remote-2',change_seq:2}],epochs:{notes:0},next_cursor:'two'})
  assert.equal((await f.assetStore.getForWorkspace(asset.id,'workspace')).bodyText,'# Edited')
  assert.equal((await f.assetStore.listBlobs(asset.id)).length,1)
  const [blob] = await f.assetStore.listBlobs(asset.id)
  assert.equal(await f.assetStore.getBlob(blob.id),'PRIVATE-ATTACHMENT')
  await applyPage(store,{changes:[{...remote,payload:{},deleted:true,client_updated_at:'2024-03-04T01:02:03.000003Z',op_id:'remote-3',change_seq:3}],epochs:{notes:0},next_cursor:'three'})
  assert.equal(await f.notes.getNote(asset.id,false,'workspace'),null)
  assert.equal(f.count('local_asset_blobs'),1)
}))

test('import holds the original owner and cannot attach a resource after account switch', async () => fixture(async f => {
  await f.setMode('local')
  const { importEnex } = await f.loadModule('/src/features/imports/import-service.ts')
  const paused = gate(); f.onEncrypt(paused.pause)
  const xml='<en-export><note><title>A private import</title><content><![CDATA[<en-note>body</en-note>]]></content><created>20240301T010203Z</created><updated>20240302T010203Z</updated><resource><data encoding="base64">c2VjcmV0</data><mime>text/plain</mime></resource></note></en-export>'
  const importing=importEnex(xml,'workspace'); importing.catch(()=>{})
  await paused.started
  await f.login('B')
  paused.release()
  await assert.rejects(importing,{name:'AbortError'})
  assert.equal(f.count('local_asset_blobs'),0)
  assert.equal((await f.assetStore.search({workspaceId:'workspace',kind:'note'})).length,0)
  await f.login('A'); f.onEncrypt(async()=>{})
  await importEnex(xml,'workspace')
  assert.equal(f.count('local_assets'),1,'retry keeps the already committed parent')
  assert.equal(f.count('local_asset_blobs'),1,'retry completes its interrupted attachment')
}))

test('an imported Markdown note cannot be saved through the HTML editor and attachments are owner isolated', async () => fixture(async f => {
  await f.setMode('local')
  const asset=await f.assetStore.upsert({kind:'note',title:'Markdown',bodyText:markdown,source:'enex_import',workspaceId:'workspace',metaJson:JSON.stringify({originalUpdated:1000})})
  const blob=await f.assetStore.addBlob(asset.id,'secret binary',{hash:'blob-hash'})
  assert.equal((await f.pkm.getNote(asset.id,'workspace')).format,'markdown')
  await assert.rejects(f.pkm.saveNote({id:asset.id,title:'HTML editor',html:'<p>normalized</p>',workspaceId:'workspace'}),/Markdown/)
  assert.equal((await f.assetStore.getForWorkspace(asset.id,'workspace')).bodyText,markdown)
  await f.login('B')
  assert.equal(await f.assetStore.getBlob(blob.id),null)
  assert.deepEqual(await f.assetStore.listBlobs(asset.id),[])
  await assert.rejects(f.assetStore.addBlob(asset.id,'B content'),/unavailable/)
  await f.login('A')
  assert.equal(await f.assetStore.getBlob(blob.id),'secret binary')
}))

test('imports retain their initiating reset epoch through duplicate lookup',async()=>fixture(async f=>{
  await f.setMode('local')
  const { importEnex }=await f.loadModule('/src/features/imports/import-service.ts')
  const paused=gate(), originalLookup=f.assetStore.findByMeta.bind(f.assetStore)
  f.assetStore.findByMeta=async(...args)=>{const row=await originalLookup(...args);await paused.pause();return row}
  const importing=importEnex('<en-export><note><title>Epoch</title><content><![CDATA[<en-note>body</en-note>]]></content><updated>20240302T010203Z</updated></note></en-export>','workspace')
  await paused.started
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  await store.observeEpoch(1)
  paused.release();await importing
  f.assetStore.findByMeta=originalLookup
  const row=await f.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  assert.equal((await store.decode(row.item_json)).epoch,0)
  assert.equal(row.last_error,'reset_epoch_mismatch')
  assert.equal((await store.pending()).length,0)
}))

test('legacy ENEX assets stay unclaimed and oversized originals remain local without truncation',async()=>fixture(async f=>{
  await f.setMode('local')
  await f.db.run("INSERT INTO local_assets(id,workspace_id,kind,title,body_text,source,sync_mode,created_at,updated_at) VALUES('legacy','workspace','note','Legacy',?,'enex_import','e2ee_local_first',1,1)",[markdown])
  await f.notes.updateNote('legacy',{title:'Local legacy edit'},'workspace')
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  assert.equal(await store.find('asset','legacy'),null)
  assert.equal((await store.pending()).length,0)
  const body='中'.repeat(90000)
  const asset=await f.assetStore.upsert({kind:'note',title:'Large',bodyText:body,source:'enex_import',workspaceId:'workspace',metaJson:JSON.stringify({originalUpdated:1000})})
  const out=await f.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  assert.match(out.last_error,/256 KiB/)
  assert.equal((await store.decode(out.item_json)).payload.content,body)
  assert.equal((await f.assetStore.getForWorkspace(asset.id,'workspace')).bodyText,body)
}))
