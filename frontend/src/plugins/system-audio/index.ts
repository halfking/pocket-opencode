/**
 * SystemAudio Plugin - 前端桥接层
 * 
 * 提供跨平台的系统音频捕获能力
 */

import { registerPlugin } from '@capacitor/core'

export type CaptureMode = 'microphone' | 'system' | 'hybrid'

export interface SystemAudioPlugin {
  /**
   * 检查系统音频捕获支持
   */
  isSystemAudioSupported(): Promise<{ supported: boolean; apiLevel?: number; iOSVersion?: string }>
  
  /**
   * 获取可用的捕获模式
   */
  getAvailableCaptureModes(): Promise<{ modes: CaptureMode[] }>
  
  /**
   * 启动系统音频捕获（仅系统音频）
   */
  startSystemAudio(): Promise<{ sessionId: string; mode: string; uri: string }>
  
  /**
   * 启动混合模式（系统音频 + 麦克风）
   */
  startHybridMode(): Promise<{ sessionId: string; mode: string; uri: string }>
  
  /**
   * 停止录音
   */
  stopRecording(): Promise<{ sessionId: string; uri: string }>
  
  /**
   * 添加音频数据监听器
   */
  addListener(
    eventName: 'audioData',
    listenerFunc: (data: { sessionId: string; length: number; type?: string }) => void
  ): Promise<PluginListenerHandle>
  
  /**
   * 添加错误监听器
   */
  addListener(
    eventName: 'captureError',
    listenerFunc: (data: { error: string }) => void
  ): Promise<PluginListenerHandle>
  
  /**
   * 移除所有监听器
   */
  removeAllListeners(): Promise<void>
}

export interface PluginListenerHandle {
  remove: () => Promise<void>
}

const SystemAudio = registerPlugin<SystemAudioPlugin>('SystemAudio', {
  web: () => import('./web').then(m => new m.SystemAudioWeb()),
})

export default SystemAudio
