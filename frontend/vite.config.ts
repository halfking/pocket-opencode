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
