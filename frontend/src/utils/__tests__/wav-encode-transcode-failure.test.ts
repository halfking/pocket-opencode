// wav-encode-transcode-failure.test.ts — §819：转码失败必须**留痕**。
//
// ⚠ 这组能在 node 里真跑起来，靠的是一个很方便的性质：
//   `encodeWav16kMono` 依赖 `window.AudioContext`，而 node 里 `window` 不存在
//   ⇒ `ensureGatewayCompatible` 对需要转码的容器**必然走 catch 分支**。
//   ⇒ 「失败有没有被说出来」是可执行断言，不需要 stub 浏览器环境。
//
// ★ 与 §818 的区别（别混）：
//   §818 修的是会议**整段重转**那条路（`toGatewayWavBytes`，调用方 `finalizeRecording`），
//   那里信号所在的函数在 node 里**跑不到**，判据只能是接线扫描；
//   本文件这条路的函数**跑得到** ⇒ 判据是真行为断言，能对「信号变死代码」有牙。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { ensureGatewayCompatible, needsWavTranscode } from '../wav-encode.ts'

/** 真机产物：`VadSegmenter.stop()` 产的就是 `audio/webm`。 */
const webm = (bytes = 1024) =>
  new Blob([new Uint8Array(bytes)], { type: 'audio/webm' })

/** 抓 console.warn，期间不改别的全局。 */
async function captureWarnings(fn: () => Promise<unknown>): Promise<string[]> {
  const seen: string[] = []
  const orig = console.warn
  console.warn = (...a: unknown[]) => { seen.push(a.map(String).join(' ')) }
  try {
    await fn()
  } finally {
    console.warn = orig
  }
  return seen
}

describe('§819 转码失败必须留痕（行为层，node 里必然走 catch 分支）', () => {
  it('★ 降级行为不变：仍退回原 blob，且是同一个对象', async () => {
    const src = webm()
    const out = await ensureGatewayCompatible(src, () => {})
    assert.equal(out, src, '退回的必须是原 blob —— 「不比现状差」是既有契约')
  })

  it('★ 默认通报器必须真的 warn 一次，且消息含根因与原始异常', async () => {
    const warns = await captureWarnings(() => ensureGatewayCompatible(webm(2048)))
    assert.equal(warns.length, 1, `转码失败没有出声（warn ${warns.length} 条）—— §819 的洞没修`)
    assert.match(warns[0], /转码失败/, '消息没说清是转码失败')
    assert.match(warns[0], /2048/, '字节数没进去，无法定位是哪一批音频')
    assert.match(warns[0], /webm/, '原容器 MIME 没进去，排查时不知道退回的是什么')
    // ★ 必须让人能分辨「解码失败」与「环境没有 AudioContext」两种成因
    assert.match(warns[0], /AudioContext|decode|window/i, '原始异常信息丢了')
  })

  it('★ 自定义通报器被调用一次，且三个字段都对得上', async () => {
    const seen: { mimeType: string; bytes: number; error: unknown }[] = []
    const src = webm(4096)
    await ensureGatewayCompatible(src, (i) => { seen.push(i) })
    assert.equal(seen.length, 1)
    assert.equal(seen[0].mimeType, 'audio/webm')
    assert.equal(seen[0].bytes, 4096)
    assert.ok(seen[0].error, 'error 没传出去，无法定位成因')
  })

  it('★ 通报器自己抛错也不得影响降级（不能反过来把收尾炸掉）', async () => {
    const src = webm()
    const out = await ensureGatewayCompatible(src, () => { throw new Error('REPORTER_BROKE') })
    assert.equal(out, src, '通报失败把降级路径也带崩了 —— 比不通报更糟')
  })

  it('负控：已经是 wav 的容器不需转码 ⇒ 一声不吭', async () => {
    const wav = new Blob([new Uint8Array(64)], { type: 'audio/wav' })
    assert.equal(needsWavTranscode('audio/wav'), false)
    const warns = await captureWarnings(() => ensureGatewayCompatible(wav))
    assert.deepEqual(warns, [], '不需要转码却报了转码失败 —— 噪声源')
    assert.equal(await ensureGatewayCompatible(wav), wav)
  })

  it('负控：mp3 同样不需转码', async () => {
    const mp3 = new Blob([new Uint8Array(64)], { type: 'audio/mpeg' })
    const warns = await captureWarnings(() => ensureGatewayCompatible(mp3))
    assert.deepEqual(warns, [])
  })

  it('★ 变异防线：把通报调用改成死代码必须被抓（不是只测「有通报」）', async () => {
    // 这条是判据的牙齿所在：§818 的第一版修复就是这么被放过的
    // （把 `console.warn` 改成 `void 0 && console.warn` 仍然全绿）。
    // 这里 warn 的宿主函数**跑得到**，所以行为断言对它有牙。
    const warns = await captureWarnings(() => ensureGatewayCompatible(webm()))
    assert.equal(warns.length, 1)
  })
})
