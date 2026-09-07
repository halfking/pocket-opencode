import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  classifyAudioLabel,
  louderChannelRms,
  preferredAudioInput,
  rankAudioInputs,
} from './audio-inputs.ts'

describe('rankAudioInputs', () => {
  it('prefers bluetooth over builtin mic', () => {
    const ranked = rankAudioInputs([
      { deviceId: 'a', label: 'Builtin Microphone', kind: 'audioinput' },
      { deviceId: 'b', label: 'WH-1000XM5 Bluetooth', kind: 'audioinput' },
      { deviceId: 'c', label: 'USB Audio Device', kind: 'audioinput' },
    ])
    assert.equal(ranked[0].deviceId, 'b')
    assert.equal(ranked[1].deviceId, 'c')
    assert.equal(ranked[2].deviceId, 'a')
    assert.equal(preferredAudioInput(ranked)?.kind, 'bluetooth')
  })

  it('classifies headset and skips empty default ids', () => {
    assert.equal(classifyAudioLabel('Wired headset'), 'headset')
    const ranked = rankAudioInputs([
      { deviceId: 'default', label: 'Default', kind: 'audioinput' },
      { deviceId: 'real', label: 'Headset earpiece', kind: 'audioinput' },
    ])
    assert.equal(ranked.length, 1)
    assert.equal(ranked[0].deviceId, 'real')
  })
})

describe('louderChannelRms', () => {
  it('picks the louder stereo channel', () => {
    const left = Float32Array.from([0.01, 0.02, 0.01])
    const right = Float32Array.from([0.4, -0.5, 0.3])
    assert.equal(louderChannelRms(left, right), 'right')
  })

  it('reports mono when there is no right channel', () => {
    assert.equal(louderChannelRms(Float32Array.from([0.1]), new Float32Array()), 'mono')
  })
})
