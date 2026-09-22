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
    /**
     * BUG-A 修复 (2026-09-22)：Capacitor 8 SystemBars 默认 insetsHandling='css'
     * 会在 WebView 装载早期执行 evaluateJavascript 注入
     *   document.documentElement.style.setProperty('--safe-area-inset-*', ...)
     * 但该 script 跑得比 document.documentElement 创建还早，在
     * Chrome WebView 126 上 (Chromium bug 40699457 引入的回归,直到 140 才
     * 在 SystemBars 修复) 抛 "Cannot read properties of null (reading 'style')"。
     *
     * 表现：TypeError 干扰 Vue mount，导致 WebView 全局点击事件不触发
     * （[chromium] console.log 显示 "Uncaught TypeError"，但被 Capacitor
     * 脚本的 try/catch 局部捕获后仍留下 1+ 个 setProperty 抛出的异常，
     * Vue 渲染管线无法 attach event handlers）。
     *
     * 修法：把 insetsHandling 关掉，由我们 MainActivity.injectSafeInsets()
     * 独家负责向 --android-safe-top 注入（main.ts 启动后定时 flush 一次
     * 兜底 race window）。Capacitor 不再代为注入。
     */
    SystemBars: {
      insetsHandling: 'disable',
    },
  },
};

export default config;