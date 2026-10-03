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

    // BUG-F 修复 (2026-09-30)：WebView 本地页面的 scheme。
    //
    // 默认 'https' 时页面 origin = https://localhost。此时 http:// 的 XHR 由
    // WebSettings.setMixedContentMode(MIXED_CONTENT_ALWAYS_ALLOW) 放行
    // （仅 DEBUG 分支，release 走 NEVER_ALLOW），所以 REST 调用能通。
    //
    // 但 WebSocket 不受 mixed content mode 管辖：Chromium >= 111 的
    // "Insecure WebSocket" 策略会【硬阻断】从安全上下文发起的 ws:// 连接，
    // 控制台报 "attempted to connect to the insecure WebSocket endpoint ...
    // Insecure access is deprecated."，随后 WebSocket error → 无限重连。
    // 真机上表现为任务流式输出 / 审批 / 会话推送等实时通道全部失效。
    //
    // 改用 'http' 后页面 origin = http://localhost，与后端 http/ws 同为
    // 非安全上下文，mixed content 规则不再适用：XHR 走 CORS（后端
    // corsMiddleware 对 dev 放行 http://localhost），ws:// 直接放行。
    //
    // 这里保留 'https' 为默认值：生产环境后端应走 HTTPS + wss，届时不需要
    // 降级。仅在本地/内网 HTTP 后端联调时用 CAP_ANDROID_SCHEME=http 构建。
    androidScheme: (process.env.CAP_ANDROID_SCHEME as 'http' | 'https') ?? 'https',
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