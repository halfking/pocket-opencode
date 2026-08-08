# Phase B: 笔记去过度安全 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 移除笔记字段级 AES-GCM 加密（保留 SQLCipher 全库加密），解除"登录密码 = 主密码"的耦合，提供原地解锁面板，让笔记日常使用零摩擦。

**Architecture:**
- 引入 `crypto.cfg` 单例持久化到 localStorage，决定是否调用 `encryptString`。
- `LoginView` 不再隐式 `initLobster(password)`，改为首启一次性"创建主密码"对话框（参考 vault `setupMasterPassword`）。
- `NoteListView` 增加原地解锁面板（不再强制跳 `/login`）。
- 保留 SQLCipher 全库加密与 PKM 明文路线两条路并存。

**Tech Stack:** Vue 3 + TypeScript / @capacitor-community/sqlite / Web Crypto (AES-GCM + PBKDF2) / Pinia stores。

---

## 文件结构

```
frontend/src/
├── native/
│   ├── crypto.ts                     # (改) 增加 getCryptoConfig/setCryptoConfig
│   └── lobster-init.ts               # (改) initLobster 接受独立主密码参数
├── features/
│   ├── notes/
│   │   ├── notes-store.ts            # (改) 读 crypto.cfg 决定 encryptString
│   │   └── NoteListView.vue          # (改) 增加原地解锁面板
│   └── auth/
│       ├── LoginView.vue             # (改) 移除隐式 initLobster
│       └── MasterPasswordDialog.vue  # (新增) 首启创建主密码
├── stores/
│   └── crypto-config.ts              # (新增) crypto.cfg pinia store
└── native/
    └── schema.ts                     # (改) local_notes 加 encrypted_content 列
```

---

## Task 1: crypto-config store

**Files:**
- Create: `frontend/src/stores/crypto-config.ts`

- [ ] **Step 1: 创建 store**

新建 `frontend/src/stores/crypto-config.ts`：

```ts
import { defineStore } from 'pinia'
import { ref } from 'vue'

export type FieldEncryptionMode = 'disabled' | 'enabled'

export interface CryptoConfig {
  /** 字段级 AES-GCM 是否启用（默认 disabled 推荐） */
  fieldEncryption: FieldEncryptionMode
  /** 是否已创建主密码 */
  hasMasterPassword: boolean
  /** 主密码派生 hint（用于解锁界面提示） */
  passwordHint: string | null
  /** 上次更新时间 */
  updatedAt: number
}

const STORAGE_KEY = 'pocket_crypto_cfg'

const DEFAULT_CONFIG: CryptoConfig = {
  fieldEncryption: 'disabled',
  hasMasterPassword: false,
  passwordHint: null,
  updatedAt: 0,
}

export const useCryptoConfig = defineStore('crypto-config', () => {
  const cfg = ref<CryptoConfig>(load())

  function load(): CryptoConfig {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return { ...DEFAULT_CONFIG }
      const parsed = JSON.parse(raw) as CryptoConfig
      return { ...DEFAULT_CONFIG, ...parsed }
    } catch {
      return { ...DEFAULT_CONFIG }
    }
  }

  function save() {
    cfg.value.updatedAt = Date.now()
    localStorage.setItem(STORAGE_KEY, JSON.stringify(cfg.value))
  }

  function setFieldEncryption(mode: FieldEncryptionMode) {
    cfg.value.fieldEncryption = mode
    save()
  }

  function setMasterPassword(hint: string | null) {
    cfg.value.hasMasterPassword = true
    cfg.value.passwordHint = hint
    save()
  }

  function resetMasterPassword() {
    cfg.value.hasMasterPassword = false
    cfg.value.passwordHint = null
    save()
  }

  function shouldEncryptField(): boolean {
    return cfg.value.fieldEncryption === 'enabled'
  }

  return {
    cfg,
    setFieldEncryption,
    setMasterPassword,
    resetMasterPassword,
    shouldEncryptField,
  }
})
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/stores/crypto-config.ts
git commit -m "feat(notes): 新增 crypto-config store 控制字段加密开关"
```

