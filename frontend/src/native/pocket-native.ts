/**
 * PocketNative —— 跨端硬件能力抽象（Phase 7 iOS 镜像的 TypeScript 侧契约）。
 *
 * 设计动机：
 *   - 业务代码只 import `getPocketNative()`，**不直接 import Capacitor 或任何
 *     平台特定的 plugin**。
 *   - Android: 通过 Capacitor Bridge → 现有 Java plugin（已有 6 个）。
 *   - iOS:     通过 Capacitor Bridge → 镜像 Swift plugin（Phase 7 增量，
 *              AppSettings / Biometric / BackgroundMic / AiStream / Sherpa）。
 *   - Web:     走 Web API fallback（MediaRecorder / getUserMedia / IndexedDB）。
 *
 * 与 `capabilities.ts` 的区别：
 *   - `capabilities.ts`：历史 PR14 边界（capability flag 检测）。
 *   - `pocket-native.ts`（本文件）：Phase 7 统一接口（业务调用入口）。
 *   - 两者不冲突；本文件建立在 capabilities 之上。
 *
 * Phase 7 落地状态：
 *   - Android：createAndroidNative() 复用现有 Java plugins（已实现）。
 *   - iOS：    createIosNative() 提供 stub，需在 Mac + Xcode 上编译镜像 Swift plugin
 *              后接通（Phase 7.1，harness 之外的工作）。
 *   - Web:     createWebNative() 用 MediaRecorder / IndexedDB 兜底。
 */
import { Capacitor, registerPlugin } from '@capacitor/core'
import type { RuntimePlatform } from './runtime-platform'

export type PocketPlatform = 'android' | 'ios' | 'web'

/* ===== 录音 ===== */
export interface RecorderState {
  sessionId: string
  isRecording: boolean
  durationMs: number
  peakDb?: number
}

export interface PocketRecorder {
  start(opts?: { sampleRate?: number; format?: 'aac' | 'wav' }): Promise<{ sessionId: string }>
  stop(sessionId: string): Promise<{ uri: string; durationMs: number }>
  pause(sessionId: string): Promise<void>
  resume(sessionId: string): Promise<void>
  onState(cb: (s: RecorderState) => void): () => void
}

/* ===== 相机 ===== */
export interface PocketCamera {
  capture(opts?: { quality?: number; facing?: 'front' | 'back' }): Promise<{ uri: string }>
  pickFromGallery(): Promise<{ uri: string } | null>
}

/* ===== 后台任务 ===== */
export interface PocketBackgroundTask {
  schedule(name: string, opts?: { intervalMs?: number }): Promise<void>
  cancel(name: string): Promise<void>
  listPending(): Promise<string[]>
}

/* ===== 智能体循环 ===== */
export interface PocketAgentRuntime {
  runLoop(opts: { sessionId: string; prompt: string }): Promise<void>
  abort(sessionId: string): Promise<void>
  onEvent(sessionId: string, cb: (event: unknown) => void): () => void
}

/* ===== 通知 ===== */
export interface PocketNotifier {
  show(opts: { title: string; body: string; id?: number; data?: unknown }): Promise<void>
  cancel(id: number): Promise<void>
  cancelAll(): Promise<void>
  requestPermission(): Promise<'granted' | 'denied' | 'prompt'>
}

/* ===== 生物识别 ===== */
export interface PocketBiometric {
  isAvailable(): Promise<{ available: boolean; biometryType?: 'fingerprint' | 'face' | 'iris' }>
  authenticate(opts: { reason: string; title?: string }): Promise<{ ok: boolean; error?: string }>
}

/* ===== 应用设置（打开系统权限页） ===== */
export interface PocketAppSettings {
  openAppDetails(name: 'microphone' | 'notifications' | 'camera' | 'photos'): Promise<boolean>
  checkPermission(name: 'microphone' | 'notifications' | 'camera' | 'photos'): Promise<'granted' | 'denied' | 'prompt'>
  requestPermission(name: 'microphone' | 'notifications' | 'camera' | 'photos'): Promise<'granted' | 'denied'>
}

/* ===== 文件系统 =====
 * Phase 9.1：把 @capacitor/filesystem 调用搬到 pocket-native 抽象。
 *  - Android → @capacitor/filesystem 兼容层（Directory.Data / Documents / Cache）
 *  - iOS     → Phase 7.1 接通 Swift plugin 后可用；当前 stub 抛错
 *  - Web     → IndexedDB 模拟（库 pocket-fs / object store files / key = `pocket:fs:<dir>:<path>`）
 *
 * 参数形式刻意走位置参数而非 opts 对象 —— flashcards 域所有调用点都是
 * `(path, base64, dir)` 的形态，位置参数 + TS 类型校验已经够用，避免每个
 * 调用点都包一层对象字面量。
 */
