import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'com.kaixuan.opencode.pocket',
  appName: 'OpenCode Pocket',
  webDir: 'dist',
  server: {
    // 不要设置 server.url：那会让 WebView 加载远程站点而非本地打包资源，
    // 导致真机访问 localhost 打不开页面。API 地址由前端代码里的
    // VITE_API_BASE（构建期注入 http://192.168.31.45:8088）决定。
    cleartext: true,
  },
  android: {
    allowMixedContent: true,
    backgroundColor: '#ffffff',
  },
  ios: {
    contentInset: 'always',
    backgroundColor: '#ffffff',
    preferredContentMode: 'mobile',
  },
  plugins: {
    SplashScreen: {
      // 原生顺滑度审计 A3/P0 #3：废除 2s 定时 splash。launchShowDuration: 0 +
      // launchAutoHide: false 让 splash 一直盖到首帧渲染完成，由 main.ts 主动
      // hide（200ms fade）——冷启动体感从"定时 2s + 白屏等待"变为就绪即进。
      launchShowDuration: 0,
      launchAutoHide: false,
      backgroundColor: '#ffffff',
    },
    /**
     * 状态栏：
     * - overlaysWebView: true —— WebView 绘制延伸到状态栏区域，body 背景
     *   真正"铺满全屏"。body 的 padding-top = var(--app-safe-top) 仍然
     *   让标题栏让出状态栏高度（不留白边）。
     * - style: 'LIGHT' —— 浅色图标（深字）；深浅主题切换时由 App.vue 的
     *   useStatusBar 按当前皮肤设置 style / backgroundColor。
     */
    StatusBar: {
      overlaysWebView: true,
      style: 'LIGHT',
    },
  },
};

export default config;