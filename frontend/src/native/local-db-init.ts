/**
 * local-db-init — LocalDB 重开守卫。
 *
 * 已完成初始化且连接仍在时无需重开（幂等）；否则需要重新走 open 流程
 * （升级迁移 / 加密模式切换 / 连接丢失恢复）。
 */
export function localDbNeedsOpen(initialized: boolean, hasConn: boolean): boolean {
  return !(initialized && hasConn)
}
