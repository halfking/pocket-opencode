/**
 * shell/capabilities.ts — 能力协商。
 *
 * 必须区分两个维度（UI规范 08 §1）：
 *   - `hyperMode` 是**交互形态**开关（compact / 在壳内）；
 *   - `capabilities` 才证明**原生能力**是否可用。
 *
 * 存在 `window.Capacitor`、在壳内、compact 宽度——都**不**证明可后台录音、
 * 可 OCR、可持续后台执行。因此每个原生能力逐项检测，缺就报 false，
 * 由 UI 决定是否渲染，而不是「先渲染再说」。
 *
 * ⚠️ 与 `src/native/capabilities.ts` 的边界：本模块管 **Hyper 运行时**的
 * 能力协商（protocolVersion / continuation / agent / skillFormat）；
 * 那个模块管**原生基元**（biometric / keystore / push / network，扁平 boolean，
 * 受 feature flag 与 HarmonyOS clamp 裁剪）。形状与消费者都不同，**故意不合并**。
 *
 * ⚠️ 现状（2026-10-04 实测）：**本模块目前没有任何 UI 消费者**，因此不进
 * bundle ——「已实现且已测」不等于「已上线」。接线前先确认目标页面属于
 * Hyper 层，否则该用 `native/capabilities.ts`。
 *
 * ⚠️ 绝不能把 Provider API Key、refresh token 放进本模块或任何客户端配置。
 * 云端任务由既有登录 API 提交；原生如需后台上传，Go 侧签发短期、
 * 任务范围、账号绑定的 upload capability，本模块只持有引用。
 */

import type { HyperCapabilities } from './types.ts'

/** 探针接口：真实实现去问原生；测试里注入假探针。 */
export interface CapabilityProbes {
  /** 是否在 Capacitor 原生壳内。 */
  inShell(): boolean
  /** 平台标识。 */
  platform(): 'web' | 'ios' | 'android'
  /** 原生插件是否真的注册成功——只看 import 存在是不够的。 */
  pluginAvailable(name: string): boolean
}

/** 能力 → 原生插件名。集中登记，避免探测逻辑里散落字符串。 */
export const CAPABILITY_PLUGINS = {
  /** 后台录音（Android microphone 前台服务）。 */
  recording: ['BackgroundMic'],
  /** 本地识别（sherpa ASR / PDF 文本）。 */
  recognition: ['Sherpa'],
  /** 本地持久任务账本。⚠️ **当前仓内没有这个插件**，所以它恒为 false。 */
  taskLedger: ['TaskLedger'],
  /** 内置轻量 Agent 运行时。⚠️ **当前仓内没有这个插件**，所以它恒为 false。 */
  agent: ['LocalAgent'],
  /** AI 流保活。 */
  aiStream: ['AiStreamKeepalive'],
} as const

/**
 * 默认探针。全部基于运行时事实，不基于构建期常量。
 *
 * `pluginAvailable` 用 `Capacitor.isPluginAvailable()` —— 它查的是**运行时
 * 插件注册表**，即真正在 JS 侧注册过、且原生侧有实现的插件。
 * 这比「import 存在」严格：Capacitor 包装了不等于插件实现了
 * （本仓就有 8 个自研插件，其中 `TaskLedger`/`LocalAgent` **根本不存在**）。
 *
 * ⚠️ 探不到就是 false。`isPluginAvailable` 不会因为「原生实现类存在但没注册」
 * 而返回 true——那正是我们要保守的原因：声称有而实际 start() 会 reject，
 * 比声称没有更难排查。
 */
export function defaultProbes(): CapabilityProbes {
  const cap = (globalThis as {
    Capacitor?: {
      isNativePlatform?: () => boolean
      getPlatform?: () => string
      isPluginAvailable?: (name: string) => boolean
    }
  }).Capacitor
  return {
    inShell: () => {
      try {
        return cap?.isNativePlatform?.() === true
      } catch {
        return false
      }
    },
    platform: () => {
      try {
        const p = cap?.getPlatform?.()
        return p === 'ios' || p === 'android' ? p : 'web'
      } catch {
        return 'web'
      }
    },
    pluginAvailable: (name) => {
      try {
        // 显式要求在壳内：在浏览器里 isPluginAvailable 对同名 shim 可能返回 true，
        // 但那不代表有原生实现（例如 web shim）。
        if (cap?.isNativePlatform?.() !== true) return false
        return cap?.isPluginAvailable?.(name) === true
      } catch {
        return false
      }
    },
  }
}

