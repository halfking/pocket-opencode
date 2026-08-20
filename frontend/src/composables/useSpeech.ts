/**
 * useSpeech — AI 回复朗读（P2 可选 TTS，规格 2026-07-24「AI 响应语音播放」）。
 *
 * 双引擎：
 * - 原生（Capacitor Android/iOS）：@capacitor-community/text-to-speech
 *   （架构文档 MOBILE_ARCHITECTURE_V2.md §723 指定插件）。Android WebView
 *   实测没有 speechSynthesis（API 30 'speechSynthesis' in window === false），
 *   必须走原生引擎。
 * - Web：浏览器 speechSynthesis（Web Speech API）。
 *
 * 两引擎都不支持时 supported=false，按钮隐藏。
 * 纯函数（pickVoice / stripMarkdownForSpeech / splitForSpeech）抽出供 node 测试。
 */
import { ref, onBeforeUnmount } from 'vue'
import { Capacitor } from '@capacitor/core'

/** 从可用声音中挑朗读声音：优先 preferLang 前缀（zh-CN），再退回 null（用系统默认）。 */
export function pickVoice(
  voices: { lang: string; localService?: boolean; name?: string }[],
  preferLang = 'zh',
): { lang: string; localService?: boolean; name?: string } | null {
  const matched = voices.filter((v) => v.lang?.toLowerCase().startsWith(preferLang.toLowerCase()))
  if (matched.length === 0) return null
  // 优先本地服务声音（离线、延迟低）
  const local = matched.find((v) => v.localService)
  return local ?? matched[0]
}

/**
 * 朗读前的 Markdown 清洗：去掉代码块/行内代码围栏、链接语法、标题/强调标记，
 * 保留可读文本。代码块整体替换为「（代码略）」避免逐字符朗读符号。
 */
export function stripMarkdownForSpeech(text: string): string {
  return text
    // 围栏代码块（含 ```lang 开头）
    .replace(/```[\s\S]*?```/g, '（代码略）')
    // 行内代码 → 去反引号保留内容
    .replace(/`([^`]+)`/g, '$1')
    // 链接 [text](url) → text；裸 URL → （链接略）
    .replace(/\[([^\]]*)\]\(([^)]+)\)/g, '$1')
    .replace(/https?:\/\/\S+/g, '（链接略）')
    // 图片 ![alt](url) → 已被上面链接规则处理 alt，再兜底删残余 ![]
    .replace(/!\[([^\]]*)\]/g, '$1')
    // 标题/引用/列表标记
    .replace(/^#{1,6}\s+/gm, '')
    .replace(/^>\s?/gm, '')
    .replace(/^(\s*)[-*+]\s+/gm, '$1')
    // 粗斜体与删除线标记
    .replace(/(\*\*|__|\*|_|~~)/g, '')
    // 表格分隔线与多余空行
    .replace(/^\|?[-:| ]+\|$/gm, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

/**
 * Web speechSynthesis 单次合成的长度上限（部分内核超长文本会静默截断，
 * 按句切分串行朗读更稳）。原生引擎自行处理长文本，不走此切分。
 */
export function splitForSpeech(text: string, maxLen = 1200): string[] {
  if (text.length <= maxLen) return [text]
  const parts: string[] = []
  let rest = text
  while (rest.length > maxLen) {
    // 在上限内找最后一个句末标点
    const slice = rest.slice(0, maxLen)
    let cut = Math.max(slice.lastIndexOf('。'), slice.lastIndexOf('？'), slice.lastIndexOf('！'), slice.lastIndexOf('. '))
    if (cut < maxLen * 0.5) cut = maxLen // 找不到合适断点就硬切
    parts.push(rest.slice(0, cut + 1))
    rest = rest.slice(cut + 1)
  }
  if (rest.trim()) parts.push(rest)
  return parts
}

export function useSpeech() {
  const nativeTTS = Capacitor.isNativePlatform()
  const webTTS =
    typeof window !== 'undefined' && 'speechSynthesis' in window && typeof SpeechSynthesisUtterance !== 'undefined'
  const supported = nativeTTS || webTTS
  /** 当前正在朗读的消息 id（同一时间只朗读一条） */
  const speakingId = ref<string | null>(null)
  let queue: string[] = []
  let currentId: string | null = null

  function clear() {
    queue = []
    currentId = null
    speakingId.value = null
  }

  async function stop() {
    clear()
    if (!supported) return
    try {
      if (nativeTTS) {
        const { TextToSpeech } = await import('@capacitor-community/text-to-speech')
        await TextToSpeech.stop()
      } else {
        window.speechSynthesis.cancel()
      }
    } catch {
      // 引擎不在（如模拟器无 TTS 数据）时静默
    }
  }

  function speakNextWeb() {
    if (!webTTS || queue.length === 0) {
      clear()
      return
    }
    const text = queue.shift()!
    const utter = new SpeechSynthesisUtterance(text)
    utter.lang = 'zh-CN'
    // 与 utter.lang 对齐用 zh-CN 精确前缀挑选，避免 zh-TW 声音被误选
    const voice = pickVoice(window.speechSynthesis.getVoices(), 'zh-CN')
    if (voice) utter.voice = voice as SpeechSynthesisVoice
    utter.rate = 1
    utter.onend = () => {
      if (currentId !== null && queue.length > 0) speakNextWeb()
      else clear()
    }
    utter.onerror = () => clear()
    window.speechSynthesis.speak(utter)
  }

  async function speakNative(text: string) {
    const { TextToSpeech } = await import('@capacitor-community/text-to-speech')
    // v8 无朗读结束事件（仅 onRangeStart）：按钮保持「停止朗读」直到用户点停
    await TextToSpeech.speak({ text, lang: 'zh-CN', rate: 1.0 })
  }

  /** 朗读一条消息文本；再次点击同一条会停止。 */
  async function speak(id: string, markdownText: string) {
    if (!supported) return
    if (currentId === id) {
      await stop()
      return
    }
    await stop() // 换条朗读：先停旧的（不等其结束回调）
    const clean = stripMarkdownForSpeech(markdownText)
    if (!clean) return
    currentId = id
    speakingId.value = id
    try {
      if (nativeTTS) {
        await speakNative(clean)
      } else {
        queue = splitForSpeech(clean)
        speakNextWeb()
      }
    } catch {
      // 无 TTS 引擎/权限异常：复位按钮，不影响会话功能
      clear()
    }
  }

  onBeforeUnmount(() => {
    void stop()
  })

  return { supported, speakingId, speak, stop }
}
