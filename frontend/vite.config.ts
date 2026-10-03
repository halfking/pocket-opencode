import { defineConfig, loadEnv } from "vite"
import vue from "@vitejs/plugin-vue"
import path from "path"

const apiProxy = process.env.VITE_API_PROXY || "http://127.0.0.1:8090"

/**
 * BUG-D 守卫（2026-09-30 真机验收发现）。
 *
 * 背景：VITE_API_BASE 只在加载到对应 .env.<mode> 时才会被内联进 bundle。
 * 任何一次裸 `vite build`（即 `npm run build` / `npm run build:fast`，都走
 * MODE=production 而 `.env.production` 并不存在）都会把它替换成 undefined，
 * 于是：
 *   1. ServerSelectView 的 "Build default" 选项因 v-if 整条消失；
 *   2. App 运行时回落到同源 —— 在 Capacitor 里就是 WebView 自己的
 *      https://localhost，/api 请求拿回本地 index.html（HTML 而非 JSON）。
 * 实测该故障在一次验收中自发复现 4 次。
 *
 * 原守卫只写在 scripts/build-mobile.mjs 里，任何绕过该脚本的构建都不受保护。
 * 这里下沉到 vite.config，使守卫对所有 mode 生效。判定用加载后的 env 而非
 * process.env，因为 Vite 的 .env 解析优先级是：
 *   process.env > .env.<mode>.local > .env.<mode> > .env.local > .env
 * 只看 process.env 会在「值来自 .env 文件」时误报。
 *
 * 逃生舱：
 *   - MOBILE_ALLOW_EMPTY_API_BASE=1  显式放行（仅 web 同源部署等确有需要的场景）
 *   - 非构建（vite dev / vite preview）不校验
 */
function assertApiBaseForBuild(mode: string, env: Record<string, string>) {
  if (mode === 'development') return // vite dev 走 server.proxy，不需要绝对地址
  if (env.MOBILE_ALLOW_EMPTY_API_BASE === '1' || env.MOBILE_ALLOW_EMPTY_API_BASE === 'true') return

  const base = String(env.VITE_API_BASE || '').trim()
  if (base) return

  throw new Error(
    [
      '',
      '[vite] 拒绝构建：VITE_API_BASE 为空。',
      '',
      '  本次 mode = "' + mode + '"，没有加载到任何提供 VITE_API_BASE 的 env 文件。',
      '  移动端 bundle 一旦缺少它，App 会静默回落到 WebView 同源',
      '  （https://localhost），所有 /api 请求返回本地 index.html 而不是 JSON。',
      '',
      '  移动端请用：node scripts/build-mobile.mjs <ios|android> <dev|staging|prod>',
      '  Web 同源部署确实需要空值时：MOBILE_ALLOW_EMPTY_API_BASE=1 npm run build',
      '',
    ].join('\n'),
  )
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  assertApiBaseForBuild(mode, env)

  // 构建时刻（2026-10-03）。
  //
  // 为什么需要它：APP_VERSION.buildDate 是写死的 '2026-06-29'，而
  // build-mobile.mjs 从不更新它。设置页因此长期显示一个与实际产物无关的日期，
  // 而「设备上装的是不是最新包」恰恰是验收时最需要判断的一件事
  // （handoff §4.74.2 丢过一整轮就是这个）。
  //
  // 注入成**编译期常量**而不是运行时读：运行时没有任何可信的时钟来源，
  // 而 __BUILD_TIME__ 会随这一次构建被固化进 bundle，读它就是读「这个包
  // 是什么时候打的」。
  //
  // 格式说明：不用 toISOString()，因为它丢掉时区偏移，读起来像是 UTC 却没写。
  // 这里取本机时区并显式带偏移，避免「构建日期」在不同机器上含义漂移。
  const builtAt = new Date()
  const tzOffsetMinutes = -builtAt.getTimezoneOffset()
  const tzSign = tzOffsetMinutes >= 0 ? '+' : '-'
  const tzAbs = Math.abs(tzOffsetMinutes)
  const tzLabel =
    tzSign +
    String(Math.floor(tzAbs / 60)).padStart(2, '0') +
    ':' +
    String(tzAbs % 60).padStart(2, '0')
  const buildTimestamp =
    builtAt.getFullYear() +
    '-' +
    String(builtAt.getMonth() + 1).padStart(2, '0') +
    '-' +
    String(builtAt.getDate()).padStart(2, '0') +
    ' ' +
    String(builtAt.getHours()).padStart(2, '0') +
    ':' +
    String(builtAt.getMinutes()).padStart(2, '0') +
    ':' +
    String(builtAt.getSeconds()).padStart(2, '0') +
    ' UTC' +
    tzLabel

  return {
    plugins: [vue()],
    define: {
      // 字符串字面量形式：__BUILD_TIME__ 会被替换成带引号的字符串。
      __BUILD_TIME__: JSON.stringify(buildTimestamp),
    },
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    build: {
      rollupOptions: {
        output: {
          // 框架代码独立 vendor 包（原生顺滑度审计 A3/P1 #7）：业务改动不再整体
          // 失效框架缓存，主包仅含首屏业务代码
          manualChunks: {
            "vue-vendor": ["vue", "vue-router", "pinia", "vue-i18n"],
          },
        },
      },
    },
    server: {
      host: "0.0.0.0",
      port: 4174,
      proxy: {
        "/api": apiProxy,
        "/ws": { target: apiProxy, ws: true },
        "/healthz": apiProxy,
      },
    },
  }
})
