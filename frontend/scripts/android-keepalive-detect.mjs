/**
 * android-keepalive-detect.mjs
 *
 * Android 三档启动档前置探测（T1 模拟器部分）：
 *   1. web assets 是否已同步进 android（android/app/src/main/assets/public）— 提示先跑 build-mobile
 *   2. dev 后端 (默认 http://localhost:8090) 是否存活 + 走一遍 SSE 探测（chat + stream + history）
 *   3. 从 AndroidManifest 探测 keepalive 能力（KiwoomKeepAliveService 兜底）
 *
 * 与 T1「Android 三档启动档」配合：本脚本只探测/报告，不启动模拟器、不装 APK。
 * 退出码：0 = 全部探测通过；1 = 任一关键项失败（用于 CI/脚本串接）。
 * 详细设计：docs/design/2026-09-09-ai-async-background-survival.md §D5
 */
import { existsSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const frontendDir = path.resolve(here, '..');
const backendUrl = (process.argv[2] || process.env.OPENPOCKET_BACKEND_URL || 'http://localhost:8090').replace(/\/$/, '');

const result = {
  webAssetsSynced: false,
  backend: { ok: false, streamChunks: 0, chunkBytes: 0, historyBytes: 0, error: null },
  manifest: { hasPackage: true, foregroundService: false, dataSyncPermission: false, serviceType: null },
};

const failures = [];
const USER = process.env.POCKET_USER || 'admin';
const PASS = process.env.POCKET_PASS || 'change-me';
const AUTH = 'Basic ' + Buffer.from(`${USER}:${PASS}`).toString('base64');

// ---------- 极简 EventSource（Node 20 无原生实现，仅够 SSE 探测） ----------
function streamSSE(url, { headers = {}, onMessage, timeoutMs = 8000 } = {}) {
  return new Promise((resolve, reject) => {
    const controller = new AbortController();
    const timer = setTimeout(() => {
      controller.abort();
      reject(new Error(`SSE timeout after ${timeoutMs}ms`));
    }, timeoutMs);
    fetch(url, { headers, signal: controller.signal })
      .then(async (res) => {
        if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = '';
        try {
          for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            buf += decoder.decode(value, { stream: true });
            const blocks = buf.split('\n\n');
            buf = blocks.pop() || '';
            for (const block of blocks) {
              const line = block.split('\n').find((l) => l.startsWith('data:'));
              if (!line) continue;
              const payload = line.slice(5).trim();
              if (!payload) continue;
              if (onMessage(payload) === 'done') {
                clearTimeout(timer);
                controller.abort();
                resolve();
                return;
              }
            }
          }
          clearTimeout(timer);
          resolve();
        } catch (err) {
          clearTimeout(timer);
          reject(err);
        }
      })
      .catch(reject);
  });
}

// ---------- 1. 探测 web assets ----------
function detectWebAssets() {
  const p = path.join(frontendDir, 'android/app/src/main/assets/public/index.html');
  result.webAssetsSynced = existsSync(p);
  if (!result.webAssetsSynced) {
    failures.push({
      phase: 'web-assets',
      message: 'android/app/src/main/assets/public 缺少 index.html（未同步 dist）',
      next: '先跑 CI 或本地 `cd frontend && pnpm build-mobile`，该脚本会 vite build + npx cap sync android',
    });
  }
}

