/** The authenticated login response selects the authority route. This value is
 * only a routing hint; persisted mode and workspace never prove membership. */
export function usesDirectPocketAuthority(method: string): boolean {
  // Existing direct Casdoor logins can retain their old persisted label.
  return ['password', 'legacy', 'dev-bypass', 'casdoor-oidc', 'redclaw-sso'].includes(method)
}
