# 🔐 密码箱专项安全设计

**版本**: v1.0.0
**日期**: 2026-07-02
**状态**: 设计方案
**归属**: OpenCode Pocket 个人助理 APP — 密码箱模块

> 配套：主方案 [`2026-07-02-android-personal-assistant-plan.md`](./2026-07-02-android-personal-assistant-plan.md)

---

## 📌 概要

一个**生物识别保护、本地加密、可选端到端加密跨设备同步**的个人密码管家，集成进 OpenCode Pocket APP。核心目标：即使设备丢失或服务端被入侵，密码条目也不泄露。

---

## 🎯 设计目标

1. **零明文**：密码条目在客户端内存之外永不以明文存在；服务端仅存密文
2. **生物识别解锁**：指纹/面部，无需记忆主密码（但保留主密码兜底）
3. **离线可用**：完全本地解密，不依赖服务端
4. **跨设备同步**（可选）：端到端加密，主密钥不经服务端
5. **易用**：自动填充、密码生成、强度评估

---

## 🏗️ 加密架构

### 密钥层级（三段式）

```
┌─────────────────────────────────────────────────────────┐
│ Layer 1: Master Key (MK)                                 │
│   - 256-bit AES key, 用户主密码经 PBKDF2 派生            │
│   - 或由 Android Keystore 生成的生物识别绑定密钥          │
│   - 仅存于内存（解锁后）+ Keystore 内（受硬件保护）       │
├─────────────────────────────────────────────────────────┤
│ Layer 2: Vault Encryption Key (VEK)                      │
│   - 256-bit AES-GCM key, 用 MK 加密后存本地               │
│   - 所有密码条目用 VEK 加密                              │
│   - 换主密码时只需重加密 VEK，不用重加密全部条目          │
├─────────────────────────────────────────────────────────┤
│ Layer 3: Entry Ciphertext                                │
│   - 每条密码条目用 VEK + AES-256-GCM 加密                │
│   - 含 nonce + auth tag                                  │
└─────────────────────────────────────────────────────────┘
```

### 解锁流程

```
用户点击密码箱入口
    ↓
检查 Keystore 是否有生物识别绑定密钥
    ↓
是 → BiometricPrompt 弹出（指纹/面部）
    ↓ 验证通过
Keystore 解锁 MK → 解密 VEK → VEK 存内存 → 显示列表
    ↓
否（首次/无生物识别）→ 要求设置主密码
    ↓
主密码 → PBKDF2(sha256, 600000 iters, salt) → MK
    ↓ MK
生成 VEK → 加密 VEK 存本地 → 加密空条目库
    ↓
设置完成
```

### 超时锁定
- VEK 在内存保留 **5 分钟**（可配置），超时自动清零并锁屏
- 切到后台 30 秒后自动锁定
- 可手动一键锁定

---

## 📦 数据模型

### 客户端本地存储（通过 cap-keystore 插件）

```typescript
interface VaultBlob {
  version: 1;                    // 格式版本
  vekCiphertext: string;         // base64, MK 加密的 VEK
  vekParams: { iv: string; tag: string };
  mkDerivation: {
    algorithm: "PBKDF2" | "BIOMETRIC";
    iterations: 600000;          // PBKDF2
    salt: string;                // base64
  };
  entries: VaultEntryEncrypted[];
  updatedAt: string;
}

interface VaultEntryEncrypted {
  id: string;
  ciphertext: string;            // VEK 加密的 VaultEntry 明文 JSON
  iv: string;
  tag: string;
  updatedAt: string;
}

// 解密后的明文（仅内存）
interface VaultEntry {
  id: string;
  title: string;                 // "Gmail"、"公司VPN"
  username: string;
  password: string;
  url?: string;
  notes?: string;
  category?: "login" | "card" | "note" | "identity";
  totpSecret?: string;           // 2FA TOTP 种子
  customFields?: { key: string; value: string }[];
  createdAt: string;
  updatedAt: string;
}
```

### 服务端（pocketd）同步存储

**仅存密文**。pocketd 无法解密任何条目。

```sql
CREATE TABLE vault_sync (
    user_id TEXT NOT NULL,
    blob_ciphertext TEXT NOT NULL,   -- 整个 VaultBlob 的二次加密（用用户公钥）
    version INTEGER NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, version)
);
```

跨设备同步用**非对称加密**：每个用户一对密钥对（私钥用 MK 加密存本地，公钥上传），同步 blob 用对方公钥加密。

---

## 🔬 威胁模型

| 威胁 | 资产 | 缓解 |
|------|------|------|
| 设备丢失/被盗 | 本地密码库 | Keystore 硬件保护 + 生物识别锁定；本地文件即使提取也是密文 |
| APP 沙箱被突破 | VaultBlob 文件 | 文件用 MK 加密；MK 不落盘，仅在 Keystore（TEE/StrongBox） |
| 服务端被入侵 | 同步密文 | 端到端加密，服务端无私钥；blob 用用户公钥加密 |
| 中间人攻击 | 同步流量 | HTTPS + 证书绑定；同步 payload 本身已加密 |
| 恶意 APP 读取剪贴板 | 复制的密码 | 密码复制 30 秒后自动清空剪贴板 |
| 屏幕录制/截屏 | 密码显示 | 密码字段默认隐藏（•••），flag_secure 阻止截屏 |
| 生物识别被伪造 | 解锁 | 依赖 Android BiometricPrompt 的硬件级活体检测（Class 3 生物识别） |
| 暴力破解主密码 | MK | PBKDF2 600000 迭代 + 强制主密码强度 + 错误 5 次锁定 5 分钟 |
| 内存 dump | VEK | 使用即清零；用 `byte[]` 而非 String 存敏感数据，便于 GC |
| 离线肩窥 | 屏幕内容 | 敏感字段遮罩；自动锁定超时 |

