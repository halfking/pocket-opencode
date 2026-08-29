import { createApp } from "vue"
import { createPinia } from "pinia"
import App from "./app/App.vue"
import router from "./app/router-mobile"
import { initWsBus } from "./services/ws-bus"
import { connectWs } from "./api/websocket"
import { useConnectivityStore } from "./stores/connectivity"
import i18n from "./i18n"
import "./styles.css"
import "./styles/tokens.css"
import "./styles/responsive.css"
import "./styles/material-symbols.css"

// StatusBar 控制权已迁到 App.vue 的 setup 生命周期（start/stop 绑定组件卸载），
// 这里只做副作用无关的初始化。

const pinia = createPinia()
const app = createApp(App)
app.use(pinia)
app.use(router)
app.use(i18n)
app.mount("#app")

// 离线同步接线（P1）：网络恢复 / App 回前台时自动 drain outbox + 同步会话
// 与审批快照；全局状态条读取该 store。未登录 / 本地库未解锁时静默跳过。
useConnectivityStore(pinia).init()

// 🦞 启动 WS 事件集中路由层：把所有需要监听的服务端推送一次性订阅好，
// 后续各 store / view 只跟 ws-bus 打交道。
initWsBus()
// 🦞 仅在已持久化 token 时建立 WS（未登录则 no-op）。
// 登录流程（LoginView）在认证成功后会再次调用 connectWs。
connectWs()