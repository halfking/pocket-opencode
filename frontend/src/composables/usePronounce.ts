/**
 * usePronounce — 英语单词/例句朗读（内置学习库用）。
 *
 * 与 useSpeech 的区别只有两处，但都必须独立：
 *  1. 语言是 en-US，不是 zh-CN。英语卡片的音标是 IPA，念错语言等于没发音。
 *  2. 一次只念一条，重复点击同一条即停止（学习场景要能反复听同一个词）。
 *
 * 引擎选择沿用仓库既有结论：Android WebView 没有 window.speechSynthesis，
 * 必须走 @capacitor-community/text-to-speech；Web 端退回浏览器 API。
 */
import { ref, onBeforeUnmount } from 'vue'
import { Capacitor } from '@capacitor/core'

export type PronounceLang = 'en-US' | 'en-GB'

export function usePronounce(defaultLang: PronounceLang = 'en-US') {
  const nativeTTS = Capacitor.isNativePlatform()
  const webTTS =
    typeof window !== 'undefined' && 'speechSynthesis' in window && typeof SpeechSynthesisUtterance !== 'undefined'
  const supported = nativeTTS || webTTS

  /** 正在朗读的卡片 id；null 表示当前没有朗读。 */
  const speakingId = ref<string | null>(null)
  const errorMsg = ref('')
  let currentId: string | null = null

  function reset() {
    currentId = null
    speakingId.value = null
  }

  async function stop() {
    reset()
    if (!supported) return
    try {
      if (nativeTTS) {
        const { TextToSpeech } = await import('@capacitor-community/text-to-speech')
        await TextToSpeech.stop()
      } else {
        window.speechSynthesis.cancel()
      }
    } catch {
      // 引擎缺失（模拟器无 TTS 数据）时静默
    }
  }

  async function speakNative(text: string, lang: PronounceLang) {
    const { TextToSpeech } = await import('@capacitor-community/text-to-speech')
    await TextToSpeech.speak({ text, lang, rate: 0.9 })
  }

  /** 朗读一条英文文本；同一条再次点击即停止。 */
  async function speak(id: string, text: string, lang: PronounceLang = defaultLang) {
    if (!supported || !text.trim()) return
    errorMsg.value = ''
    if (currentId === id) {
      await stop()
      return
    }
    await stop()
    currentId = id
    speakingId.value = id
    try {
      if (nativeTTS) {
        await speakNative(text, lang)
        // 原生插件 v8 没有"朗读结束"事件，结束后无法自动复位按钮；
        // 保留 speakingId 让用户可以点第二次停止。
      } else {
        const utter = new SpeechSynthesisUtterance(text)
        utter.lang = lang
        utter.rate = 0.9
        utter.onend = () => reset()
        utter.onerror = () => {
          errorMsg.value = '朗读失败：设备没有可用的英语语音'
          reset()
        }
        window.speechSynthesis.speak(utter)
      }
    } catch (e: any) {
      errorMsg.value = `朗读失败：${e?.message ?? e}`
      reset()
    }
  }

  onBeforeUnmount(() => {
    void stop()
  })

  return { supported, speakingId, errorMsg, speak, stop }
}

/**
 * 从卡片背面里抽出该朗读的英文文本。
 *
 * 内置英语牌组的背面统一是 `IPA: /…/ · 中文释义 · 例: "…"`。
 * 直接念整段会把音标和中文一起念出来，所以这里只取 `例:` 引号里的英文，
 * 卡片正面（单词/句子本身）则由调用方直接传。
 */
export function extractExampleSentence(back: string): string {
  const m = /例\s*[:：]\s*["“]([^"”]+)["”]/.exec(back || '')
  return m ? m[1].trim() : ''
}
