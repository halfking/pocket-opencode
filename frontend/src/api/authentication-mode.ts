/** The authenticated login response selects the authority route. This value is
 * only a routing hint; persisted mode and workspace never prove membership. */
export function usesDirectPocketAuthority(method: string): boolean {
  return ['password', 'legacy', 'dev-bypass'].includes(method)
}
