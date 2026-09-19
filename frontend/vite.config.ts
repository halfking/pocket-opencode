import { defineConfig } from "vite"
import vue from "@vitejs/plugin-vue"
import path from "path"

const apiProxy = process.env.VITE_API_PROXY || "http://127.0.0.1:8090"

export default defineConfig({
  plugins: [vue()],
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
})
