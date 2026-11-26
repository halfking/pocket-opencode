const RETURN_PATH = '/auth/sso/app-return'
const PROOF_PATTERN = /^[A-Za-z0-9_-]{43}$/
const CODE_PATTERN = /^[a-f0-9]{64}$/

export interface NativeSSOProof {
  verifier: string
  challenge: string
  appOrigin: string
  apiBase: string
  startUrl: string
  expiresAt: number
}

function base64url(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export async function createNativeSSOProof(apiBase: string, offeredOrigin: string, bundledOrigin: string): Promise<NativeSSOProof> {
  const api = new URL(apiBase)
  const app = new URL(bundledOrigin)
  if (api.protocol !== 'https:' || api.username || api.password || api.pathname !== '/' ||
      api.search || api.hash || app.protocol !== 'https:' || app.origin !== bundledOrigin ||
      offeredOrigin !== bundledOrigin || app.port || app.hostname === api.hostname) {
    throw new Error('企业登录回跳地址与应用发行配置不匹配')
  }
  const verifier = base64url(crypto.getRandomValues(new Uint8Array(32)))
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  const challenge = base64url(new Uint8Array(digest))
  const start = new URL('/api/auth/sso/native/start', api)
  start.searchParams.set('challenge', challenge)
  return { verifier, challenge, appOrigin: app.origin, apiBase, startUrl: start.href, expiresAt: Date.now() + 10 * 60_000 }
}

export function readNativeSSOReturn(raw: string, proof: NativeSSOProof): string | undefined {
  let url: URL
  try { url = new URL(raw) } catch { return }
  if (Date.now() > proof.expiresAt || url.origin !== proof.appOrigin || url.username || url.password ||
      url.pathname !== RETURN_PATH || url.search) return
  const params = new URLSearchParams(url.hash.slice(1))
  const codes = params.getAll('code')
  const challenges = params.getAll('challenge')
  if (codes.length !== 1 || challenges.length !== 1 ||
      !CODE_PATTERN.test(codes[0]) || !PROOF_PATTERN.test(challenges[0]) || challenges[0] !== proof.challenge) return
  return codes[0]
}