// ---------- 2. 探测后端 + SSE ----------
async function detectBackend() {
  if (typeof globalThis.EventSource === 'undefined') {
    // Node 20+: 走自实现 SSE，不用浏览器 EventSource
  }
  const headers = { Authorization: AUTH, 'Content-Type': 'application/json' };
  try {
    const chatRes = await fetch(`${backendUrl}/api/ai/chat`, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        messages: [{ role: 'user', content: 'respond with the single word: pong' }],
        user_id: USER,
      }),
      signal: AbortSignal.timeout(15_000),
    });
    if (chatRes.status === 404) {
      throw new Error('/api/ai/chat 404（后端未到 M4 版本或未启动 openpocket 后端）');
    }
    if (!chatRes.ok) {
      const text = (await chatRes.text().catch(() => '')) || '';
      throw new Error(`ai/chat HTTP ${chatRes.status}: ${text.slice(0, 200)}`);
    }
    const chatJson = await chatRes.json().catch(() => ({}));
    const conversationId = chatJson.conversation_id || chatJson.id;
    if (!conversationId) {
      throw new Error(`ai/chat 响应缺少 conversation_id: ${JSON.stringify(chatJson).slice(0, 200)}`);
    }

    let streamChunks = 0;
    let chunkBytes = 0;
    await streamSSE(`${backendUrl}/api/ai/stream/${conversationId}`, {
      headers: { Authorization: AUTH },
      onMessage: (payload) => {
        try {
          const d = JSON.parse(payload);
          if (d.type === 'chunk' && typeof d.delta === 'string') {
            streamChunks += 1;
            chunkBytes += d.delta.length;
          }
          if (d.type === 'done' || d.type === 'aborted' || d.type === 'error') return 'done';
        } catch {
          /* 单条解析失败不算失败 */
        }
      },
      timeoutMs: 12_000,
    });
    result.backend.streamChunks = streamChunks;
    result.backend.chunkBytes = chunkBytes;
    if (streamChunks === 0) {
      throw new Error('SSE 正常返回但 0 个 chunk —— 后端流管线可能异常');
    }

    const histRes = await fetch(
      `${backendUrl}/api/ai/history/${conversationId}?user_id=${encodeURIComponent(USER)}&roles=user,assistant`,
      { headers: { Authorization: AUTH }, signal: AbortSignal.timeout(8000) }
    );
    if (histRes.ok) {
      result.backend.historyBytes = (await histRes.text()).length;
    }
    result.backend.ok = true;
  } catch (err) {
    result.backend.error = String(err && err.message ? err.message : err);
    failures.push({
      phase: 'backend',
      message: result.backend.error,
      next: `确认 ${backendUrl} 可访问，且已启用 M4 接口（/api/ai/chat /stream /history）。
   本地启动参考: docs/design/2026-09-09-ai-async-background-survival.md §Mock LLM dev-loop`,
    });
  }
}

// ---------- 3. 探测 manifest / keepalive ----------
async function detectNative() {
  const manifestPath = path.join(frontendDir, 'android/app/src/main/AndroidManifest.xml');
  try {
    const xml = await readFile(manifestPath, 'utf8');
    result.manifest.foregroundService = /<service[\s\S]*?(KiwoomKeepAliveService|AiStreamService)/.test(xml);
    result.manifest.dataSyncPermission = xml.includes('FOREGROUND_SERVICE_DATA_SYNC');
    const m = xml.match(/foregroundServiceType="([^"]+)"/);
    result.manifest.serviceType = m ? m[1] : null;
    if (!result.manifest.foregroundService) {
      failures.push({
        phase: 'native',
        message: 'AndroidManifest.xml 未声明 keepalive 前台服务（KiwoomKeepAliveService 或 AiStreamService）',
        next: '参考 frontend/android/app/src/main/AndroidManifest.xml 中已加的 AiStreamService 段（M5/T2 已落）',
      });
    }
    if (!result.manifest.dataSyncPermission) {
      failures.push({
        phase: 'native',
        message: 'AndroidManifest.xml 缺少 FOREGROUND_SERVICE_DATA_SYNC 权限（T2 Android 前台服务需要）',
        next: '在 manifest 加 <uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC" />',
      });
    }
  } catch (err) {
    result.manifest.hasPackage = false;
    failures.push({
      phase: 'native',
      message: `读取 AndroidManifest.xml 失败: ${err.message}`,
      next: '确认已在 frontend 根目录运行，或先 `npx cap add android` + `pnpm build-mobile`',
    });
  }
}

async function main() {
  detectWebAssets();
  await detectBackend();
  await detectNative();

  const exitCode = failures.length === 0 ? 0 : 1;
  console.log('\n=== android-keepalive-detect 结果 ===');
  console.log(
    JSON.stringify(
      {
        exitCode,
        ...result,
        failures,
      },
      null,
      2
    )
  );
  if (failures.length > 0) {
    console.error('\n💡 修复提示:');
    for (const f of failures) {
      console.error(`  [${f.phase}] ${f.message}\n    → ${f.next}`);
    }
  }
  process.exit(exitCode);
}

main().catch((err) => {
  console.error('android-keepalive-detect 本身出错:', err);
  process.exit(1);
});
