import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { createServer as createHttpServer } from 'node:http'

export async function loadSubtitleModules() {
  const root = fileURLToPath(new URL('../../../', import.meta.url))
  const cacheDir = await mkdtemp(join(tmpdir(), 'subtitle-owner-'))
  const vite = await createServer({ root, cacheDir, configFile: false,
    optimizeDeps: { noDiscovery: true }, server: { middlewareMode: true, hmr: { server: createHttpServer() } },
    resolve: { alias: { '@': `${root}src` } },
    plugins: [{ name: 'subtitle-no-db', enforce: 'pre',
      resolveId(source) { if (/\/native\/local-db(?:\.ts)?$/.test(source)) return '\0subtitle-no-db' },
      load(id) { if (id === '\0subtitle-no-db') return "export const localDB=new Proxy({},{get(){throw new Error('Unexpected DB IO')}})" },
    }],
  })
  try {
    const subtitle = await vite.ssrLoadModule('/src/stores/liveSubtitle.ts')
    const auth = await vite.ssrLoadModule('/src/stores/auth.ts')
    return { ...subtitle, ...auth, async close() { await vite.close(); await rm(cacheDir, { recursive: true, force: true }) } }
  } catch (error) {
    await vite.close(); await rm(cacheDir, { recursive: true, force: true }); throw error
  }
}
