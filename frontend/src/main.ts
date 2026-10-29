import { createApp } from "vue"
import { Capacitor } from "@capacitor/core"
import { createPinia } from "pinia"
import { useAuthStore } from "./stores/auth"
import { useSyncPolicyStore } from "./stores/syncPolicyStore"
import { startSyncPolicyRuntime } from "./stores/sync-policy-runtime"
import App from "./app/App.vue"
import router from "./app/router-mobile"
import { initWsBus } from "./services/ws-bus"
import { connectWs } from "./api/websocket"
import { useConnectivityStore } from "./stores/connectivity"
import { useThemeStore } from "./stores/theme"
import { startEmailConfigSync } from "./features/email/account-sync"
import { startUserConfigSync } from "./native/config-sync/runtime"
import { startPortalNotesSync } from "./native/portal-sync/runtime"
import { appLifecycleHub } from "./native/appLifecycleHub.ts"
import { installLlmBffStreamRuntime } from "./api/llm-bff"
import { startAiStreamKeepalive } from "./native/aiStreamKeepalive.ts"
import {
  ApprovalsRuntime,
  setApprovalsRuntime,
  getApprovalsRuntime,
} from "./native/approvalsRuntime.ts"
import { listPendingApprovals } from "./api/approvals.ts"
import {
  initIdempotentWsBus,
  subscribe as wsBusSubscribe,
} from "./services/idempotentWsBus.ts"
import { startNotificationDispatcher } from "./services/notificationDispatcher.ts"
import {
  APPROVAL_EVENT_TYPES,
  parseApprovalEvent,
} from "./services/approvalEvents.ts"
import wsClient from "./api/websocket.ts"
import i18n from "./i18n"
import "./styles.css"
import "./styles/tokens.css"
import "./styles/responsive.css"
// 2026-10-04 修复孤儿文件：breakpoints.css 从未被任何入口 import，
// 于是断点阶梯的 CSS 镜像（--bp-* 变量与 .bp-* 工具类）根本没进产物。
// 门禁只保证它与 TS 常量一致，却保证不了它被打包——这类「文档与门禁都绿、
// 但东西没上线」的空档靠接线补上。narrow 子档工具类现在才真正可用。
import "./styles/breakpoints.css"
import "./styles/material-symbols.css"

// StatusBar 控制权已迁到 App.vue 的 setup 生命周期（start/stop 绑定组件卸载），
// 这里只做副作用无关的初始化。

const pinia = createPinia()
// 首帧前应用持久化的皮肤偏好（html[data-theme] / color-scheme），避免暗色闪白
useThemeStore(pinia)
const app = createApp(App)
app.use(pinia)
const syncAuth = useAuthStore(pinia)
const initialActivation = syncAuth.syncFromStorage()
// Install policy admission before any mounted component can start sync work.
startSyncPolicyRuntime(syncAuth, useSyncPolicyStore(pinia), {
  startRuntimes() {
    useConnectivityStore(pinia).init()
    startEmailConfigSync()
    startUserConfigSync()
    startPortalNotesSync()
  },
  connect: connectWs,
  disconnect: () => wsClient.disconnect(),
})
void initialActivation.catch(error => console.warn('[auth] 启动会话恢复失败：', error))
app.use(router)
app.use(i18n)
app.mount("#app")

// Splash 就绪即隐（原生顺滑度审计 A3/P0 #3）：config 已关定时（launchShowDuration:0
// + launchAutoHide:false），这里在 Vue mount + 首帧绘制完成后主动 fade 掉。
// 双 rAF 确保首帧真的上屏；Web 端无 splash，no-op。
//
// ⚠ §948：原来这里写着「插件加载失败静默——卡住 splash 比没有 splash 更糟，
// 200ms 后兜底强隐」，但**那个 200ms 兜底强隐从来就不存在**（全仓没有）。
// 没有插件就没有 hide 可调 ⇒ JS 侧无法强隐 ⇒ 唯一能做的是**留痕**：
// 失败时至少留下一条线索，别让「白屏卡死」变成查不到根因的灵异事件。
// （要不要改 capacitor.config 的 launchAutoHide 作安全网 = 产品口径，已登记。）
if (Capacitor.isNativePlatform()) {
  const hideSplash = () => {
    import("@capacitor/splash-screen")
      .then(({ SplashScreen }) =>
        SplashScreen.hide({ fadeOutDuration: 200 }).catch((e) => {
          // hide 失败 ⇒ 启动画面可能一直盖着（launchAutoHide:false）。
          console.warn('[splash] 隐藏启动画面失败：', e)
        }),
      )
      .catch((e) => {
        // 插件加载失败 ⇒ 没有 hide 可调 ⇒ 启动画面可能一直盖着。
        console.warn('[splash] 插件加载失败，启动画面可能未被隐藏：', e)
      })
  }
  requestAnimationFrame(() => requestAnimationFrame(hideSplash))
}

// 🦞 启动 WS 事件集中路由层：把所有需要监听的服务端推送一次性订阅好，
// 后续各 store / view 只跟 ws-bus 打交道。
initWsBus()
// 🦞 仅在已持久化 token 时建立 WS（未登录则 no-op）。
// 登录流程（LoginView）在认证成功后会再次调用 connectWs。
// Connection starts only from the ready-state watcher above.

// M1（2026-09-09）：AI 流所有权上移 — 启动 lifecycle hub（hidden/visible/frozen/resumed
// 收口）+ 注入 llm-bff 流式运行时依赖（fetcher / apiBase / token）。
// aiStreamRuntime 自身惰性 start()（首次 spawn 时触发），此处显式注册 lifecycle
// 让"切后台 → 暂停 120s watchdog → 切回前台续命"在首条流之前就生效。
appLifecycleHub.start()
installLlmBffStreamRuntime()
// M5/T2（2026-09-10）：Android 前台服务保活 —「活跃流 + 已切后台」时拉起
// AiStreamService。仅 Android Capacitor 生效；Web/iOS 检测后 no-op。
startAiStreamKeepalive()

// M2（2026-09-09）：审批轮询上移到 approvalsRuntime（进程级 singleton）。
// 组件级 usePendingApprovals 仅订阅本视图的 pendingPermissions；切走/切回页面
// 不影响 runtime 继续轮询 / 监听 WS 审批推送。WS / 计时器在此处一次性 start()。
{
  initIdempotentWsBus()
  // 通知分发器(2026-09-20 通知体系 P1):关键 WS 事件 → inbox 入账 +
  // 前台 toast + 后台系统通知(deepLink 回跳);WS 连接成功时增量补拉 inbox。
  startNotificationDispatcher(pinia, router)
  setApprovalsRuntime(
    new ApprovalsRuntime({
      isOnline: () => useConnectivityStore(pinia).online,
      isWsConnected: () => wsClient.isConnected(),
      fetchPending: ({ instanceId, sessionId }) =>
        listPendingApprovals({ instanceID: instanceId, sessionID: sessionId }),
      subscribeApprovalEvents: (handler) => {
        const subs = APPROVAL_EVENT_TYPES.map((t) =>
          wsBusSubscribe(t, (env) => handler({ type: t, payload: env })),
        )
        return () => {
          for (const s of subs) s.unsubscribe()
        }
      },
    }),
  )
  getApprovalsRuntime()?.start()
}
