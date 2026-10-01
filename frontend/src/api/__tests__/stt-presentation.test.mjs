/**
 * STT 设置页展示辅助的回归测试（2026-10-01）。
 *
 * 最要紧的一条是 formatCost：最便宜档是 $0.012/小时，
 * 若用 toFixed(2) 会显示成 $0.01，把「1 分钱档」和「0.5 分钱档」压成同一个价，
 * 用户无法比较——而「尽可能费用少」正是这个字段存在的唯一理由。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

const { formatCost, streamingHint, maxSecondsHint } = await import('../stt-presentation.ts')

describe('formatCost', () => {
  it('1 分钱档显示 3 位小数，不会被四舍五入成 $0.01', () => {
    // OpenRouter qwen3-asr-0.6b ≈ $0.012/小时
    assert.equal(formatCost(0.012), '$0.012/小时')
  })

  it('两个 1 分钱档之间仍能区分（0.012 与 0.027）', () => {
    assert.notEqual(formatCost(0.012), formatCost(0.027))
  })

  it('主流价格档用 2 位小数', () => {
    assert.equal(formatCost(0.38), '$0.38/小时')  // MiniMax
    assert.equal(formatCost(0.5), '$0.50/小时')   // 智谱
    assert.equal(formatCost(0.18), '$0.18/小时')  // gpt-4o-mini-transcribe
    assert.equal(formatCost(0.36), '$0.36/小时')  // gpt-4o-transcribe
  })

  it('更便宜的档位不显示成 $0.00', () => {
    assert.match(formatCost(0.0086), /^\$0\.0086\/小时$/)
    assert.notEqual(formatCost(0.0086), '$0.00/小时')
  })

  it('0 / 负数 / NaN 返回空串 —— 显示 $0.00 会被读成「免费」', () => {
    assert.equal(formatCost(0), '')
    assert.equal(formatCost(-1), '')
    assert.equal(formatCost(NaN), '')
    assert.equal(formatCost(Infinity), '')
  })
})

describe('streamingHint', () => {
  it('支持流式的模型给出正向说明', () => {
    assert.equal(streamingHint({ streaming: true, group: 'external' }), '支持即时流式出字')
  })

  it('不支持流式的外部模型要明说，避免用户选完才发现不是逐字', () => {
    // OpenRouter 全部不支持（上游约 60 秒超时）
    assert.equal(
      streamingHint({ streaming: false, group: 'external' }),
      '分段出字（不支持逐字流式）',
    )
  })

  it('网关候选不提示流式（网关尚无任何 ASR 上游）', () => {
    assert.equal(streamingHint({ group: 'gateway' }), '')
  })
})

describe('maxSecondsHint', () => {
  it('说明超限会自动切段，而不是让用户以为功能不可用', () => {
    assert.equal(maxSecondsHint(30), '单次上限 30 秒，超过会自动切段转写')
    assert.equal(maxSecondsHint(500), '单次上限 500 秒，超过会自动切段转写')
  })

  it('未知上限不显示（0/空/负数）', () => {
    assert.equal(maxSecondsHint(0), '')
    assert.equal(maxSecondsHint(undefined), '')
    assert.equal(maxSecondsHint(-5), '')
  })
})

describe('设置页接入了展示辅助（源码级）', () => {
  const vue = read('../../features/settings/SettingsSTT.vue')

  it('不再用 toFixed(2) 直接渲染单价', () => {
    // 若有人把 formatCost 改回内联 toFixed(2)，这里会红
    assert.equal(
      /usdPerHour\s*\}\}\/小时|toFixed\(2\)\}\}\/小时/.test(vue),
      false,
      '单价必须走 formatCost，否则低价档会被四舍五入成 $0.01',
    )
  })

  it('复用了 formatCost / streamingHint / maxSecondsHint', () => {
    for (const fn of ['formatCost', 'streamingHint', 'maxSecondsHint']) {
      assert.ok(vue.includes(fn), `SettingsSTT.vue 未使用 ${fn}`)
    }
  })
})
