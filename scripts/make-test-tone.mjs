#!/usr/bin/env node
/**
 * 生成录音测试用的 WAV（人声近似：基频 + 共振峰包络 + 音节节律）。
 *
 * 用途：真机验证「笔记录音 → 停止 → 转文字」链路时，系统麦克风需要有声音可拾取。
 * 纯正弦波容易被 VAD 判为噪声，因此这里用 3 个共振峰近似浊音，
 * 并按 ~4.5 音节/秒做幅度调制，让 VAD 更可能判定为语音。
 *
 * 用法: node scripts/make-test-tone.mjs out.wav [seconds]
 */
import { writeFileSync } from 'node:fs'

const out = process.argv[2] || 'test-tone.wav'
const seconds = Number(process.argv[3] || 8)
const rate = 16000

// 基频随时间缓慢起伏，模拟说话音高
const f0 = (t) => 120 + 25 * Math.sin(2 * Math.PI * 0.35 * t)
// 三个共振峰（近似 /a/ 元音）
const formants = [730, 1090, 2440]
const fmtGain = [1.0, 0.5, 0.25]

const samples = new Int16Array(rate * seconds)
for (let i = 0; i < samples.length; i++) {
  const t = i / rate
  // 音节包络：4.5Hz 音节率，占空比约 0.55
  const syl = Math.sin(2 * Math.PI * 4.5 * t)
  const env = syl > 0 ? Math.pow(syl, 0.7) : 0
  // 词间停顿
  const phrase = Math.sin(2 * Math.PI * 0.45 * t) > -0.75 ? 1 : 0
  let v = 0
  const base = f0(t)
  for (let h = 1; h <= 12; h++) {
    const freq = base * h
    if (freq > rate / 2 - 500) break
    let g = 0
    for (let k = 0; k < formants.length; k++) {
      // 共振峰带宽约 120Hz
      g += fmtGain[k] * Math.exp(-Math.pow((freq - formants[k]) / 120, 2))
    }
    v += (g / h) * Math.sin(2 * Math.PI * freq * t)
  }
  samples[i] = Math.max(-32767, Math.min(32767, Math.round(v * env * phrase * 0.42 * 32767)))
}

const header = Buffer.alloc(44)
const dataBytes = samples.length * 2
header.write('RIFF', 0)
header.writeUInt32LE(36 + dataBytes, 4)
header.write('WAVE', 8)
header.write('fmt ', 12)
header.writeUInt32LE(16, 16)      // PCM chunk size
header.writeUInt16LE(1, 20)       // PCM
header.writeUInt16LE(1, 22)       // mono
header.writeUInt32LE(rate, 24)
header.writeUInt32LE(rate * 2, 28) // byte rate
header.writeUInt16LE(2, 32)       // block align
header.writeUInt16LE(16, 34)      // bits per sample
header.write('data', 36)
header.writeUInt32LE(dataBytes, 40)

writeFileSync(out, Buffer.concat([header, Buffer.from(samples.buffer)]))
console.log(`${out}  ${seconds}s  ${rate}Hz mono 16bit  ${44 + dataBytes} bytes`)
