import { createApp } from "vue"
import { createPinia } from "pinia"
import App from "./app/App.vue"
import router from "./app/router-mobile"
import { initWsBus } from "./services/ws-bus"
import { connectWs } from "./api/websocket"
import { useConnectivityStore } from "./stores/connectivity"
import { useThemeStore } from "./stores/theme"
import { startEmailConfigSync } from "./features/email/account-sync"
import { startUserConfigSync } from "./native/config-sync/runtime"
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
import {
  APPROVAL_EVENT_TYPES,
  parseApprovalEvent,
} from "./services/approvalEvents.ts"
import wsClient from "./api/websocket.ts"
import i18n from "./i18n"
import "./styles.css"
import "./styles/tokens.css"
import "./styles/responsive.css"
import "./styles/material-symbols.css"

// StatusBar 控制权已迁到 App.vue 的 setup 生命周期（start/stop 绑定组件卸载），
// 这里只做副作用无关的初始化。

const pinia = createPinia()
// 首帧前应用持久化的皮肤偏好（html[data-theme] / color-scheme），避免暗色闪白
useThemeStore(pinia)
const app = createApp(App)
app.use(pinia)
app.use(router)
app.use(i18n)
app.mount("#app")

// 离线同步接线（P1）：网络恢复 / App 回前台时自动 drain outbox + 同步会话
// 与审批快照；全局状态条读取该 store。未登录 / 本地库未解锁时静默跳过。
useConnectivityStore(pinia).init()
startEmailConfigSync()
startUserConfigSync()

// 🦞 启动 WS 事件集中路由层：把所有需要监听的服务端推送一次性订阅好，
// 后续各 store / view 只跟 ws-bus 打交道。
initWsBus()
// 🦞 仅在已持久化 token 时建立 WS（未登录则 no-op）。
// 登录流程（LoginView）在认证成功后会再次调用 connectWs。
connectWs()

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