import { defineConfig } from "vite"
import vue from "@vitejs/plugin-vue"
import path from "path"

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
      "/api": "http://localhost:8088",
      "/ws": { target: "http://localhost:8088", ws: true },
      "/healthz": "http://localhost:8088",
    },
  },
})
