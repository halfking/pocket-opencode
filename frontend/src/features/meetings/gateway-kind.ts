/** BFF kind → 网关 work-type。空字符串表示不打 work-type，走 preferred 模型。 */
export function gatewayWorkType(kind: string | undefined): string {
  switch (kind) {
    case 'live_translate':
    case 'doc_translate':
      return 'doc_translate'
    case 'meeting_summary':
      return 'meeting_summary'
    case 'meeting_refine':
      return 'doc_translate'
    default:
      return ''
  }
}
