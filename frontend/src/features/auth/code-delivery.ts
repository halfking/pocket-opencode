/**
 * 「发送验证码」之后要不要推进到输入验证码那一步的判定。
 *
 * 背景（真机审计发现的静默失败）：忘记密码三段式的第 1 步点「发送验证码」，
 * 后端在没有配置 SMTP 时依然恒返 200 `{"ok": true, "ttl_sec": 300}`（防枚举，
 * 且这是刻意的设计）。但此时验证码只写库、**根本不会发邮件**。旧前端只看
 * status 是 200 就推进到第 2 步，于是用户永远停在「输入验证码」，界面上
 * 没有任何错误提示，看起来像是自己没收到邮件、实际是服务端压根没发。
 *
 * 后端现在会在响应里带 `delivery`（见 server_auth_extended.go 的
 * handleAuthSendCode）：`smtp` = 有邮件通道，`none` = 没有。这是全局部署
 * 事实，与该邮箱是否注册无关，所以不破坏防枚举。
 *
 * 唯一可以放行的例外是 dev 模式：POCKET_SMTP_DEBUG_ECHO 会把验证码回显到
 * 响应体，开发者照着回显值就能走通流程，挡住反而会打断本地联调。
 */

export interface SendCodeShape {
  /** 后端声明的投递能力；老后端不返回该字段。 */
  delivery?: 'smtp' | 'none'
  /** 仅 dev（DEBUG_ECHO 且未配 SMTP）时回显。 */
  debug_code?: string
}

export type DeliveryVerdict =
  | { advance: true; error: '' }
  | { advance: false; error: string }

export const NO_MAIL_DELIVERY_ERROR =
  '本服务未配置邮件服务器，验证码邮件无法送达，请联系管理员重置密码'

/**
 * @param res `sendCode()` 的响应体
 * @returns advance=true 表示可以进入第 2 步；否则带上要展示给用户的错误文案
 */
export function judgeCodeDelivery(res: SendCodeShape): DeliveryVerdict {
  // 老后端没有 delivery 字段：按「能投递」处理，避免误伤已部署的实例。
  if (res.delivery !== 'none') return { advance: true, error: '' }
  // dev 模式回显了验证码，流程仍然走得通，不该拦。
  if (res.debug_code) return { advance: true, error: '' }
  return { advance: false, error: NO_MAIL_DELIVERY_ERROR }
}
