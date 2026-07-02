import { createApp } from "vue"
import { createPinia } from "pinia"
import App from "./app/App.vue"
import router from "./app/router-mobile"
import { initWsBus } from "./services/ws-bus"
import i18n from "./i18n"
import "./styles.css"
import "./styles/tokens.css"

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(i18n)
app.mount("#app")

// 🦞 启动 WS 事件集中路由层：把所有需要监听的服务端推送一次性订阅好，
// 后续各 store / view 只跟 ws-bus 打交道。
initWsBus()