/**
 * Tiny helper: register a Capacitor plugin when running native, return a
 * fallback stub on web/dev. Keeps the native/*.ts modules uniform.
 */
export function registerPluginSafely<T>(name: string, stub: T): T {
  // Dynamic import avoids a hard dependency on @capacitor/core in pure-web
  // builds and during SSR/SSG. If Capacitor isn't present we use the stub.
  //
  // BUG-G (2026-09-30)：registerPlugin 返回的是**带 .then 的 thenable 代理**。
  // 若从 async 函数直接 return 它，promise 决议会去调 .then()，未实现的原生
  // 插件会抛 "<Name>.then() is not implemented"，连 stub 降级都走不到。
  // 与 biometricAuth.ts / background-mic.ts 的既定约定一致：用 { value } 盒子
  // 装载实例，async 只返回盒子。
  let cached: { value: T } | null = null
  const ensure = async (): Promise<{ value: T }> => {
    if (cached) return cached
    let impl: T
    try {
      const cap = await import('@capacitor/core')
      impl = (cap.registerPlugin as <T>(n: string) => T)(name) as T
    } catch {
      impl = stub
    }
    cached = { value: impl }
    return cached
  }
  // Return a proxy that awaits the impl on every call — simplest correct form.
  return new Proxy(stub as any, {
    get(_target, prop: string) {
      return (...args: unknown[]) =>
        ensure().then((box) => {
          const fn = (box.value as any)[prop]
          if (typeof fn !== 'function') {
            return Promise.reject(new Error(`${name}.${prop} not implemented`))
          }
          return fn.apply(box.value, args)
        })
    },
  }) as T
}
