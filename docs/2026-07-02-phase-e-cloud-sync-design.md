# ☁️ Phase E — 龙虾云同步设计

**日期**: 2026-07-02
**状态**: 实现完成（vault blob 上传/下载/智能同步）

> 用户数据全部在手机本地加密存储。云同步是**可选功能**，只传密文 blob，服务端零知识。

---

## 1. 同步策略

| 场景 | 策略 | 实现 |
|------|------|------|
| 单设备偶尔备份 | 整库加密 blob 上传 | `sync-store.uploadSync()` |
| 换新手机恢复 | 从云端下载 blob → 解密 → 写入本地 | `sync-store.downloadSync()` |
| 多设备同时编辑 | MVP: last-write-wins | `sync-store.smartSync()` |
| 历史版本恢复 | GET /api/vault/sync/versions | `sync-store.listVersions()` |

### 加密层次（双层）

```
本地库（SQLCipher AES-256）
  └── local_vault_entries.entry_ciphertext（AES-GCM 密文，字段级）
       └── exportEncryptedBlob() 再包一层 AES-GCM（整库级）
            └── POST /api/vault/sync → pocketd 只存密文 blob
```

服务端有两层保护都无法突破：① PG 里是密文 blob，② blob 内每条 entry 也是密文。

---

## 2. 文件清单

| 文件 | 职责 |
|------|------|
| `vault-store.ts` | `exportEncryptedBlob()` + `importEncryptedBlob()` + `countEntries()` |
| `sync-store.ts` | 与 pocketd `/api/vault/sync` 的 HTTP 交互 |
| `VaultListView.vue` | 云同步按钮 + 同步状态显示 |
| `server_assistant.go:handleVaultSync` | pocketd 端 PUT/GET/versions |

---

## 3. 智能同步流程（smartSync）

```
用户点击"☁️ 云同步"
  │
  ├── 检查本地条目数
  │     ├── localCount > 0 → uploadSync()（本地优先，push 到云端）
  │     └── localCount == 0
  │           ├── GET /api/vault/sync/latest
  │           ├── 云端有数据 → downloadSync()（恢复）
  │           └── 云端空 → "无需同步"
  │
  └── 显示结果: "已上传 X 条" 或 "已恢复 X 条"
```

---

## 4. 后续演进

当前 MVP 用 last-write-wins（整库覆盖）。未来可改进：

1. **增量同步**：只传变化的 entry（基于 `updated_at` watermark），减少传输量
2. **CRDT 合并**：多设备同时编辑时自动合并（需要 entry 级 CRDT）
3. **笔记/邮件同步**：当前只同步 vault（密码箱），可扩展到 notes/emails（同样加密 blob）
4. **选择性同步**：用户选择哪些数据类型同步（如不同步邮件正文）
