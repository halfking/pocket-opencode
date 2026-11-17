#!/usr/bin/env node
// generate-test-audio.mjs
// 生成用于自动化测试的音频文件

import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

/**
 * 生成 WAV 格式的测试音频
 * @param {string} outputPath - 输出文件路径
 * @param {number} durationSec - 时长（秒）
 * @param {number} frequency - 频率 (Hz)
 * @param {number} sampleRate - 采样率
 */
function generateTestAudio(outputPath, durationSec = 5, frequency = 440, sampleRate = 16000) {
  const numSamples = sampleRate * durationSec
  const amplitude = 32767 * 0.5 // 50% 振幅避免削波
  
  // 生成 PCM 数据（16-bit signed little-endian）
  const pcmData = new Int16Array(numSamples)
  for (let i = 0; i < numSamples; i++) {
    const t = i / sampleRate
    pcmData[i] = Math.floor(amplitude * Math.sin(2 * Math.PI * frequency * t))
  }
  
  // 构建 WAV 头
  const dataSize = numSamples * 2
  const fileSize = 44 + dataSize
  
  const wavHeader = new ArrayBuffer(44)
  const view = new DataView(wavHeader)
  
  // RIFF header
  view.setUint32(0, 0x52494646, false)  // "RIFF"
  view.setUint32(4, fileSize - 8, true)  // File size - 8
  view.setUint32(8, 0x57415645, false)  // "WAVE"
  
  // fmt chunk
  view.setUint32(12, 0x666d7420, false) // "fmt "
  view.setUint32(16, 16, true)          // fmt chunk size
  view.setUint16(20, 1, true)           // PCM format
  view.setUint16(22, 1, true)           // Mono
  view.setUint32(24, sampleRate, true)  // Sample rate
  view.setUint32(28, sampleRate * 2, true) // Byte rate
  view.setUint16(32, 2, true)           // Block align
  view.setUint16(34, 16, true)          // Bits per sample
  
  // data chunk
  view.setUint32(36, 0x64617461, false) // "data"
  view.setUint32(40, dataSize, true)    // Data size
  
  // 合并头和数据
  const wavFile = new Uint8Array(fileSize)
  wavFile.set(new Uint8Array(wavHeader), 0)
  wavFile.set(new Uint8Array(pcmData.buffer), 44)
  
  writeFileSync(outputPath, Buffer.from(wavFile))
  
  console.log(`✓ 生成测试音频:`)
  console.log(`  文件: ${outputPath}`)
  console.log(`  时长: ${durationSec}s`)
  console.log(`  频率: ${frequency}Hz`)
  console.log(`  采样率: ${sampleRate}Hz`)
  console.log(`  大小: ${(fileSize / 1024).toFixed(2)} KB`)
}

// 命令行使用
if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2)
  const outputPath = args[0] || resolve(process.cwd(), 'scripts/__tests__/test-audio-5s.wav')
  const duration = parseInt(args[1]) || 5
  const frequency = parseInt(args[2]) || 440
  
  generateTestAudio(outputPath, duration, frequency)
}

export { generateTestAudio }
