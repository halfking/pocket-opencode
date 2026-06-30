import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'com.kaixuan.opencode.pocket',
  appName: 'OpenCode Pocket',
  webDir: 'dist',
  // 注释掉 server.url，使用本地打包的文件
  // server: {
  //   url: 'http://14.103.169.56:8088',
  //   cleartext: true
  // },
  android: {
    allowMixedContent: true, // 允许混合内容 (HTTP + HTTPS)
    backgroundColor: '#ffffff'
  }
};

export default config;
