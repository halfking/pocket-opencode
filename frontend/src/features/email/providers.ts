/**
 * 邮箱服务商目录：新增向导只从这里选，不从已有账户里挑。
 * 授权码必须用户在官方网页生成；我们只打开说明页并预填 IMAP/SMTP。
 */
export type EmailProviderId = 'exmail' | 'qq' | '163' | 'gmail' | 'outlook' | 'other'

export interface EmailProvider {
  id: EmailProviderId
  label: string
  hint: string
  domains: string[]
  imapHost: string
  imapPort: number
  smtpHost: string
  smtpPort: number
  authCodeRequired: boolean
  authCodeUrl: string
  authCodeSteps: string[]
}

export const EMAIL_PROVIDERS: EmailProvider[] = [
  {
    id: 'exmail',
    label: '腾讯企业邮',
    hint: '公司域名，如 @kxpms.cn',
    domains: ['exmail.qq.com'],
    imapHost: 'imap.exmail.qq.com',
    imapPort: 993,
    smtpHost: 'smtp.exmail.qq.com',
    smtpPort: 465,
    authCodeRequired: true,
    authCodeUrl: 'https://exmail.qq.com/login',
    authCodeSteps: [
      '用浏览器登录企业邮网页版',
      '打开设置 → 客户端专用密码 / 安全设置',
      '生成客户端专用密码（不要填网页登录密码）',
      '回到本页粘贴该密码，再测试连接',
    ],
  },
  {
    id: 'qq',
    label: 'QQ 邮箱',
    hint: '@qq.com',
    domains: ['qq.com'],
    imapHost: 'imap.qq.com',
    imapPort: 993,
    smtpHost: 'smtp.qq.com',
    smtpPort: 465,
    authCodeRequired: true,
    authCodeUrl: 'https://help.mail.qq.com/detail/0/985',
    authCodeSteps: [
      '打开 QQ 邮箱网页版并登录',
      '设置 → 账号与安全 → 安全设置，开启 IMAP/SMTP',
      '点击「生成授权码」，按提示验证',
      '把 16 位授权码粘贴到本页（不是 QQ 密码）',
    ],
  },
  {
    id: '163',
    label: '163 / 网易邮箱',
    hint: '@163.com @126.com',
    domains: ['163.com', '126.com', 'yeah.net'],
    imapHost: 'imap.163.com',
    imapPort: 993,
    smtpHost: 'smtp.163.com',
    smtpPort: 465,
    authCodeRequired: true,
    authCodeUrl: 'https://help.mail.163.com/faqDetail.do?code=d7a5dc8471cd0c0e8b4b8f4f8e49998b374173cfe9171305fa1ce630d7f67ac2eda07326646e6eb0',
    authCodeSteps: [
      '登录 163 网页邮箱，设置里开启 IMAP/SMTP',
      '生成客户端授权码（不是登录密码）',
      '粘贴授权码后保存；收信时服务端会自动发 IMAP ID 头',
      '官方说明页可对照「不安全登录 / Unsafe Login」',
    ],
  },
  {
    id: 'gmail',
    label: 'Gmail',
    hint: '@gmail.com',
    domains: ['gmail.com', 'googlemail.com'],
    imapHost: 'imap.gmail.com',
    imapPort: 993,
    smtpHost: 'smtp.gmail.com',
    smtpPort: 465,
    authCodeRequired: true,
    authCodeUrl: 'https://myaccount.google.com/apppasswords',
    authCodeSteps: [
      'Google 账号需先开启两步验证',
      '打开应用专用密码页并生成一枚',
      '把 16 位应用专用密码粘贴到本页',
    ],
  },
  {
    id: 'outlook',
    label: 'Outlook',
    hint: '@outlook.com @hotmail.com',
    domains: ['outlook.com', 'hotmail.com', 'live.com'],
    imapHost: 'outlook.office365.com',
    imapPort: 993,
    smtpHost: 'smtp.office365.com',
    smtpPort: 587,
    authCodeRequired: true,
    authCodeUrl: 'https://account.live.com/proofs/AppPassword',
    authCodeSteps: [
      '登录 Microsoft 账号安全页',
      '生成应用密码（部分租户需管理员允许 IMAP）',
      '把应用密码粘贴到本页',
    ],
  },
  {
    id: 'other',
    label: '其他 IMAP',
    hint: '手动填写服务器',
    domains: [],
    imapHost: '',
    imapPort: 993,
    smtpHost: '',
    smtpPort: 465,
    authCodeRequired: false,
    authCodeUrl: '',
    authCodeSteps: ['向服务商确认 IMAP/SMTP 主机、端口和是否需要授权码'],
  },
]

export function providerById(id: EmailProviderId): EmailProvider {
  const found = EMAIL_PROVIDERS.find((p) => p.id === id)
  if (!found) throw new Error(`unknown email provider: ${id}`)
  return found
}

export function inferProviderId(email: string): EmailProviderId {
  const domain = email.split('@')[1]?.toLowerCase() ?? ''
  if (!domain) return 'other'
  for (const p of EMAIL_PROVIDERS) {
    if (p.id === 'other') continue
    if (p.domains.some((d) => domain === d || domain.endsWith(`.${d}`))) return p.id
  }
  return 'other'
}

export function isLocalTestAddress(email: string): boolean {
  return email.trim().toLowerCase().endsWith('.local')
}

export function openAuthCodePage(url: string): void {
  if (!url) return
  window.open(url, '_blank', 'noopener,noreferrer')
}
