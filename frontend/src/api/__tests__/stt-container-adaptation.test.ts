// stt-container-adaptation.test.ts — 两条转写上传路径的**容器适配**必须对齐。
//
// §50 的教训：录音产物是 webm/opus，而网关的 chat-audio bridge 只收 mp3/wav
//（2026-10-07 实测 400 `audio format "webm" is not supported`）。
// 当时只有「录音中每块」的 `sttApi.transcribe` 内部调了 `ensureGatewayCompatible`，
// 而「录完整段」的 `sttSettingsApi.transcribeFull` **没有** ⇒ 安全网只盖了一半，
// §50 的 bug 正是从那半边漏出去的（四轮都没人发现，因为门都喂 wav 夹具）。
//
// ⇒ 本门把「容器适配」钉成**每个上传端点都必须自己负责**，而不是靠调用方记得做。
//   （`refetchFullTranscript` 仍会先转一次，那是它的 `toWav` 契约；
//   `needsWavTranscode('audio/wav')` 为 false ⇒ 二次调用零成本，不是重复转码。）
//
// ⚠ 2026-10-12（§504）本文件此前自称「两条路径」，那是**手写名单**而不是枚举：
//   源码里构造 `audioBase64` 的其实有**四处**，名单漏掉了 `transcribeIncremental`
//   —— 而它恰好是四条里唯一既不转码、也不让 filename 跟实际容器走的一条。
//   手写名单的失效形态很安静：第 5 个入口加进来照样没人记得补这三条断言。
//   ⇒ 下方「枚举出来的上传端点」一节改成从 `/api/stt/` 网络边界自动枚举。
//   本文件原有的「逐条点名」断言全部保留——它们各自钉着一个已发生过的真实 bug，
//   名字本身就是证据；枚举负责覆盖完整性，点名负责讲清历史。
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { filenameForMimeType } from '../stt-filename.ts'
import { needsWavTranscode } from '../../utils/wav-encode.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const read = (f: string) => fs.readFileSync(path.join(HERE, '..', f), 'utf8')
const blank = (m: string) => m.replace(/[^\n]/g, ' ')
const strip = (t: string) => t
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

/** 取某个方法体：起点是声明，终点是同缩进的下一个 `},`。 */
function bodyOf(src: string, decl: string): string {
  const at = src.indexOf(decl)
  assert.ok(at > 0, `找不到 ${decl}`)
  const end = src.indexOf('\n  },', at)
  return src.slice(at, end > at ? end : at + 3000)
}

