/**
 * 邮件正文字符集解码（2026-10-01 真机审计 P0：正文乱码）。
 *
 * 乱码的根因不是字体，而是**解码那一层就选错了编码**。国内企业邮箱/老系统
 * 至今仍在用 GBK/GB2312/GB18030 发信，声明形如：
 *
 *   Content-Type: text/html; charset=GB2312
 *   Content-Transfer-Encoding: base64
 *
 * 此前 email-body-format 里的 base64 / quoted-printable 解码一律写死
 * `new TextDecoder('utf-8')`。GBK 字节被当成 UTF-8 解，结果是典型的
 * 「锟斤拷」/「烫烫烫」——字符已经被替换成 U+FFFD，后面再怎么换字体都救不回来。
 *
 * 这里按 MIME 头里声明的 charset 解码，并处理三类现实情况：
 *   1. 标签别名：gb2312 / gbk / gb18030 / x-gbk / gb_2312-80 …
 *   2. 服务端谎报：声明 UTF-8 实际是 GBK（老系统极常见），需要探测。
 *   3. 8bit/二进制：本身就带 charset，必须按声明解。
 *
 * 说明：TextDecoder 对 gb18030 / gbk / big5 等编码的支持依赖运行时的 ICU。
 * 浏览器与 Android WebView 均内置完整 ICU；Node 18+ 亦然。若某编码不被支持，
 * `createDecoder` 会抛 RangeError，这里统一回退到 UTF-8（最坏也只是回到旧行为，
 * 不会让整封邮件解析失败）。
 */

/** MIME charset 标签 → TextDecoder 标准编码名。 */
const CHARSET_ALIASES: Record<string, string> = {
  // UTF-8 系
  'utf-8': 'utf-8',
  'utf8': 'utf-8',
  'unicode-1-1-utf-8': 'utf-8',
  'us-ascii': 'utf-8',
  'ascii': 'utf-8',
  'iso-8859-1': 'windows-1252', // 邮件里 ISO-8859-1 实际按 cp1252 解释才对得上
  'latin1': 'windows-1252',
  'iso8859-1': 'windows-1252',
  // GBK 系（中文邮件主体）
  'gbk': 'gbk',
  'gb2312': 'gbk', // GB2312 是 GBK 子集，用 GBK 解码器可兼容 GBK 扩展字
  'gb_2312-80': 'gbk',
  'gb18030': 'gb18030',
  'x-gbk': 'gbk',
  'csgb2312': 'gbk',
  'chinese': 'gbk',
  'gb_2312': 'gbk',
  'euccn': 'gbk',
  'windows-936': 'gbk',
  'cp936': 'gbk',
  'ms936': 'gbk',
  // Big5 / Big5-HKSCS（繁体）
  'big5': 'big5',
  'big5-hkscs': 'big5-hkscs',
  'cp950': 'big5',
  // 日韩
  'iso-2022-jp': 'iso-2022-jp',
  'euc-jp': 'euc-jp',
  'shift_jis': 'shift-jis',
  'sjis': 'shift-jis',
  'euc-kr': 'euc-kr',
  'ks_c_5601-1987': 'euc-kr',
  // 西里尔 / 中东（RFC 2047 里偶尔出现）
  'koi8-r': 'koi8-r',
  'koi8-u': 'koi8-u',
  'windows-1251': 'windows-1251',
  'windows-1250': 'windows-1250',
  'windows-1255': 'windows-1255',
}

/** 归一化 charset 标签：去引号、去空白、转小写。 */
export function normalizeCharset(raw: string | null | undefined): string {
  return (raw || '').trim().replace(/^["']|["']$/g, '').toLowerCase()
}

/** 解析出的标准编码名；未知标签返回 ''。 */
export function resolveCharsetEncoding(raw: string | null | undefined): string {
  const key = normalizeCharset(raw)
  if (!key) return ''
  return CHARSET_ALIASES[key] || ''
}

/**
 * 该标签是否指向非 UTF-8 的历史编码（决定要不要额外探测）。
 */
export function isLegacyCharset(raw: string | null | undefined): boolean {
  const enc = resolveCharsetEncoding(raw)
  return !!enc && enc !== 'utf-8'
}

/**
 * 取得一个 TextDecoder；标签未知或运行时不支持该编码时回退 UTF-8。
 * 绝不抛错——正文解析不能因为一个冷门 charset 而整封失败。
 */
function decoderFor(encoding: string): TextDecoder {
  try {
    return new TextDecoder(encoding)
  } catch {
    return new TextDecoder('utf-8')
  }
}

/**
 * 声明 UTF-8 但内容其实是 GBK 的探测（老系统常见谎报）。
 *
 * 判据：合法 UTF-8 序列里绝不该出现 U+FFFD；一旦出现，且按 GBK 解码后不再有
 * U+FFFD，就说明原字节根本不是 UTF-8。这里只在「声明 UTF-8」时启用，
 * 避免对真正的 UTF-8 正文误判。
 */
export function looksLikeMisdeclaredUtf8(s: string): boolean {
  if (!s.includes('\uFFFD')) return false
  // 真正的 UTF-8 正文里出现 U+FFFD 极罕见；连片出现更像编码错配。
  return (s.match(/\uFFFD/g) || []).length >= 2 || s.length < 400
}

const HAN = /[\u4e00-\u9fff]/g
function countHan(s: string): number {
  return (s.match(HAN) || []).length
}

/** 打分：替换字符越少越好；同样「脏」时汉字更多的更像真实中文正文。 */
function quality(s: string): number {
  const bad = (s.match(/\uFFFD/g) || []).length
  // 每 100 个替换字符扣 1000 分；汉字提供轻微正向信号。
  return -bad * 1000 + Math.min(countHan(s), 200)
}

/** 声明 UTF-8/未声明时，额外试这几个「国内邮件最常见谎报」编码。 */
const FALLBACK_ENCODINGS = ['gb18030', 'gbk', 'big5']

/**
 * 按声明 charset 解码**原始字节**，并在「声明 UTF-8 但内容其实是历史编码」时
 * 自动纠正。
 *
 * 必须在**字节层面**做纠正：一旦按错误的编码解过一遍，原始字节就永久丢失了
 * （U+FFFD 是替换而非映射，无法反解回 GBK）。所以本函数是正文解码的唯一入口，
 * 调用方必须把未解码的字节直接传进来。
 *
 * 老系统谎报 charset 的两种典型：
 *   - `charset=utf-8` 实际 GBK  → 解出满屏 U+FFFD，改用 GBK 立刻通顺；
 *   - 完全不声明 charset      → 同上。
 */
export function decodePartBytes(bytes: Uint8Array, declared: string | null | undefined): string {
  const enc = resolveCharsetEncoding(declared) || 'utf-8'
  const primary = decoderFor(enc).decode(bytes)

  // 声明的是历史编码就认它，不做二次猜测（GBK 正文按 big5 解可能"也像中文"，
  // 乱猜反而会破坏正确结果）。
  if (enc !== 'utf-8') return primary
  if (!looksLikeMisdeclaredUtf8(primary)) return primary

  let best = primary
  let bestScore = quality(primary)
  for (const cand of FALLBACK_ENCODINGS) {
    const decoded = decoderFor(cand).decode(bytes)
    const score = quality(decoded)
    if (score > bestScore) {
      best = decoded
      bestScore = score
    }
  }
  return best
}
