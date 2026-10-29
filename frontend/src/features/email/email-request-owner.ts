/** Bind each step of a multi-request mail action to its initiating identity. */
export function bindEmailRequests<T extends object>(service: T, owner: { assertCurrent(): void }): T {
  return new Proxy(service, {
    get(target, key, receiver) {
      const method = Reflect.get(target, key, receiver)
      if (typeof method !== 'function') return method
      return async (...args: unknown[]) => {
        owner.assertCurrent()
        const result = await method.apply(target, args)
        owner.assertCurrent()
        return result
      }
    },
  })
}