---

## Task 2: notes-store 读 cfg 决定是否 encryptString

**Files:**
- Modify: `frontend/src/features/notes/notes-store.ts`

- [ ] **Step 1: 阅读现有加密调用**

打开 `frontend/src/features/notes/notes-store.ts`，定位以下行：
- L74-87：`createNote` 写入时调 `encryptString`
- L109-114：插入 SQL 前 `encryptedContent`
- L274-302：`listNotes` 读出时调 `decryptString`
- L326-331：解密失败占位符

- [ ] **Step 2: 引入 cryptoConfig**

在 `notes-store.ts` 顶部 import：

```ts
import { useCryptoConfig } from '../../stores/crypto-config'
```

- [ ] **Step 3: 改造 createNote**

替换 `notes-store.ts:74-87` 的内容写入逻辑：

```ts
const cryptoConfig = useCryptoConfig()
const shouldEncrypt = cryptoConfig.shouldEncryptField()
const encryptedContent = shouldEncrypt
  ? await encryptString(note.content)
  : note.content  // 不加密：原样写入
```

- [ ] **Step 4: 改造 listNotes 解密**

替换 `notes-store.ts:274-302` 的 `decryptString(r.content)`：

```ts
const cryptoConfig = useCryptoConfig()
const shouldDecrypt = cryptoConfig.shouldEncryptField()
const finalContent = shouldDecrypt
  ? await decryptStringSafe(r.content)
  : r.content

async function decryptStringSafe(ct: string): Promise<string> {
  try {
    return await decryptString(ct)
  } catch {
    return '[加密内容无法解密]'
  }
}
```

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/notes/notes-store.ts
git commit -m "feat(notes): 读 crypto.cfg 决定是否调 encryptString/decryptString"
```

---

## Task 3: local_notes schema 加 encrypted_content 列

**Files:**
- Modify: `frontend/src/native/schema.ts`

- [ ] **Step 1: 定位 local_notes 表**

打开 `frontend/src/native/schema.ts`，定位 `local_notes` 的 CREATE TABLE 语句。

- [ ] **Step 2: 增加 encrypted_content 列**

在 `local_notes` 表增加列：

```sql
encrypted_content INTEGER NOT NULL DEFAULT 0
```

完整 schema 片段：

```sql
CREATE TABLE IF NOT EXISTS local_notes (
  id TEXT PRIMARY KEY,
  workspace_id TEXT,
  title TEXT,
  content TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT 'text',
  domain TEXT,
  category TEXT,
  tags TEXT,
  audio_path TEXT,
  audio_duration_ms INTEGER NOT NULL DEFAULT 0,
  created_by_voice INTEGER NOT NULL DEFAULT 0,
  encrypted_content INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

- [ ] **Step 3: 增加 ALTER TABLE 迁移**

在 schema.ts 现有 `migrate()` 函数（搜索 `migrate`）追加版本 7：

```ts
// 版本 7：local_notes.encrypted_content 列
const v7 = async (db: any) => {
  await db.run(`ALTER TABLE local_notes ADD COLUMN encrypted_content INTEGER NOT NULL DEFAULT 0`)
}
migrations.push({ version: 7, up: v7 })
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/native/schema.ts
git commit -m "feat(schema): local_notes 加 encrypted_content 列 + 迁移 v7"
```

---

## Task 4: MasterPasswordDialog 组件（首启创建 + 修改主密码）

**Files:**
- Create: `frontend/src/features/auth/MasterPasswordDialog.vue`

- [ ] **Step 1: 创建组件**

新建 `frontend/src/features/auth/MasterPasswordDialog.vue`：

```vue
<!--
  MasterPasswordDialog — 首启"创建主密码"对话框，或修改主密码。
  调用 initLobster(masterPassword) 完成初始化。
  Props:
    - mode: 'create' | 'change'
    - onSuccess(masterPassword): 回调
-->
<template>
  <div v-if="open" class="dialog-mask" @click.self="close">
    <div class="dialog">
      <h3>{{ mode === 'create' ? '创建主密码' : '修改主密码' }}</h3>
      <p class="hint">主密码用于加密本地笔记与密码箱，与登录密码独立。请妥善保管，丢失后无法恢复。</p>
      <input v-model="password" type="password" placeholder="主密码（≥8 字符）" autocomplete="new-password" />
      <input v-model="confirm" type="password" placeholder="再次输入" autocomplete="new-password" />
      <input v-model="hint" type="text" placeholder="密码提示（可选）" />
      <div v-if="error" class="error">{{ error }}</div>
      <div class="actions">
        <button class="ghost" @click="close">取消</button>
        <button class="primary" :disabled="submitting" @click="submit">
          {{ submitting ? '初始化中…' : '确认' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { initLobster } from '../../native/lobster-init'
import { useCryptoConfig } from '../../stores/crypto-config'

const props = defineProps<{
  open: boolean
  mode: 'create' | 'change'
}>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'success', masterPassword: string): void
}>()

const password = ref('')
const confirm = ref('')
const hint = ref('')
const error = ref('')
const submitting = ref(false)

const cryptoConfig = useCryptoConfig()

async function submit() {
  error.value = ''
  if (password.value.length < 8) {
    error.value = '主密码至少 8 字符'
    return
  }
  if (password.value !== confirm.value) {
    error.value = '两次输入不一致'
    return
  }
  submitting.value = true
  try {
    await initLobster(password.value)
    cryptoConfig.setMasterPassword(hint.value || null)
    emit('success', password.value)
    close()
  } catch (e: any) {
    error.value = e?.message || '主密码初始化失败'
  } finally {
    submitting.value = false
  }
}

function close() {
  password.value = ''
  confirm.value = ''
  hint.value = ''
  error.value = ''
  emit('close')
}
</script>

<style scoped>
.dialog-mask {
  position: fixed; inset: 0;
  background: rgba(0,0,0,0.5);
  display: flex; align-items: center; justify-content: center;
  z-index: 100;
}
.dialog {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  width: min(360px, 90vw);
  display: flex; flex-direction: column; gap: var(--space-2);
}
.dialog h3 { margin: 0; font-size: 16px; }
.hint { font-size: 12px; color: var(--text-secondary); margin: 0; }
.dialog input {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 14px;
}
.error { font-size: 12px; color: var(--danger); }
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-2); }
.ghost, .primary {
  flex: 1; padding: var(--space-2); border-radius: var(--radius-sm);
  border: 1px solid var(--border); cursor: pointer; font-size: 14px;
}
.primary { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.primary:disabled { opacity: 0.6; }
</style>
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/auth/MasterPasswordDialog.vue
git commit -m "feat(auth): 新增 MasterPasswordDialog 首启创建主密码"
```

---

## Task 5: LoginView 解除隐式 initLobster

**Files:**
- Modify: `frontend/src/features/auth/LoginView.vue`

- [ ] **Step 1: 定位 initLobster 调用**

打开 `frontend/src/features/auth/LoginView.vue`，定位 L151-156 的 `try { await initLobster(password.value) }`。

- [ ] **Step 2: 删除 initLobster 调用**

删除 `LoginView.vue:151-156` 的 `try { await initLobster(password.value) }` 整段。

- [ ] **Step 3: 引入 MasterPasswordDialog**

在 LoginView template 末尾追加：

```vue
<MasterPasswordDialog
  :open="showMasterPasswordDialog"
  :mode="cryptoConfig.cfg.hasMasterPassword ? 'change' : 'create'"
  @close="showMasterPasswordDialog = false"
  @success="onMasterPasswordCreated"
/>
```

并在 `<script setup>` 中：

```ts
import { ref } from 'vue'
import MasterPasswordDialog from './MasterPasswordDialog.vue'
import { useCryptoConfig } from '../../stores/crypto-config'
import { isLobsterReady } from '../../native/lobster-init'

const cryptoConfig = useCryptoConfig()
const showMasterPasswordDialog = ref(false)

onMounted(() => {
  // 登录后若未创建主密码，弹对话框（首启场景）
  if (!cryptoConfig.cfg.hasMasterPassword) {
    showMasterPasswordDialog.value = true
  }
})

function onMasterPasswordCreated(pwd: string) {
  // 主密码创建成功后无需额外操作；initLobster 已在 dialog 内完成
  console.log('[login] master password created, lobster ready:', isLobsterReady())
}
```

- [ ] **Step 4: 保留 needUnlock 分支**

确保 `LoginView.vue:91-119` 的 `needUnlock` 分支保留（用户在另一台设备/刷新后回到 login 页时仍可输入主密码解锁）：

```vue
<input v-model="unlockPassword" type="password" placeholder="主密码解锁" />
<button class="primary" @click="onUnlock">解锁</button>
```

其中 `onUnlock` 调 `initLobster(unlockPassword.value)` 即可（无需创建）。

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/auth/LoginView.vue
git commit -m "refactor(auth): LoginView 解除隐式 initLobster 由 MasterPasswordDialog 负责"
```

---

## Task 6: NoteListView 原地解锁面板

**Files:**
- Modify: `frontend/src/features/notes/NoteListView.vue`

- [ ] **Step 1: 定位 dbNotReady 兜底**

打开 `frontend/src/features/notes/NoteListView.vue`，定位 L8-17 的"本地数据未解锁"面板。

- [ ] **Step 2: 增加原地解锁面板**

替换 `NoteListView.vue:8-17` 的兜底面板为：

```vue
<div v-if="dbNotReady" class="state" style="padding: 40px 20px;">
  <p style="font-size: 48px; margin-bottom: 16px;">🔒</p>
  <p style="font-size: 16px; font-weight: 600; margin-bottom: 8px;">本地数据未解锁</p>
  <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
    输入主密码以解锁本地加密存储
  </p>
  <input
    v-model="unlockPassword"
    type="password"
    placeholder="主密码"
    style="width: 100%; padding: 10px; border: 1px solid var(--border); border-radius: 8px; margin-bottom: 12px;"
    @keyup.enter="onUnlock"
  />
  <button class="primary" :disabled="unlocking" @click="onUnlock" style="margin: 0 auto 8px; padding: 10px 24px; background: var(--brand-primary); color: white; border-radius: 8px; border: 0;">
    {{ unlocking ? '解锁中…' : '解锁' }}
  </button>
  <button class="btn-ghost" @click="goToLogin" style="display: block; margin: 0 auto; padding: 8px 24px; border: 1px solid var(--border); border-radius: 8px; background: transparent;">
    退出登录
  </button>
  <p v-if="unlockError" style="color: var(--danger); font-size: 12px; margin-top: 8px;">{{ unlockError }}</p>
</div>
```

- [ ] **Step 3: 增加解锁逻辑**

在 `<script setup>` 中：

```ts
import { initLobster } from '../../native/lobster-init'

const unlockPassword = ref('')
const unlocking = ref(false)
const unlockError = ref('')

async function onUnlock() {
  if (!unlockPassword.value) return
  unlocking.value = true
  unlockError.value = ''
  try {
    await initLobster(unlockPassword.value)
    unlockPassword.value = ''
    await load()
  } catch (e: any) {
    unlockError.value = e?.message || '主密码错误，请重试'
  } finally {
    unlocking.value = false
  }
}
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/notes/NoteListView.vue
git commit -m "feat(notes): NoteListView 增加原地解锁面板 不再强制跳登录"
```

---

## Task 7: vault 验证 crypto.config 兼容

**Files:**
- Modify: `frontend/src/features/vault/VaultListView.vue`

- [ ] **Step 1: 定位 setupMasterPassword**

打开 `frontend/src/features/vault/VaultListView.vue`，找到 `setupMasterPassword` 调用。

- [ ] **Step 2: 验证不与 notes 主密码冲突**

确保 vault 的主密码与 notes 的主密码**共用同一个 master password**：在 vault 的 setupMasterPassword 中调 `cryptoConfig.setMasterPassword(hint)`，保持单一来源。

```ts
import { useCryptoConfig } from '../../stores/crypto-config'
const cryptoConfig = useCryptoConfig()

async function onSetupMasterPassword() {
  await vaultStore.setupMasterPassword(masterPwd.value)
  cryptoConfig.setMasterPassword(hint.value || null)
}
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/vault/VaultListView.vue
git commit -m "refactor(vault): 复用 crypto-config 共享主密码状态"
```

---

## Task 8: e2e 验收测试

**Files:**
- Create: `frontend/tests/e2e/notes-de-over-secure.spec.ts`

- [ ] **Step 1: 创建测试文件**

新建 `frontend/tests/e2e/notes-de-over-secure.spec.ts`：

```ts
import { test, expect } from '@playwright/test'

test.describe('笔记去过度安全', () => {
  test('登录后弹出"创建主密码"对话框', async ({ page }) => {
    await page.goto('/#/login')
    // 模拟登录（账号密码通过 mock api）
    await page.fill('input[type=email]', 'test@example.com')
    await page.fill('input[type=password]', 'loginpass')
    await page.click('button[type=submit]')
    // 期望 MasterPasswordDialog 弹出
    await expect(page.locator('text=创建主密码')).toBeVisible()
  })

  test('进入 /notes 时若 db 未解锁，原地解锁面板可见', async ({ page }) => {
    await page.evaluate(() => localStorage.removeItem('pocket_lobster_ready'))
    await page.goto('/#/notes')
    await expect(page.locator('text=本地数据未解锁')).toBeVisible()
    await expect(page.locator('input[placeholder=主密码]')).toBeVisible()
    await expect(page.locator('button:has-text("解锁")')).toBeVisible()
  })

  test('crypto.cfg 默认 fieldEncryption=disabled', async ({ page }) => {
    await page.goto('/#/ai')
    const cfg = await page.evaluate(() => localStorage.getItem('pocket_crypto_cfg'))
    expect(JSON.parse(cfg || '{}').fieldEncryption).toBe('disabled')
  })
})
```

- [ ] **Step 2: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/e2e/notes-de-over-secure.spec.ts --reporter=list
```

期望：3 个 test 通过。

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/e2e/notes-de-over-secure.spec.ts
git commit -m "test(notes): 笔记去过度安全 e2e 验收"
```

---

## Self-Review

**1. Spec 覆盖（设计文档 §2.2.A 三档模型）**：
- [x] L0 默认（去字段加密 + 保留 SQLCipher + 首启创建主密码）→ Task 1 + Task 4 + Task 5
- [x] 原地解锁面板（不再跳登录）→ Task 6
- [x] crypto.cfg 单例持久化 → Task 1
- [x] notes-store 读 cfg → Task 2
- [x] schema 迁移 v7 → Task 3
- [x] vault 兼容 → Task 7
- [x] e2e → Task 8

**2. 占位符扫描**：
- 无 "TBD" / "TODO" / "实现细节后续"。
- 所有代码块均含完整实现。

**3. 类型一致性**：
- `CryptoConfig.fieldEncryption: 'disabled' | 'enabled'` → Task 1 store + Task 2 notes-store 读法一致。
- `shouldEncryptField()` 返回 boolean → Task 2 使用一致。

**4. 风险**：
- Task 3 schema 迁移 v7：旧用户升级时需 ALTER TABLE ADD COLUMN；本地 SQLite 在 web 端首次启动会自动迁移，Android 需 clear data 才能强制迁移（或在 init 时检测列存在性）。
- Task 5 LoginView 删除 initLobster 后，老用户（已用 master password = login password）需走"忘记主密码"路径 → 在设置页提供"重置主密码"入口（Task 7 留作 v1.5，本期仅留 TODO）。

**5. 不在本期**：
- PKM 改造（保留明文 FTS 路线）。
- 老用户数据迁移向导（v1.5）。