---

## 🧬 生物识别集成

### Android BiometricPrompt（通过 Capacitor 插件）

- 仅使用 **Class 3（强）生物识别**（拒绝 Class 2 弱识别）
- 关联 **CryptoObject**：生物识别验证直接绑定到一个 `Cipher`（Keystore 私钥初始化），确保"只有本人生物特征才能完成解密"，而非"验证生物后程序自己解密"——防 hook 攻击
- 首次设置时，要求用户**同时设置主密码**作为生物识别不可用时的兜底

### Keystore 密钥属性
- `setUserAuthenticationRequired(true)`：必须生物识别才能用
- `setInvalidatedByBiometricEnrollment(true)`：新增指纹后密钥失效，需重设（防他人录指纹）
- StrongBox 优先（若设备支持），否则 TEE
- 密钥有效期与生物识别绑定

---

## 🔧 Capacitor 插件设计（cap-keystore）

### 接口

```typescript
interface CapKeystorePlugin {
  // 初始化/解锁
  isVaultInitialized(): Promise<boolean>;
  setupMasterPassword(password: string): Promise<void>;
  unlockWithBiometric(): Promise<void>;
  unlockWithPassword(password: string): Promise<boolean>;  // 返回成功/失败
  lock(): Promise<void>;

  // 条目操作（解锁后）
  listEntries(): Promise<VaultEntryMeta[]>;      // 不含密码明文，需单独取
  getEntry(id: string): Promise<VaultEntry>;
  saveEntry(entry: VaultEntry): Promise<string>;  // 返回 id
  deleteEntry(id: string): Promise<void>;

  // 同步
  exportEncryptedBlob(): Promise<string>;          // 用于上传同步
  importEncryptedBlob(blob: string): Promise<void>;

  // 生成器
  generatePassword(opts: { length: number; upper: boolean; lower: boolean; digits: boolean; symbols: boolean }): Promise<string>;
  evaluateStrength(password: string): Promise<{ score: 0|1|2|3|4; feedback: string }>;
}
```

### 原生实现要点（Android，Kotlin）
- AES-256-GCM（`javax.crypto.Cipher`）
- PBKDF2WithHmacSHA256，迭代 600000
- Keystore：`AndroidKeyStore` provider，`KeyGenParameterSpec` 配置如上
- BiometricPrompt + BiometricPrompt.CryptoObject 绑定 Cipher
- 文件存 app 私有目录 `filesDir/vault/vault.blob`，权限 `MODE_PRIVATE`
- 截屏防护：Activity `FLAG_SECURE`

---

## 🔄 跨设备同步流程

```
设备 A 新增条目
    ↓
本地 VEK 加密 → 更新 VaultBlob
    ↓
用同步密钥对私钥签名 + 用自己公钥加密整个 blob
    ↓
POST /api/vault/sync  (pocketd，Go ServeMux 注册为子树 /api/vault/sync/)
    ↓
pocketd 存密文 + 推送 vault.synced (WebSocket)
    ↓
设备 B 收到推送
    ↓
GET /api/vault/sync/latest （同一子树，末段 latest 区分）
    ↓
用自己私钥解密 → 用 MK 验证 → 合并到本地（按 updatedAt 取新；同条目冲突保留两版让用户选择）
```

> **路由实现说明**：Go stdlib `ServeMux` 用 `/api/vault/sync/`（带尾斜杠）注册为子树匹配，handler 内按 `r.Method`（POST 上传 / GET 拉取）和末段路径（`latest`）分发。`vault/store.go` 的 schema 也已调整为支持多版本（`is_current` 标记），以实现冲突双版本保留，而非早期的整体覆盖。

**冲突处理**：按 `updatedAt` 取新；同条目冲突保留两版让用户选择。

---

## 🛠️ 密码生成器与强度评估

### 生成器
- 默认 20 位，含大小写+数字+符号，排除易混字符（0/O/l/1）
- 可配置：长度 8-64，字符集开关，排除字符
- 可生成：密码、口令短语（diceware 中文词表）、PIN

### 强度评估
- zxcvbn 算法（Dropbox），0-4 分
- 反馈：识别常见模式、字典词、重复序列
- 实时显示在输入框旁

---

## 🚫 不做的事（明确边界）

- **不做浏览器扩展自动填充**（首版），仅支持 APP 内复制
- **不做团队共享**（个人密码箱）
- **不做云备份恢复码**之外的找回机制——主密码丢失=数据不可恢复（这是安全设计，不是缺陷）
- **不在 root 设备上运行**：启动检测 root，警告并建议退出

---

## 📅 实施计划

1. **第 4 周上半**：cap-keystore 插件（Keystore + AES + PBKDF2 + 生成器）
2. **第 4 周下半**：生物识别解锁 + VaultUnlockView
3. **第 5 周上半**：条目 CRUD UI + 列表 + 详情
4. **第 5 周下半**：跨设备同步（加密 blob 上传/下载/合并）
5. **第 5 周末**：截屏防护、剪贴板自动清除、超时锁定、root 检测

---

## 🔍 验收清单

- [ ] 锁屏后内存中无 VEK 残留（内存 dump 测试）
- [ ] 提取 vault.blob 文件无法解密（离线暴力破解测试）
- [ ] 服务端数据库被 dump 不泄露任何明文
- [ ] 生物识别新增指纹后旧密钥失效
- [ ] 主密码连续错误 5 次锁定 5 分钟
- [ ] 截屏密码字段为黑屏
- [ ] 复制密码 30 秒后剪贴板清空
- [ ] PBKDF2 迭代 ≥ 600000
