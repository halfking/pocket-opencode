/**
 * SystemAudio Plugin - Web 降级实现
 */

import { WebPlugin } from '@capacitor/core'
import type { SystemAudioPlugin, CaptureMode } from './index'

export class SystemAudioWeb extends WebPlugin implements SystemAudioPlugin {
  async isSystemAudioSupported(): Promise<{ supported: boolean }> {
    // Web 不支持系统音频捕获
    return { supported: false }
  }
  
  async getAvailableCaptureModes(): Promise<{ modes: CaptureMode[] }> {
    // Web 仅支持麦克风
    return { modes: ['microphone'] }
  }
  
  async startSystemAudio(): Promise<{ sessionId: string; mode: string; uri: string }> {
    throw this.unimplemented('System audio capture is not available on web')
  }
  
  async startHybridMode(): Promise<{ sessionId: string; mode: string; uri: string }> {
    throw this.unimplemented('Hybrid mode is not available on web')
  }
  
  async stopRecording(): Promise<{ sessionId: string; uri: string }> {
    throw this.unimplemented('Not recording')
  }
}
