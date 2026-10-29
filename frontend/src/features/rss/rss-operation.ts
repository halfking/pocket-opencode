/**
 * rss-operation.ts — RSS 删除操作的**发起会话**绑定。
 *
 * ⚠ 为什么需要这个文件（审计发现）：
 *   `RssListView.deleteSource` 原来是「先 await确认框，再发删除请求」。
 *   确认框是一个**可等待的 UI 交互**：用户在弹窗上停留期间若发生
 *   A→B 账号切换（或 token 续期改变了会话代际），那么 await 之后发出的
 *   DELETE 会带上**新账号**的凭据 ⇒ A 看到的订阅源被 B 的身份删掉。
 *
 * ⚠ 与「在途操作隔离」的区别：
 *   笔记/邮件/会议那几个切片处理的是「请求发出后等待响应」的中途换号；
 *   这里处理的是「请求**还没发出**就已经换号」—— 一个更早的失效点。
 *   两者都必须绑定，否则删除会落到错误的主体名下。
 *
 * 归属原则：**不猜测历史归属**。这里只绑定发起时会话，不为历史数据
 * 声明任何 owner；workspace 为空时也不回退到 `default`。
 */
import { assertAuthSession, captureAuthSession, useAuthStore, type AuthSession } from '../../stores/auth'
import { localDB, type OperationLease } from '../../native/local-db'

export interface RssDeleteOperation {
  /** 发起时的会话快照；账号切换后它不再成立。 */
  readonly session: AuthSession
  /** 发起时的本地库租约；身份或策略代际变化后它不再成立。 */
  readonly lease: OperationLease
  readonly db: ReturnType<typeof localDB.bindOperation>
  readonly workspaceId: string
  assertCurrent(): void
}

/**
 * 在**打开确认框之前**捕获会话。
 *
 * 调用方必须先 capture，再 await 确认框，最后用同一个 operation 发请求。
 * 若确认框期间发生账号切换，`assertCurrent()` 抛出 AbortError，
 * 删除请求不会被发出。
 */
export function captureRssDeleteOperation(): RssDeleteOperation {
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  const workspaceId = auth.workspaceId
  const lease = localDB.captureOperation({ kind: 'local-edit' })
  const db = localDB.bindOperation(lease)
  return {
    session,
    lease,
    db,
    workspaceId,
    assertCurrent() {
      assertAuthSession(auth, session)
      db.assertCurrent()
      assertAuthSession(auth, session)
    },
  }
}

/**
 * 删除一个订阅源的完整流程：**先捕获会话，再弹确认框，最后带会话发送 DELETE**。
 *
 * ⚠ 顺序是本文件的核心：确认框是异步等待点。若在 await 之后才捕获会话，
 *   弹窗期间的账号切换就不会被看见 ⇒ 删除落到新账号名下。
 *
 * `confirm` 由调用方注入（视图用全局确认框），返回 false 表示用户取消。
 * 会话已失效时返回 'stale-session'，**不发送任何请求**，也不当作失败上报。
 */
export async function deleteRssSource(
  source: { id: string; title: string },
  confirm: (options: { title: string; message: string; confirmText: string; danger: boolean }) => Promise<boolean>,
  deleteSource: (id: string, options: { assertCurrent: () => void }) => Promise<void>,
): Promise<'deleted' | 'cancelled' | 'stale-session'> {
  const operation = captureRssDeleteOperation()
  const ok = await confirm({
    title: '删除订阅源',
    message: `确定删除订阅源「${source.title}」？所有 item 也会被删除。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return 'cancelled'
  try {
    operation.assertCurrent()
  } catch (error) {
    if ((error as { name?: string })?.name === 'AbortError') return 'stale-session'
    throw error
  }
  await deleteSource(source.id, { assertCurrent: operation.assertCurrent })
  return 'deleted'
}