/** 能力清单里任意一个插件可用即为真。 */
function anyPlugin(probes: CapabilityProbes, names: readonly string[]): boolean {
  return names.some((n) => probes.pluginAvailable(n))
}

/**
 * 探测并返回能力快照。
 *
 * 所有字段都**必须**真的问过探针；任何一项拿不到证据就报 false / 'none'。
 * 「不确定」不等于「可能可以」——这是本模块存在的全部理由。
 */
export function detectCapabilities(
  probes: CapabilityProbes = defaultProbes(),
  opts: { continuousScroll?: boolean } = {},
): HyperCapabilities {
  const inShell = probes.inShell()
  const platform = probes.platform()

  const recording = anyPlugin(probes, CAPABILITY_PLUGINS.recording)
  const recognition = anyPlugin(probes, CAPABILITY_PLUGINS.recognition)
  const agent = anyPlugin(probes, CAPABILITY_PLUGINS.agent)
  const taskLedger = anyPlugin(probes, CAPABILITY_PLUGINS.taskLedger)

  return {
    protocolVersion: 2,
    platform,
    // 导航/专注是 Web 侧运行时，不依赖原生；连续加载由形态开关决定。
    navigation: true,
    focusWorkspace: true,
    continuousScroll: opts.continuousScroll ?? true,
    tasks: {
      // 本地持久任务需要原生任务账本。未装插件时诚实报 false。
      durableLocal: taskLedger,
      // 云端脱离页面：只要有登录 API + 任务端点就成立，与原生无关。
      cloudDetached: true,
      // 未装原生任务调度时，只能前台执行。
      continuation: taskLedger ? 'osScheduled' : 'foregroundOnly',
    },
    recording: {
      available: recording,
      // 后台录音需要前台服务/background audio 能力，与「能录音」是两件事。
      background: recording && inShell,
    },
    recognition: {
      // PDF 文本提取可以纯 Web 做（PDF.js Worker），不依赖原生插件。
      pdfText: true,
      ocr: recognition ? 'model' : 'none',
      asr: recognition ? 'model' : 'none',
    },
    agent: {
      available: agent,
      skillFormat: agent ? 'declarative-v1' : 'none',
    },
  }
}

/**
 * 能力门：渲染原生相关 UI 前先问它。
 *
 * 存在的意义是让「不可用」成为一条**可读的路径**，而不是散落在各个组件里的
 * `if (inShell())`。返回值不是布尔而是原因，便于 UI 给出对应提示文案。
 */
export type CapabilityResult =
  | { ok: true }
  | { ok: false; reason: 'unsupported' | 'denied' | 'unavailable'; detail: string }

export function requireCapability(
  caps: HyperCapabilities,
  need: 'recording' | 'recognition' | 'agent' | 'durableTasks',
): CapabilityResult {
  switch (need) {
    case 'recording':
      if (!caps.recording.available) {
        return {
          ok: false,
          reason: 'unsupported',
          detail: '当前宿主没有录音插件；浏览器窄屏不等于可录音。',
        }
      }
      return { ok: true }
    case 'recognition':
      if (caps.recognition.ocr === 'none' && caps.recognition.asr === 'none') {
        return { ok: false, reason: 'unsupported', detail: '当前宿主没有本地识别引擎。' }
      }
      return { ok: true }
    case 'agent':
      if (!caps.agent.available) {
        return { ok: false, reason: 'unsupported', detail: '当前宿主没有内置 Agent 运行时。' }
      }
      return { ok: true }
    case 'durableTasks':
      if (!caps.tasks.durableLocal) {
        return {
          ok: false,
          reason: 'unavailable',
          detail: `本地持久任务不可用；当前延续策略为 ${caps.tasks.continuation}。`,
        }
      }
      return { ok: true }
  }
}
