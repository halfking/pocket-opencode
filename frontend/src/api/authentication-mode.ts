/** The authenticated login response selects the authority route. This value is
 * only a routing hint; persisted mode and workspace never prove membership. */
export function usesDirectPocketAuthority(method: string): boolean {
  // 'redclaw-sso'（2026-11-19）：Casdoor 直连登录（/api/auth/sso/direct-login）
  // 由 pocketd 服务端代办 Casdoor 认证后签发**完整 pocketd JWT**，与 'password'
  // 登录签发的 token 同级 —— 必须走 direct authority，否则会被误判为
  // 资源凭证模式，/api/tasks 等路由 fail-closed 抛
  // PocketResourceUnavailableError（真机实测：任务看板/ACC 生态全红）。
  return ['password', 'legacy', 'dev-bypass', 'redclaw-sso'].includes(method)
}
