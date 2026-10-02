/**
 * 「更多」页 9 宫格的能力门控。
 *
 * 为什么单独抽一个纯函数（2026-10-03）：
 *   密码箱（/vault）在 Android 上**永远**打不开——原生侧没有 KeystorePlugin.java，
 *   StubKeystore 的 12 个方法全是 reject。但它此前是 9 宫格里一个**无条件**的一等公民
 *   入口，和「对话 / 邮箱」平级。用户点进去只会看到一句「功能不可用」。
 *   仓库自己在 native/keystore.ts:144 已经定过原则：
 *   「宁可少显示一个入口，也不要给一个必然失败的操作」。这次只是让入口遵守它。
 *
 * 为什么不用 featureFlags 的 security.keystore_v1：
 *   那个 flag 默认 false 且 serverOverrideable=false，纯粹是静态开关。
 *   用它 gate 入口的话，插件真落地那天还得记得回来把这个 flag 打开——
 *   也就是把「平台有没有这个能力」伪装成「谁记得改开关」。
 *   这里直接问真实能力 isKeystoreAvailable()：探针说能用，入口就出现，
 *   不需要任何人记得同步两处。
 *
 * 探针结果是异步的，首帧必然还没有结论。这里的关键决定是
 * **null（还没探完）按不可用处理**：先藏后现，好过先给一个必然失败的入口再撤掉。
 * 那会让用户看到宫格抖一下、并可能刚好点中它。
 */
import type { IconName } from '../../constants/icons'

export interface HubItem {
  to: string
  icon: IconName
  label: string
}

/**
 * 能力探针的结论。
 *   true  = 这个平台真的能用
 *   false = 确定不能用
 *   null  = 还没探完（首帧）。按不可用处理。
 */
export type CapabilityProbe = boolean | null

/**
 * 丢掉「这个平台做不到」的入口。
 *
 * gates 里没有出现的 route 一律保留——绝大多数入口不受能力门控，
 * 不能因为 map 里少一个键就把它们全筛掉。
 */
export function applyCapabilityGates(
  items: HubItem[],
  gates: Record<string, CapabilityProbe>,
): HubItem[] {
  return items.filter((item) => {
    const gate = gates[item.to]
    if (gate === undefined) return true
    return gate === true
  })
}
