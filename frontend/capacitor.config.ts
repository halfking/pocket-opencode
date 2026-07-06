import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'com.kaixuan.opencode.pocket',
  appName: 'OpenCode Pocket',
  webDir: 'dist',
  // 本地打包模式，通过 VITE_API_BASE 环境变量指定后端地址
  android: {
    allowMixedContent: true,
    backgroundColor: '#ffffff',
  }
};

export default config;
