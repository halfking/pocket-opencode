/** 邮件详情：回复 / 转发 / 转 Todo 的输入框默认关闭，点导航栏按钮才打开。 */
import { truncateUtf8BytesHead } from '../../utils/text.ts'

export type ComposeKind = 'hidden' | 'reply' | 'forward' | 'todo'

/**
 * §918：`/api/email/send` 的**主题**上限（服务端那个数的镜像）。
 *
 * ⚠ 出处：`backend/internal/server/server_assistant.go:1727`
 *   `if len(body.Subject) > 500 { 400 "subject too long" }`
 * ⚠⚠ **单位是 UTF-8 字节**（Go 的 `len(string)`）⇒ 500 字节 ≈ **166 个中文字**。
 *
 * ⚠⚠ **为什么这道闸真的会挡住人**：
 *   主题**完全由原邮件派生**（`defaultReplySubject` / `defaultForwardSubject`），
 *   **用户不能编辑它** ⇒ 原邮件主题一长，**回复/转发就永远发不出去**，
 *   而界面只显示泛化的「发送失败」⇒ 用户既不知道原因、也没有办法绕开。
 *   ⇒ 客户端必须**自己**保证发出去的主题能过闸。
 */
export const EMAIL_SUBJECT_BYTE_LIMIT = 500

export function toggleCompose(current: ComposeKind, next: ComposeKind): ComposeKind {
  if (next === 'hidden') return 'hidden'
  return current === next ? 'hidden' : next
}

export function composeSheetTitle(kind: ComposeKind, fromName: string): string {
  if (kind === 'reply') return `回复 ${fromName || '发件人'}`
  if (kind === 'forward') return '转发'
  if (kind === 'todo') return '转 Todo'
  return ''
}

/**
 * §918：回复/转发的主题**必须能过服务端的字节闸**（从**尾部**裁，前缀保留）。
 *
 * ⚠ 为什么裁**尾部**：`Re: ` / `Fwd: ` 前缀在**头部** ⇒ 裁尾时前缀一定还在
 *   ⇒ 用户看到的仍是「这是一封回复/转发」。
 * ⚠ 为什么裁**字节**而不是字符：服务端的 `len()` 是字节数（§916/§917 实测）
 *   ⇒ 按字符裁对中文会超闸 3 倍。
 * ⚠⚠ **这是静默截断**（界面上看不到被裁掉的部分）——
 *   「要不要让用户看见被截断的主题」是**产品口径**，已登记（简报 142 第 ① 问），
 *   本节只保证「这条邮件**发得出去**」这件确定的事。
 */
export function defaultReplySubject(subject: string): string {
  const s = subject.trim() || '(无主题)'
  const p = /^re:/i.test(s) ? s : `Re: ${s}`
  return truncateUtf8BytesHead(p, EMAIL_SUBJECT_BYTE_LIMIT)
}

export function defaultForwardSubject(subject: string): string {
  const s = subject.trim() || '(无主题)'
  const p = /^fwd:/i.test(s) ? s : `Fwd: ${s}`
  return truncateUtf8BytesHead(p, EMAIL_SUBJECT_BYTE_LIMIT)
}

export function todoFromDraft(text: string, fallbackTitle: string): {
  title: string
  description: string
} {
  const lines = text.replace(/\r\n/g, '\n').trim().split('\n')
  const title = (lines[0] || '').trim() || fallbackTitle || '(无主题)'
  const description = lines.slice(1).join('\n').trim()
  return { title, description }
}
