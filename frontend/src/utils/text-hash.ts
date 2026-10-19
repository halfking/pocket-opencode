/**
 * text-hash.ts — 纯 JS 的 SHA-1（同步、无环境依赖）。
 *
 * ⚠ 为什么不用 `crypto.subtle.digest`：
 *   · 它是**异步**的，且在浏览器里要求**安全上下文**（https / localhost）——
 *     Capacitor 的 `capacitor://` 源在部分平台上不保证是安全上下文 ⇒ 不可靠；
 *   · 本仓的判据在 node 里跑，`node:crypto` 在浏览器侧不可用 ⇒ 两边都要分叉。
 *   ⇒ 自己实现一份，两边行为完全一致。
 *
 * ⚠⚠ **SHA-1 在这里不是用来做密码学的**：
 *   用途只有「判断一段文本有没有变过」。它要的是**确定性**与**实现可核验**
 *   （判据能钉到公开测试向量上 ⇒ 哈希实现本身有外部真值，不靠自证）。
 *   ⇒ 绝不要拿它做签名/口令/去重安全相关的事。
 *
 * 公开测试向量（判据里钉住）：
 *   sha1('')   = da39a3ee5e6b4b0d3255bfef95601890afd80709
 *   sha1('abc')= a9993e364706816aba3e25717850c26c9cd0d89d
 */

/** UTF-8 编码（不依赖 TextEncoder，避免在个别环境缺失）。 */
function utf8Bytes(str: string): number[] {
  const out: number[] = []
  for (let i = 0; i < str.length; i++) {
    let c = str.charCodeAt(i)
    if (c < 0x80) {
      out.push(c)
    } else if (c < 0x800) {
      out.push(0xc0 | (c >> 6), 0x80 | (c & 0x3f))
    } else if (c >= 0xd800 && c <= 0xdbff && i + 1 < str.length) {
      // 代理对 → 4 字节 UTF-8
      const c2 = str.charCodeAt(i + 1)
      if (c2 >= 0xdc00 && c2 <= 0xdfff) {
        const cp = 0x10000 + ((c - 0xd800) << 10) + (c2 - 0xdc00)
        out.push(
          0xf0 | (cp >> 18),
          0x80 | ((cp >> 12) & 0x3f),
          0x80 | ((cp >> 6) & 0x3f),
          0x80 | (cp & 0x3f),
        )
        i++
        continue
      }
      out.push(0xef, 0xbf, 0xbd) // 落单的代理项 → U+FFFD
    } else if (c >= 0xd800 && c <= 0xdfff) {
      out.push(0xef, 0xbf, 0xbd)
    } else {
      out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f))
    }
  }
  return out
}

/** 文本的 SHA-1，小写十六进制。 */
export function sha1Hex(text: string): string {
  const bytes = utf8Bytes(text)
  const bitLen = bytes.length * 8
  // 追加 0x80，补 0 到 56 mod 64，再补 8 字节大端长度
  bytes.push(0x80)
  while (bytes.length % 64 !== 56) bytes.push(0)
  const hi = Math.floor(bitLen / 0x100000000)
  const lo = bitLen >>> 0
  bytes.push((hi >>> 24) & 0xff, (hi >>> 16) & 0xff, (hi >>> 8) & 0xff, hi & 0xff)
  bytes.push((lo >>> 24) & 0xff, (lo >>> 16) & 0xff, (lo >>> 8) & 0xff, lo & 0xff)

  let h0 = 0x67452301
  let h1 = 0xefcdab89
  let h2 = 0x98badcfe
  let h3 = 0x10325476
  let h4 = 0xc3d2e1f0
  const w = new Int32Array(80)

  for (let off = 0; off < bytes.length; off += 64) {
    for (let i = 0; i < 16; i++) {
      const j = off + i * 4
      w[i] = (bytes[j] << 24) | (bytes[j + 1] << 16) | (bytes[j + 2] << 8) | bytes[j + 3]
    }
    for (let i = 16; i < 80; i++) {
      const n = w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16]
      w[i] = (n << 1) | (n >>> 31)
    }
    let a = h0
    let b = h1
    let c = h2
    let d = h3
    let e = h4
    for (let i = 0; i < 80; i++) {
      let f: number
      let k: number
      if (i < 20) {
        f = (b & c) | (~b & d)
        k = 0x5a827999
      } else if (i < 40) {
        f = b ^ c ^ d
        k = 0x6ed9eba1
      } else if (i < 60) {
        f = (b & c) | (b & d) | (c & d)
        k = 0x8f1bbcdc
      } else {
        f = b ^ c ^ d
        k = 0xca62c1d6
      }
      const tmp = (((a << 5) | (a >>> 27)) + f + e + k + w[i]) | 0
      e = d
      d = c
      c = (b << 30) | (b >>> 2)
      b = a
      a = tmp
    }
    h0 = (h0 + a) | 0
    h1 = (h1 + b) | 0
    h2 = (h2 + c) | 0
    h3 = (h3 + d) | 0
    h4 = (h4 + e) | 0
  }

  return [h0, h1, h2, h3, h4]
    .map((h) => (h >>> 0).toString(16).padStart(8, '0'))
    .join('')
}
