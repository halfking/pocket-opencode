/**
 * Tiny helper: register a Capacitor plugin when running native, return a
 * fallback stub on web/dev. Keeps the native/*.ts modules uniform.
 */
export function registerPluginSafely<T>(name: string, stub: T): T {
  // Dynamic import avoids a hard dependency on @capacitor/core in pure-web
  // builds and during SSR/SSG. If Capacitor isn't present we use the stub.
  let cached: T | null = null
  const ensure = async (): Promise<T> => {
    if (cached) return cached
    try {
      const cap = await import('@capacitor/core')
      cached = (cap.registerPlugin as <T>(name: string) => T)(name) as T
    } catch {
      cached = stub
    }
    return cached
  }
  // Return a proxy that awaits the impl on every call — simplest correct form.
  return new Proxy(stub as any, {
    get(_target, prop: string) {
      return (...args: unknown[]) =>
        ensure().then((impl) => {
          const fn = (impl as any)[prop]
          if (typeof fn !== 'function') {
            return Promise.reject(new Error(`${name}.${prop} not implemented`))
          }
          return fn.apply(impl, args)
        })
    },
  }) as T
}
