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
      launchShowDuration: 2000,
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