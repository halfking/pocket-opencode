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
     * - overlaysWebView:false —— Android 上 WebView 布局在状态栏之下，
     *   这样 env(safe-area-inset-top) 才能拿到非 0 值，配合 body padding 给
     *   标题栏让出系统状态栏高度。
     * - style: 'LIGHT' —— 浅色图标；深浅主题切换时由 App.vue 按背景切换。
     */
    StatusBar: {
      overlaysWebView: false,
      style: 'LIGHT',
    },
  },
};

export default config;