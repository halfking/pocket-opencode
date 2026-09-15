/**
 * localagent/tools/fs.ts — 文件工具的沙箱存储后端。
 *
 * 设计(对应 pi mobile-handoff 的 ExecutionEnv/FileSystem 思路,裁剪版):
 *   - 路径永远是沙箱根下的相对路径,`..` 与绝对路径直接拒绝;
 *   - 原生(Capacitor)→ @capacitor/filesystem,Directory.Data 下 `agent/` 子目录,
 *     动态 import 保持 node --test 可加载;
 *   - Web / 测试 → localStorage 虚拟 FS(`pocket:localagent:fs:` 前缀);
 *   - 后端可在测试中整体替换(构造时注入)。
 */

export interface FsBackend {
  read(path: string): Promise<string>
  write(path: string, data: string): Promise<void>
  list(path: string): Promise<string[]>
  remove(path: string): Promise<void>
}

const SANDBOX_ROOT = 'agent'
const LS_PREFIX = 'pocket:localagent:fs:'

/** 路径规范化:拒绝越界,返回沙箱内相对路径('' 表示根)。 */
export function normalizePath(input: string): string {
  const trimmed = (input ?? '').trim().replace(/^\/+/, '')
  if (input.includes('\\')) throw new Error('路径不允许反斜杠')
  const parts = trimmed.split('/').filter((p) => p !== '' && p !== '.')
  for (const p of parts) {
    if (p === '..') throw new Error('路径不允许 ..(沙箱限制)')
  }
  return parts.join('/')
}

/** localStorage 虚拟 FS(Web/测试)。key = LS_PREFIX + path;目录由前缀推导。 */
type StorageLike = Pick<Storage, 'getItem' | 'setItem' | 'removeItem' | 'key' | 'length'>

export class MemoryFsBackend {
  private readonly store: StorageLike

  constructor(store: StorageLike = defaultStore()) {
    this.store = store
  }

  async read(path: string): Promise<string> {
    const v = this.store.getItem(LS_PREFIX + path)
    if (v === null) throw new Error(`文件不存在:${path}`)
    return v
  }

  async write(path: string, data: string): Promise<void> {
    this.store.setItem(LS_PREFIX + path, data)
  }

  async list(dir: string): Promise<string[]> {
    const prefix = dir ? `${dir}/` : ''
    const names = new Set<string>()
    for (let i = 0; i < this.store.length; i++) {
      const key = this.store.key(i)
      if (!key || !key.startsWith(LS_PREFIX)) continue
      const p = key.slice(LS_PREFIX.length)
      if (prefix && !p.startsWith(prefix)) continue
      const rest = prefix ? p.slice(prefix.length) : p
      if (rest === '') continue
      const seg = rest.split('/')[0]
      names.add(rest.includes('/') ? `${seg}/` : seg)
    }
    return [...names].sort()
  }

  async remove(path: string): Promise<void> {
    if (this.store.getItem(LS_PREFIX + path) === null) throw new Error(`文件不存在:${path}`)
    this.store.removeItem(LS_PREFIX + path)
  }
}

function defaultStore(): Storage {
  if (typeof localStorage !== 'undefined') return localStorage
  // node --test 环境:内存 map 顶替。
  const m = new Map<string, string>()
  return {
    getItem: (k) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
    key: (i) => [...m.keys()][i] ?? null,
    get length() {
      return m.size
    },
  } as Storage
}

/** 原生 Capacitor FS;仅在原生平台使用(Web/Node 走 Memory 兜底,避免
 *  Capacitor Web 实现的 IndexedDB 在非浏览器环境拖死工具调用)。 */
async function tryCapacitorFs(): Promise<(FsBackend & { kind: 'capacitor' }) | null> {
  try {
    const core = await import('@capacitor/core')
    if (!core.Capacitor?.isNativePlatform?.()) return null
    const mod = await import('@capacitor/filesystem')
    const { Filesystem, Directory, Encoding } = mod
    // 探测一次:目录能 mkdir 说明原生桥在。
    await Filesystem.mkdir({ path: SANDBOX_ROOT, directory: Directory.Data, recursive: true }).catch(() => {})
    const join = (p: string) => (p ? `${SANDBOX_ROOT}/${p}` : SANDBOX_ROOT)
    return {
      kind: 'capacitor',
      async read(path) {
        const res = await Filesystem.readFile({ path: join(path), directory: Directory.Data, encoding: Encoding.UTF8 })
        return typeof res.data === 'string' ? res.data : new TextDecoder().decode(res.data as unknown as Uint8Array)
      },
      async write(path, data) {
        await Filesystem.writeFile({ path: join(path), directory: Directory.Data, data, encoding: Encoding.UTF8, recursive: true })
      },
      async list(path) {
        const res = await Filesystem.readdir({ path: join(path), directory: Directory.Data })
        return res.files.map((f) => (f.type === 'directory' ? `${f.name}/` : f.name))
      },
      async remove(path) {
        await Filesystem.deleteFile({ path: join(path), directory: Directory.Data })
      },
    }
  } catch {
    return null
  }
}

let cachedBackend: FsBackend | null = null

/** 取沙箱 FS 后端(进程级缓存;测试用 setFsBackend 重置)。 */
export async function getFsBackend(): Promise<FsBackend> {
  if (cachedBackend) return cachedBackend
  cachedBackend = (await tryCapacitorFs()) ?? new MemoryFsBackend()
  return cachedBackend
}

/** 测试注入用。 */
export function setFsBackend(backend: FsBackend | null): void {
  cachedBackend = backend
}