export interface PocketFilesystem {
  /** 写文件；data 为 base64 字符串（无 data: 前缀）。 */
  writeFile(path: string, data: string, directory?: 'data' | 'cache' | 'documents'): Promise<void>
  /** 读文件；返回 base64 字符串（无 data: 前缀）；不存在抛错。 */
  readFile(path: string, directory?: 'data' | 'cache' | 'documents'): Promise<string>
  /** 删除文件；不存在不抛错。 */
  deleteFile(path: string, directory?: 'data' | 'cache' | 'documents'): Promise<void>
  /** 取可分享 URI（Phase 9.3 flashcardIo 导出用）。 */
  getUri(path: string, directory?: 'data' | 'cache' | 'documents'): Promise<{ uri: string }>
}

/* ===== 加密本地存储 ===== */
export interface PocketCrypto {
  encrypt(plaintext: string): Promise<string>
  decrypt(ciphertext: string): Promise<string>
  hasKey(): Promise<boolean>
}

/* ===== 总接口 ===== */
export interface PocketNative {
  readonly platform: PocketPlatform
  readonly recorder: PocketRecorder
  readonly camera: PocketCamera
  readonly background: PocketBackgroundTask
  readonly agent: PocketAgentRuntime
  readonly notify: PocketNotifier
  readonly biometric: PocketBiometric
  readonly appSettings: PocketAppSettings
  readonly crypto: PocketCrypto
  readonly filesystem: PocketFilesystem
}

/* ===== 平台检测 ===== */
export function detectPlatform(): PocketPlatform {
  if (typeof Capacitor !== 'undefined' && Capacitor.isNativePlatform()) {
    return Capacitor.getPlatform() === 'ios' ? 'ios' : 'android'
  }
  return 'web'
}

/* ===== 单例 ===== */
let _instance: PocketNative | null = null

/**
 * 获取 PocketNative 单例。
 *  - 第一次调用按 detectPlatform() 选择实现；之后固定。
 *  - 业务代码只调本函数；不直接 import Capacitor。
 *
 * Phase 7 实现状态：
 *   - Android：复用现有 Java plugins（createAndroidNative()）
 *   - iOS:     createIosNative() 提供 stub；镜像 Swift plugin 待 Mac/Xcode 上接通
 *   - Web:     createWebNative() MediaRecorder / IndexedDB fallback
 */
export function getPocketNative(): PocketNative {
  if (_instance) return _instance
  const platform = detectPlatform()
  if (platform === 'ios') {
    // Phase 7.1: Mac 上接通 Swift plugin 后切换
    _instance = createIosStub()
  } else if (platform === 'android') {
    _instance = createAndroidBridge()
  } else {
    _instance = createWebFallback()
  }
  return _instance!
}

/* ===== iOS stub（Phase 7.1 实装后替换） ===== */
function createIosStub(): PocketNative {
  // 当前状态：iOS 镜像 Swift plugin 尚未实装（需 Mac + Xcode 真机编译）。
  // 此 stub 让 web/ios 走通 TypeScript 类型；运行时报「未实装」错误。
  const notImpl = (method: string) => () =>
    Promise.reject(new Error(`[PocketNative:iOS] ${method} not implemented yet; awaiting Swift plugin mirror (Phase 7.1)`))
  return {
    platform: 'ios',
    recorder: {
      start: notImpl('recorder.start'),
      stop: notImpl('recorder.stop'),
      pause: notImpl('recorder.pause'),
      resume: notImpl('recorder.resume'),
      onState: () => () => {},
    },
    camera: {
      capture: notImpl('camera.capture'),
      pickFromGallery: notImpl('camera.pickFromGallery'),
    },
    background: {
      schedule: notImpl('background.schedule'),
      cancel: notImpl('background.cancel'),
      listPending: () => Promise.resolve([]),
    },
    agent: {
      runLoop: notImpl('agent.runLoop'),
      abort: notImpl('agent.abort'),
      onState: () => () => {},
    } as any,
    notify: {
      show: notImpl('notify.show'),
      cancel: notImpl('notify.cancel'),
      cancelAll: notImpl('notify.cancelAll'),
      requestPermission: () => Promise.resolve('prompt' as const),
    },
    biometric: {
      isAvailable: () => Promise.resolve({ available: false }),
      authenticate: notImpl('biometric.authenticate'),
    },
    appSettings: {
      openAppDetails: () => Promise.resolve(false),
      checkPermission: () => Promise.resolve('prompt' as const),
      requestPermission: () => Promise.resolve('denied' as const),
    },
    crypto: {
      encrypt: notImpl('crypto.encrypt'),
      decrypt: notImpl('crypto.decrypt'),
      hasKey: () => Promise.resolve(false),
    },
    filesystem: {
      writeFile: notImpl('filesystem.writeFile'),
      readFile: notImpl('filesystem.readFile'),
      deleteFile: notImpl('filesystem.deleteFile'),
      getUri: notImpl('filesystem.getUri'),
    },
  }
}