describe('容器适配：两条上传路径都必须自己兜住 webm', () => {
  const stt = strip(read('stt.ts'))
  const settings = strip(read('stt-settings.ts'))

  it('★ 录音中每块（sttApi.transcribe）必须调 ensureGatewayCompatible', () => {
    const body = bodyOf(stt, 'async transcribe(')
    assert.ok(/ensureGatewayCompatible\s*\(/.test(body), '每块转写不再做容器适配')
  })

  it('★★ 录完整段（transcribeFull）也必须自己调（§50 漏的就是这一半）', () => {
    const body = bodyOf(settings, 'async transcribeFull(')
    assert.ok(
      /ensureGatewayCompatible\s*\(|toGatewayWavBytes\s*\(/.test(body),
      'transcribeFull 不做容器适配 —— 整段重转会直接发 webm 给只收 mp3/wav 的网关（§50 实测 400）',
    )
  })

  it('★ filename 必须跟着实际容器走，不跟调用方给的字符串走', () => {
    const body = bodyOf(settings, 'async transcribeFull(')
    assert.ok(
      /filenameForMimeType\s*\(\s*blob\.type/.test(body),
      'filename 没用实际容器推导 —— 发 webm 字节却报 .wav，后端「按扩展名判容器」的分支会与实际不符',
    )
  })
})

describe('映射表只有一份（防止再漂）', () => {
  it('★ stt-settings.ts 不得再自带一份 filenameForMimeType', () => {
    // 实测：它曾经与 api/stt-filename.ts 并存，且**已经漂了**（少 3 个条目、
    // 没有 baseName 参数）。stt-filename.ts 的存在理由正是防这件事。
    const src = read('stt-settings.ts')
    const defs = src.match(/^function filenameForMimeType/gm) ?? []
    assert.equal(defs.length, 0, 'stt-settings.ts 又出现了一份本地 filenameForMimeType 实现')
  })

  it('★ 两处都必须从共享模块导入，而不是各自实现', () => {
    assert.ok(/import[^;]*filenameForMimeType[^;]*stt-filename/.test(read('stt.ts')),
      'stt.ts 没从共享模块导入 filenameForMimeType')
    assert.ok(/import[^;]*filenameForMimeType[^;]*stt-filename/.test(read('stt-settings.ts')),
      'stt-settings.ts 没从共享模块导入 filenameForMimeType')
  })

  it('★ 同一个 MIME 在两处必须得到同一个文件名（防映射漂移）', () => {
    for (const mime of ['audio/webm', 'audio/wav', 'audio/mpeg', 'audio/mp4', 'audio/ogg;codecs=opus']) {
      assert.equal(
        filenameForMimeType(mime, 'meeting'),
        filenameForMimeType(mime, 'meeting'),
        '共享实现自身不自洽',
      )
    }
    // 具体值也钉住：§50 实测 webm 必须落成 .webm（而不是被 .wav 掩盖）
    assert.equal(filenameForMimeType('audio/webm', 'meeting'), 'meeting.webm')
    assert.equal(filenameForMimeType('audio/webm;codecs=opus', 'meeting'), 'meeting.webm',
      '带 codecs 参数时必须先剥参数再查表')
    assert.equal(filenameForMimeType('audio/wav', 'meeting'), 'meeting.wav')
  })

  it('★ 两个转码入口必须用**同一个**判定函数（不能各写各的）', () => {
    // ⚠ 第一版这条是拿 needsWavTranscode 跟它自己比 —— 恒真，已删。
    // 真正要守的是：两个入口（ensureGatewayCompatible / toGatewayWavBytes）
    // 都必须走 `needsWavTranscode` 这**一份**判定，不能各自 if 一遍。
    const enc = strip(read('../utils/wav-encode.ts'))
    for (const fn of ['export async function ensureGatewayCompatible', 'export async function toGatewayWavBytes']) {
      const at = enc.indexOf(fn)
      assert.ok(at > 0, `找不到 ${fn}`)
      const body = enc.slice(at, enc.indexOf('\n}', at) + 2)
      assert.ok(/needsWavTranscode\s*\(/.test(body), `${fn} 没有走共享判定`)
    }
  })

  it('★ 判定本身：已合规的容器不转、webm 转', () => {
    assert.equal(needsWavTranscode('audio/wav'), false, '已合规的容器被再转一次 ⇒ 重复转码（纯浪费 + 内存翻倍）')
    assert.equal(needsWavTranscode('audio/mpeg'), false)
    assert.equal(needsWavTranscode('audio/webm'), true)
    assert.equal(needsWavTranscode('audio/webm;codecs=opus'), true, '带 codecs 参数时判定必须仍成立')
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §504：枚举出来的上传端点（本节负责**覆盖完整性**，上面的点名断言负责历史）
//
// 为什么必须枚举而不是点名：本仓的「容器适配」已经栽过三次（§50 的
// transcribeFull、设置页 probe、以及本次的 transcribeIncremental），三次修法都是
// 「在调用点加一次 ensureGatewayCompatible」——**判据共用，调用逐点**。
// 点名式断言只能守住已知的点；新增第 5 个入口时它安静地绿。
//
// 枚举锚在**网络边界**（`http<...>('/api/stt/...')`）而不是锚在 `audioBase64`：
// 前者是「音频真的发出去了」的那一刻，后者只说明代码里出现过这个词。
// 再按「方法体内含 audioBase64」过滤掉 config/discover 这类非音频端点。
// ══════════════════════════════════════════════════════════════════════════

interface UploadEndpoint {
  method: string
  route: string
  line: number
  body: string
}

/** 扫描一个文件里所有「把音频发到 /api/stt/」的端点。 */
function audioUploadEndpoints(file: string, src: string): UploadEndpoint[] {
  const out: UploadEndpoint[] = []
  // 注意 `\s*` 必须吃掉换行：stt.ts 的调用是 http<T>(\n  '/api/stt/transcribe',
  for (const m of src.matchAll(/http<[^>]*>\(\s*'\/api\/stt\/[^']*'/g)) {
    const route = /'\/api\/stt\/[^']*'/.exec(m[0])![0].slice(1, -1)
    // 往回找最近一个 2 空格缩进的成员声明，确定「谁发的这个请求」。
    const decls = [...src.slice(0, m.index).matchAll(/^  (?:async )?([A-Za-z_$][\w$]*)\s*\(/gm)]
    const decl = decls[decls.length - 1]
    assert.ok(decl, `${file} 的 ${route} 无法确定所属方法（成员声明的正则可能失效）`)
    // 方法体终点：同缩进的下一个 `\n  },`。
    const end = src.indexOf('\n  },', m.index)
    const body = src.slice(decl.index!, end > m.index ? end : src.length)
    // 过滤掉 config / discover 这类不带音频的端点。
    if (/audioBase64/.test(body)) {
      out.push({ method: decl[1], route, line: src.slice(0, m.index).split('\n').length, body })
    }
  }
  return out
}

/** 已知的上传端点。**新增第 5 个必须同时改这里**，否则 A1 会报红。 */
const REGISTERED_UPLOAD_ENDPOINTS = ['transcribe', 'transcribeFull', 'transcribeIncremental', 'probe']

describe('枚举：每个 /api/stt 音频上传端点都必须自己兜住容器', () => {
  const sources = [
    { file: 'stt.ts', src: strip(read('stt.ts')) },
    { file: 'stt-settings.ts', src: strip(read('stt-settings.ts')) },
  ]
  const endpoints = sources.flatMap(({ file, src }) => audioUploadEndpoints(file, src).map((e) => ({ ...e, file })))

  it('枚举本身是有效的（防「枚举器自己跑飞」——零命中与全覆盖同样危险）', () => {
    // ⚠ 这条是护栏：没有它，上面所有断言在枚举器写坏时都会「因为没有样本而全绿」。
    assert.ok(
      endpoints.length >= REGISTERED_UPLOAD_ENDPOINTS.length,
      `枚举只找到 ${endpoints.length} 个端点（${endpoints.map((e) => e.method).join(',')}），`
      + '枚举正则很可能失效了 —— 此刻下面三条断言是在零样本上「全绿」，不是真的通过',
    )
  })

  it('★ 枚举出的端点集合必须与登记表一致（新增入口必须登记，不能悄悄溜进来）', () => {
    const found = endpoints.map((e) => e.method).sort()
    const want = [...REGISTERED_UPLOAD_ENDPOINTS].sort()
    assert.deepEqual(
      found, want,
      `枚举出 [${found.join(', ')}]，登记表是 [${want.join(', ')}]。\n`
      + `  多出来 = 新增了音频上传入口但没纳入本门；\n`
      + `  少掉 = 有端点不再带 audioBase64（要么它变了，要么枚举正则失效了）。`,
    )
  })

  for (const e of endpoints) {
    it(`★ ${e.file}:${e.line} ${e.method} → ${e.route}：必须自己做容器适配`, () => {
      assert.match(
        e.body,
        /(ensureGatewayCompatible|toGatewayWavBytes)\s*\(/,
        `${e.method} 不做容器适配。判据共用、调用逐点：不加这一处，网关就会拿到`
        + `${e.route === '/api/stt/probe' ? '（§50/缺陷8 实测 400）' : ''}webm 字节并直接拒收。`,
      )
    })

    it(`★ ${e.method}：filename 必须由实际容器推导，不能是硬编码常量`, () => {
      assert.match(
        e.body,
        /filenameForMimeType\s*\(/,
        `${e.method} 的 filename 不是从实际容器推导的 —— 发 webm 字节却报 .wav，`
        + '后端「按扩展名选解码方式/能不能切分」的分支会与实际不符，且症状是'
        + '「录音有内容但一个字都转不出来」且**不报错**。',
      )
    })
  }
})