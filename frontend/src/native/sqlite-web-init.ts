/**
 * sqlite-web-init.ts — Web 平台的 jeep-sqlite 初始化。
 *
 * local-db.ts 仅在 Capacitor.getPlatform() === 'web' 时调用本模块（浏览器
 * 开发/QC 路径）；原生 Android/iOS 不会加载。jeep-sqlite 提供 IndexedDB
 * 后端的 <jeep-sqlite> 自定义元素，@capacitor-community/sqlite 的 Web
 * 实现依赖它存在。
 *
 * 该文件此前缺失导致 vue-tsc 构建门禁失败（TS2307）；此处按官方 README
 * 的 Web 集成方式补齐：defineCustomElements + 挂载隐藏元素 + 等待注册完成。
 */

export async function initSqliteWeb(): Promise<void> {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  // loader 入口没有类型声明（package.json types 指向 components.d.ts），
  // 动态 import 并按结构断言，避免引入任何构建期类型依赖。
  const mod = (await import('jeep-sqlite/loader')) as unknown as {
    defineCustomElements: (win?: Window) => void
  }
  mod.defineCustomElements(window)

  if (!document.querySelector('jeep-sqlite')) {
    const el = document.createElement('jeep-sqlite')
    el.style.display = 'none'
    document.body.appendChild(el)
  }
  await customElements.whenDefined('jeep-sqlite')
}