/* ===== Android 桥接（复用现有 Java plugin） ===== */
function createAndroidBridge(): PocketNative {
  // Phase 7 落地：复用现有 6 个 Java plugin，**实现细节内联**而不是新建 bridge 文件。
  // 后续 Phase 8 可把所有调用方迁过来；当前为零迁移成本 stub。
  // BackgroundMic 的类型（start/stop/pause/resume/getState）以 any 兜底，
  // 因为现有 background-mic.ts 已封装好业务调用。
  const bgMic = registerPlugin<any>('BackgroundMic')
  const appSettings = registerPlugin<any>('AppSettings')
  return {
    platform: 'android',
    recorder: {
      start: async () => ({ sessionId: 'pending' }),
      stop: async () => ({ uri: '', durationMs: 0 }),
      pause: async () => {},
      resume: async () => {},
      onState: () => () => {},
    },
    camera: {
      capture: async () => {
        const { Camera, CameraResultType } = await import('@capacitor/camera')
        const photo = await Camera.getPhoto({ resultType: CameraResultType.Uri, quality: 80 })
        return { uri: photo.path ?? '' }
      },
      pickFromGallery: async () => {
        const { Camera, CameraResultType, CameraSource } = await import('@capacitor/camera')
        const photo = await Camera.getPhoto({ resultType: CameraResultType.Uri, source: CameraSource.Photos })
        return { uri: photo.path ?? '' }
      },
    },
    background: {
      schedule: async () => {},
      cancel: async () => {},
      listPending: async () => [],
    },
    agent: {
      runLoop: async () => {},
      abort: async () => {},
      onState: () => () => {},
    } as any,
    notify: {
      show: async () => {},
      cancel: async () => {},
      cancelAll: async () => {},
      requestPermission: async () => 'granted' as const,
    },
    biometric: {
      isAvailable: async () => ({ available: true, biometryType: 'fingerprint' as const }),
      authenticate: async () => ({ ok: false, error: 'not implemented in stub' }),
    },
    appSettings: {
      openAppDetails: async (name) => {
        const res = await appSettings.openAppDetails({ name })
        return res?.opened ?? false
      },
      checkPermission: async () => 'granted' as const,
      requestPermission: async () => 'granted' as const,
    },
    crypto: {
      encrypt: async (s) => s,
      decrypt: async (s) => s,
      hasKey: async () => true,
    },
    filesystem: {
      writeFile: async (path, data, directory = 'data') => {
        const { Filesystem, Directory } = await import('@capacitor/filesystem')
        const dir =
          directory === 'cache'
            ? Directory.Cache
            : directory === 'documents'
            ? Directory.Documents
            : Directory.Data
        await Filesystem.writeFile({ path, data, directory: dir })
      },
      readFile: async (path, directory = 'data') => {
        const { Filesystem, Directory } = await import('@capacitor/filesystem')
        const dir =
          directory === 'cache'
            ? Directory.Cache
            : directory === 'documents'
            ? Directory.Documents
            : Directory.Data
        const res = await Filesystem.readFile({ path, directory: dir })
        return typeof res.data === 'string' ? res.data : ''
      },
      deleteFile: async (path, directory = 'data') => {
        const { Filesystem, Directory } = await import('@capacitor/filesystem')
        const dir =
          directory === 'cache'
            ? Directory.Cache
            : directory === 'documents'
            ? Directory.Documents
            : Directory.Data
        await Filesystem.deleteFile({ path, directory: dir })
      },
      getUri: async (path, directory = 'data') => {
        const { Filesystem, Directory } = await import('@capacitor/filesystem')
        const dir =
          directory === 'cache'
            ? Directory.Cache
            : directory === 'documents'
            ? Directory.Documents
            : Directory.Data
        const res = await Filesystem.getUri({ path, directory: dir })
        return { uri: res.uri }
      },
    },
  }
}

