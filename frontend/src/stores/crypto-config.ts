import { defineStore } from 'pinia'
import { ref } from 'vue'
import { preserveCorruptRaw } from '../utils/corrupt-preserve'

export type FieldEncryptionMode = 'disabled' | 'enabled'

export interface CryptoConfig {
  fieldEncryption: FieldEncryptionMode
  hasMasterPassword: boolean
  passwordHint: string | null
  updatedAt: number
}

const STORAGE_KEY = 'pocket_crypto_cfg'

const DEFAULT_CONFIG: CryptoConfig = {
  fieldEncryption: 'disabled',
  hasMasterPassword: false,
  passwordHint: null,
  updatedAt: 0,
}

/** §859/D26：读失败置位，供界面如实告知（不参与任何业务判定）。 */
const configRecovered = ref(false)

/**
 * ⚠⚠§859/D26：解析失败**不能**被折叠成「从来没有主密码」。
 *
 * 原文是 `catch { return { ...DEFAULT_CONFIG } }` ⇒ 读失败与「首次安装」是同一个值。
 * 而 `hasMasterPassword` 决定三件事：
 *   · LoginView/RegisterView 登录后是否弹主密码对话框（`if (!cfg.hasMasterPassword)`）；
 *   · MasterPasswordDialog 的标题（`mode="create"` ⇒ **「创建主密码」**）；
 *   · 下一批笔记是否做字段加密（`shouldEncryptField()`）。
 * ⇒ 用户明明早就设过主密码，却会在每次登录后看到一个
 *   **标题写着「创建主密码」、提示写着「丢失后无法恢复」** 的对话框；
 *   而 `initLobster` 第一步就是 `verifyMasterPassword`（`lobster-init.ts:76`）
 *   ⇒ 输旧密码以外的任何值都只会得到「主密码不正确」
 *   ⇒ **他被卡在「创建 → 不正确 → 再创建」的循环里，且无从知道真实原因是配置损坏**。
 *
 * 修法（同 §858/D21 的形态，但**不能照抄**：这里的正确姿态不是「新建」而是「解锁」）：
 *   ① 留证：原始串另存到 `pocket:crypto:corrupt:*`；
 *   ② 不谎报：raw 存在说明这台机器**曾经有过**一份加密配置
 *      ⇒ 置 `hasMasterPassword: true`（按「锁定」处理，而不是按「没有」处理）。
 *      这一步是安全的：`verifyMasterPassword` 才是真正的判据，
 *      错密码一定会被它挡住（web fallback 下返回 true 也不会造成数据损坏，
 *      因为那条路径上库本来就没有用主密码加密）。
 *   ③ 置 `configRecovered`，由界面如实告知（见 MasterPasswordDialog 的 `unlock` 模式）。
 *
 * ⚠ `fieldEncryption` 仍回落 'disabled'：损坏前它是 enabled 的话，
 *   旧行照旧带着 `encrypted_content=1` 照常解密，新行会写成明文。
 *   这属于「用户该选哪个默认值」的产品口径，**本节不代拍**（已登记）。
 */
function loadConfig(): CryptoConfig {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { ...DEFAULT_CONFIG }
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') throw new Error('shape')
    return {
      ...DEFAULT_CONFIG,
      ...parsed,
      // 字段级校验：`as CryptoConfig` 只对编译器说话，
      // 而 localStorage 是跨边界输入（§859/D22 同一族）。
      fieldEncryption: parsed.fieldEncryption === 'enabled' ? 'enabled' : 'disabled',
      hasMasterPassword: parsed.hasMasterPassword === true,
    }
  } catch {
    const raw = (() => {
      try { return localStorage.getItem(STORAGE_KEY) } catch { return null }
    })()
    preserveCorruptRaw('pocket:crypto:corrupt', 'config', raw)
    configRecovered.value = true
    return { ...DEFAULT_CONFIG, hasMasterPassword: true }
  }
}

export const useCryptoConfig = defineStore('crypto-config', () => {
  const cfg = ref<CryptoConfig>(loadConfig())

  /**
   * ⚠⚠§866/D31：原文**没有 try/catch** ⇒ 配额满/隐私模式下 `setItem` 抛错，
   * 而它唯一的调用者之一是 `MasterPasswordDialog::submit`：
   *   initLobster（已成功）→ setMasterPassword → save() 抛
   *   ⇒ catch 里显示的是「本地数据初始化失败」，
   *   **但初始化其实成功了**，真正失败的是「这一步没存住」；
   *   而且 `hasMasterPassword` 没落盘 ⇒ 下次启动又回到 §862/D26 那个
   *   「创建主密码」的对话框（用户会以为系统从没保存过他的主密码）。
   *
   * ⇒ 修法：写失败**自己承担**（不冒泡给一个与它无关的错误出口），
   *   留 warn + 置位，让界面有机会说「设置已生效但没能保存」。
   */
  const persistFailed = ref(false)

  function save() {
    cfg.value.updatedAt = Date.now()
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(cfg.value))
      persistFailed.value = false
    } catch (err) {
      persistFailed.value = true
      console.warn('[crypto-config] 加密配置写入失败（存储满或不可用）：', err)
    }
  }

  function setFieldEncryption(mode: FieldEncryptionMode) {
    cfg.value.fieldEncryption = mode
    save()
  }

  function setMasterPassword(hint: string | null = null) {
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
    configRecovered,
    persistFailed,
    setFieldEncryption,
    setMasterPassword,
    resetMasterPassword,
    shouldEncryptField,
  }
})
