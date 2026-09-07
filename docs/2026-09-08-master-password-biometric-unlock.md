# 主密码解锁：已绑定生物认证可免密认证

主密码用于解锁本地加密库（SQLCipher + 共享 AES-GCM），与登录密码相互独立。
冷启动后 token 仍在、龙虾未初始化时，会停留在登录页的**解锁屏**。

## 用户流程

| 条件 | 操作 | 结果 |
|------|------|------|
| 未绑定指纹/人脸 | 必须输入主密码，点「解锁」 | `initLobster(主密码)` |
| 已绑定，密码为空 | 点「认证」 | 弹出系统指纹/人脸；通过后解密本机主密码密文并解锁 |
| 已绑定，用户取消系统弹窗 | 取消 | 留在解锁屏，不报错；可再点认证或改输主密码 |
| 已绑定且输入了主密码 | 点「解锁」 | 走密码路径，不再弹生物认证；成功后补写主密码密文 |

系统弹窗的 Negative 按钮文案是「取消」，认证器为 `BIOMETRIC_WEAK`（指纹与人脸均可）。

## 存储

Android `BiometricAuthPlugin` 在同一 `SharedPreferences` 里放两份密文：

- `cred_blob`：登录 `username\0password`（绑定/登录仍要弹 BiometricPrompt）
- `master_blob`：主密码明文的 AES-GCM 密文

写入 `master_blob` 不弹窗：只在用户刚刚用正确主密码创建或解锁成功之后发生。
读取 `master_blob` 必须先过 BiometricPrompt。解绑登录凭据时两份一起清除。

主密码不能复用登录密码字段。旧绑定只有 `cred_blob` 时，第一次免密认证会提示先用主密码解锁一次。

## 前端入口

- 策略：`frontend/src/features/auth/unlock-auth.ts`
- 解锁屏：`frontend/src/features/auth/LoginView.vue`（`needUnlock`）
- 首次创建后补写：`frontend/src/features/auth/MasterPasswordDialog.vue`
- 桥：`saveMasterSecret` / `getMasterSecret` / `hasMasterSecret`

Web / HarmonyOS Phase A 无此插件，解锁屏保持「必须输入主密码」。