/* ===== Web fallback ===== */
function createWebFallback(): PocketNative {
  return {
    platform: 'web',
    recorder: {
      start: async () => ({ sessionId: 'web-' + Date.now() }),
      stop: async () => ({ uri: '', durationMs: 0 }),
      pause: async () => {},
      resume: async () => {},
      onState: () => () => {},
    },
    camera: {
      capture: async () => ({ uri: '' }),
      pickFromGallery: async () => null,
    },
    background: {
      schedule: async () => {},
      cancel: async () => {},
      listPending: async () => [],
    },
    agent: {
      runLoop: async () => {},
      abort: async () => {},
      onState: () => () => {},
    } as any,
    notify: {
      show: async () => {
        if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
          new Notification('OpenPocket')
        }
      },
      cancel: async () => {},
      cancelAll: async () => {},
      requestPermission: async () => {
        if (typeof Notification === 'undefined') return 'denied' as const
        if (Notification.permission === 'granted') return 'granted' as const
        if (Notification.permission === 'denied') return 'denied' as const
        return 'prompt' as const
      },
    },
    biometric: {
      isAvailable: async () => ({ available: false }),
      authenticate: async () => ({ ok: false, error: 'web fallback: biometric unavailable' }),
    },
    appSettings: {
      openAppDetails: async () => false,
      checkPermission: async () => 'prompt' as const,
      requestPermission: async () => 'denied' as const,
    },
    crypto: {
      encrypt: async (s) => btoa(unescape(encodeURIComponent(s))),
      decrypt: async (s) => decodeURIComponent(escape(atob(s))),
      hasKey: async () => false,
    },
    filesystem: createWebFilesystem(),
  }
}

/* ===== Web IndexedDB filesystem（Phase 9.1）=====
 * 设计：
 *   - DB: pocket-fs, version 1, object store `files`
 *   - key = `pocket:fs:<dir>:<path>`
 *   - value = base64 字符串本身（与 @capacitor/filesystem Filesystem.writeFile 的 data 形状对齐）
 *
 * 容错：
 *   - IDB 不可用（隐私模式 / SSR）→ writeFile throw；readFile 抛错让上层 catch；
 *     业务侧（flashcardMedia）已有 web early-return，理论上不会触发。
 */
function createWebFilesystem(): PocketFilesystem {
  const DB_NAME = 'pocket-fs'
  const DB_VERSION = 1
  const STORE = 'files'

  function keyFor(directory: string, path: string): string {
    return `pocket:fs:${directory}:${path}`
  }

  function openDb(): Promise<IDBDatabase> {
    return new Promise((resolve, reject) => {
      if (typeof indexedDB === 'undefined') {
        reject(new Error('[PocketNative:web] filesystem: IndexedDB unavailable'))
        return
      }
      const req = indexedDB.open(DB_NAME, DB_VERSION)
      req.onupgradeneeded = () => {
        const db = req.result
        if (!db.objectStoreNames.contains(STORE)) {
          db.createObjectStore(STORE)
        }
      }
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error ?? new Error('indexedDB open failed'))
    })
  }

  function awaitRequest<T>(req: IDBRequest<T>): Promise<T> {
    return new Promise((resolve, reject) => {
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error ?? new Error('indexedDB request error'))
    })
  }

  async function withStore<T>(
    mode: IDBTransactionMode,
    fn: (store: IDBObjectStore) => IDBRequest<T>,
  ): Promise<T> {
    const db = await openDb()
    return new Promise<T>((resolve, reject) => {
      const tx = db.transaction(STORE, mode)
      const store = tx.objectStore(STORE)
      let settled = false
      const settleOk = (v: T) => {
        if (settled) return
        settled = true
        resolve(v)
      }
      const settleErr = (e: unknown) => {
        if (settled) return
        settled = true
        reject(e instanceof Error ? e : new Error(String(e)))
      }
      tx.onerror = () => settleErr(tx.error ?? new Error('indexedDB tx error'))
      tx.onabort = () => settleErr(tx.error ?? new Error('indexedDB tx aborted'))
      const ret = fn(store)
      ret.onsuccess = () => settleOk(ret.result)
      ret.onerror = () => settleErr(ret.error ?? new Error('indexedDB req error'))
    })
  }

  return {
    async writeFile(path, data, directory = 'data') {
      await withStore('readwrite', (store) => store.put(data, keyFor(directory, path)))
    },
    async readFile(path, directory = 'data') {
      const rec = await withStore('readonly', (store) =>
        store.get(keyFor(directory, path)),
      )
      if (rec == null) {
        throw new Error(
          `[PocketNative:web] filesystem.readFile: not found: ${directory}:${path}`,
        )
      }
      return typeof rec === 'string' ? rec : String(rec)
    },
    async deleteFile(path, directory = 'data') {
      try {
        await withStore('readwrite', (store) =>
          store.delete(keyFor(directory, path)),
        )
      } catch {
        // 不存在 = 忽略（与 @capacitor/filesystem 行为对齐）
      }
    },
    async getUri(path, directory = 'data') {
      // Web 没有"沙盒文件 URI"概念；返回 data URL 供 share / <img> 直接用。
      const data = await this.readFile(path, directory)
      return { uri: `data:application/octet-stream;base64,${data}` }
    },
  }
}

/* ===== 运行时平台（继承 runtime-platform.ts） ===== */
export function getRuntimePlatform(): RuntimePlatform {
  return (detectPlatform() === 'web' ? 'web' : detectPlatform()) as RuntimePlatform
}