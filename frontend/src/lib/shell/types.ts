/**
 * shell/types.ts — Hyper 运行时共享类型。
 *
 * 背景：Hyper = 页面与层级导航上下文 + 滚动/手势/专注运行时 + 受控原生能力。
 * 本目录（lib/shell）是这三样东西的 Web 侧实现，**不承载业务逻辑**：
 * 金额计算、写入、鉴权、审计继续由既有后端与业务层裁决。
 *
 * 协议边界（对应 UI规范 06/07/08）：
 *   - `hyperMode` 是交互形态开关，**不等于**具备原生能力；
 *   - `capabilities` 才证明原生能力可用，见 capabilities.ts。
 *
 * 这些类型描述的是**当前数据形状**，不是对现有 router meta 的猜测；
 * 每个字段的语义见 UI规范 06 §3。
 */

/** 呈现形态：页面、模态、底部面板、专注全屏工作区。 */
export type Presentation = 'page' | 'modal' | 'sheet' | 'focus'

/** 导航动作种类。`present` 表示由覆盖层登记而非路由跳转触发。 */
export type NavigationKind = 'push' | 'replace' | 'pop' | 'deepLink' | 'restore' | 'present'

/** 标题来源。用于诊断「标题为什么是这个」，不参与裁决。 */
export type TitleSource = 'registered' | 'aria' | 'dom' | 'inherited' | 'route' | 'document'

/** 标题文本的最大长度。超长截断，避免把整段正文塞进顶栏。 */
export const MAX_TITLE_LENGTH = 200

/**
 * scope：导航条目归属的账号域。
 * 登出/换账号/换服务器必须整体清空，否则会把上一人的标题与筛选带过来。
 */
export interface NavigationScope {
  serverId: string
  accountId: string
  projectId?: string
}

/** 滚动快照：x/y + 首个可见行锚点。只存 id，不存业务行内容。 */
export interface ScrollAnchor {
  x: number
  y: number
  anchorId?: string
}

/** 视图快照：Tab、筛选引用、列表引用、滚动、专注目标。 */
export interface NavigationView {
  tabId?: string
  /** 白名单筛选缓存引用；不存敏感正文。 */
  filterRef?: string
  /** 列表缓存引用；业务行不进导航历史。 */
  listRef?: string
  /** 按稳定 scroll id 存：页面主体、当前 Tab、弹层 body、表格横轴。 */
  scroll: Record<string, ScrollAnchor>
  focusTarget?: string
}

/** 导航条目：一次「打开」就是一条，同一个 URL 多次打开也有独立 id。 */
export interface NavigationEntry {
  id: string
  /** 打开流程的父条目；不凭路径层级猜来源。 */
  parentId?: string
  /** 运行时完整路径。持久化时只保留白名单 query。 */
  fullPath: string
  routeName?: string
  presentation: Presentation
  openedBy: NavigationKind
  title: string
  titleSource: TitleSource
  titleKey?: string
  /** 无标题弹层沿用的父条目 id。 */
  inheritedFrom?: string
  scope: NavigationScope
  view: NavigationView
  historyPosition?: number
  createdAt: number
}

/** 操作环条目：记录发生了什么、是否真的落地。 */
export interface NavigationOperation {
  seq: number
  entryId: string
  type: string
  timestamp: number
  outcome: 'committed' | 'cancelled' | 'failed'
}

/** 导航上下文：v2 schema。 */
export interface NavigationContext {
  version: 2
  entries: NavigationEntry[]
  cursor: number
  overlayIds: string[]
  operations: NavigationOperation[]
}

/** 页面历史上限。沿用 nbjl3 v1 的 80 条。 */
export const MAX_PAGE_ENTRIES = 80
/** 操作环上限。沿用 v1 的 100 条。 */
export const MAX_OPERATIONS = 100
/** 持久化用的 sessionStorage key。 */
export const STORAGE_KEY = 'hyper.navigation.v2'
/** v1 兼容读取 key：读得到就迁移一次，之后只写 v2。 */
export const LEGACY_STORAGE_KEY = 'hyper.navigation.v1'

/** 允许持久化到 URL query 的白名单键。其余（搜索原文、秘密）一律不落盘。 */
export const PERSISTED_QUERY_WHITELIST = ['tab', 'page', 'month', 'project', 'filter', 'sort', 'scope']

/** 返回决策结果。`consumed` 必须在所有分支都给出——未消费的返回会穿透到路由。 */
export type BackOutcome =
  | { kind: 'overlay-closed'; id: string }
  | { kind: 'overlay-rejected'; id: string; reason: string }
  | { kind: 'page-popped'; entryId: string }
  | { kind: 'blocked'; reason: string }
  | { kind: 'handed-to-system' }
  | { kind: 'noop'; reason: string }

/** 能力协商结果中的后台延续策略。绝不一概写「后台继续」。 */
export type ContinuationPolicy =
  | 'foregroundOnly'
  | 'bestEffort'
  | 'osScheduled'
  | 'activeAudio'
  | 'serverDurable'

/** 能力协商快照。字段存在不代表可用，可用为 false 就是不可用。 */
export interface HyperCapabilities {
  protocolVersion: 2
  platform: 'web' | 'ios' | 'android'
  navigation: boolean
  focusWorkspace: boolean
  continuousScroll: boolean
  tasks: {
    durableLocal: boolean
    cloudDetached: boolean
    continuation: ContinuationPolicy
  }
  recording: {
    available: boolean
    background: boolean
  }
  recognition: {
    pdfText: boolean
    ocr: 'none' | 'native' | 'model'
    asr: 'none' | 'native' | 'model'
  }
  agent: {
    available: boolean
    skillFormat: 'declarative-v1' | 'none'
  }
}
