/**
 * 动态图标注册表 —— 「material-symbols 子集里到底必须有谁」的唯一权威来源。
 *
 * ## 为什么需要它
 *
 * material-symbols 的图标是 **ligature（连字）**：模板里写 `<span class=
 * "material-symbols-outlined">light_mode</span>`，浏览器把这串字符合成为一个字形。
 * 字体里没有这个连字，它就退化成普通文本，直接显示成 `LIGHT_MODE`。
 * 这个 bug 在真机上发生过（SettingsView 主题三选项）。
 *
 * `build-material-symbols-subset.mjs` 靠正则扫源码来决定裁哪些连字。但正则只能看见
 * **模板里的字面量**。下面这三类引用它原理上看不见，而它们在仓库里真实存在：
 *
 *   1. **数据表**：`icon: 'forum'`，模板写 `{{ item.icon }}`
 *   2. **computed / 函数早返回**：`const iconName = computed(() => {
 *      if (x) return 'notifications_active' })`（SessionStatusBar）
 *   3. **map 表 + 兜底**：`const map = { calculate: 'calculate' }; return map[k] ?? 'handyman'`
 *      （ToolCallCard）
 *
 * 还有一个正则层面的洞：**跨行插值**。`build` 脚本按行匹配
 * `material-symbols-outlined"…>\s*\{\{([^}]*)\}\}`，遇到
 * `<span …>{{\n  cond ? 'person' : 'psychology'\n}}</span>`（AIChatView.vue:59）
 * 就整条漏掉——`{{` 与 `}}` 不在同一行。
 *
 * 这些名字今天能正常显示，**只是因为它们恰好被裁进去了**（当前字体 3.52 MB /
 * 原始 3.80 MB，只削掉 7.3%，几乎等于全量）。一旦子集真的裁小，或者有人清理
 * FALLBACK 里的冗余项，它们立刻变豆腐块，而且**没有任何检查会报警**。
 *
 * ## 契约
 *
 * - 新增动态图标必须先在这里登记，`check-icon-registry.mjs` 会拦。
 * - 这里登记的每个名字都由 `check-icon-font.mjs` 对**已提交的字体文件**做 ligature
 *   成形实测——不是比对名字集合，是真的确认「这个名字能合成一个字形」。
 * - 模板里直接写字面量（`<span class="material-symbols-outlined">send</span>`）
 *   不需要登记，构建脚本扫得到。
 *
 * @see scripts/check-icon-registry.mjs 静态登记完整性
 * @see scripts/check-icon-font.mjs     字体连字实测
 */

/** 按来源分组的动态图标名。key 只用于可读性，运行时用的是 value。 */
export const ICON = {
  // ── features/settings/SettingsView.vue：主题三选项 ──
  // 历史上「图标从模板字面量搬进数据表」导致这三个丢字，是本文件存在的主要理由。
  themeAuto: 'brightness_auto',
  themeLight: 'light_mode',
  themeDark: 'dark_mode',

  // ── features/more/MoreHubView.vue：导航网格与市场入口 ──
  moreForum: 'forum',
  moreNotes: 'sticky_note_2',
  moreMail: 'mail',
  moreRss: 'rss_feed',
  moreLock: 'lock',
  moreExtension: 'extension',
  moreSmartToy: 'smart_toy',
  moreMemory: 'memory',
  moreHandshake: 'handshake',
  morePayments: 'payments',
  moreChecklist: 'checklist',
  moreNotifications: 'notifications',
  moreImportExport: 'import_export',
  moreDns: 'dns',

  // ── components/base/SettingsMenuDrawer.vue ──
  settingsModel: 'model_training',
  settingsSchedule: 'schedule',
  settingsPrivacy: 'privacy_tip',
  settingsGear: 'settings',

  // ── components/BottomNav.vue ──
  // 2026-10-03 全局 IA 重组后一级 tab 收敛为 4 个：首页 / 笔记 / 消息 / 更多。
  // 这里的四个名字**只覆盖走数据表的写法**；BottomNav 的 items 数组目前是裸字符串
  // 字面量（'home' / 'edit_note' / 'notifications' / 'apps'），由 check:icons 兜底。
  // 早先在这里额外登记 navNotes / navMessages 是多余的——它们一个引用都没有，
  // 反而给人「已集中登记」的错觉。动态图标登记表的规则是**有引用才登记**。
  navHome: 'home',
  navApps: 'apps',
  navStyle: 'style',
  navMic: 'mic',

  // ── features/notes/NotesHubView.vue：统一流的来源图标（switch 早返回）──
  hubSourceNote: 'edit_note',
  hubSourceMeeting: 'event',
  hubSourcePkm: 'sticky_note_2',

  // ── features/messages/MessagesHubView.vue：统一时间线的来源图标 ──
  msgSourceEmail: 'mail',
  msgSourceRss: 'rss_feed',
  msgSourceTask: 'notifications',

  // ── features/sessions/useSessionDrafts.ts：草稿类型 ──
  draftPlay: 'play_arrow',
  draftMerge: 'merge',
  draftScore: 'sports_score',
  draftSubject: 'subject',
  draftScience: 'science',
  draftFastForward: 'fast_forward',
  draftCloudDownload: 'cloud_download',
  draftStop: 'stop',

  // ── features/sessions/SessionComposer.vue：斜杠命令兜底 ──
  slashBolt: 'bolt',

  // ── features/flashcards/FlashcardEditView.vue：模板类型 ──
  cardCompare: 'compare_arrows',
  cardAutoMotion: 'auto_awesome_motion',

  // ── features/study/StudyHubView.vue ──
  // 分区行图标（数据表）
  studyArchive: 'archive',
  studyPsychology: 'psychology',
  // sourceIcon(kind)：switch 早返回，模板只写 {{ sourceIcon(item.sourceKind) }}
  studySourceNote: 'edit_note',
  studySourceEmail: 'send',
  studySourceRss: 'rss_feed',
  studySourceMeeting: 'event',
  studySourceChat: 'chat_bubble',
  studySourceDefault: 'label',

  // ── features/sessions/SessionStatusBar.vue：状态信号三态 ──
  // computed + if 早返回，三个分支里两个此前从未被任何脚本收集过。
  statusApproval: 'notifications_active',
  statusRunning: 'progress_activity',
  statusIdle: 'play_arrow',

  // ── features/local-agent/ToolCallCard.vue：工具名 → 图标 map 表 ──
  toolCalculate: 'calculate',
  toolDeviceInfo: 'smartphone',
  toolHttpFetch: 'public',
  toolReadFile: 'draft',
  toolWriteFile: 'edit_document',
  toolListFiles: 'folder_open',
  toolHandyman: 'handyman',
  toolCurrentTime: 'schedule',
  toolLoadSkill: 'bolt',
  toolTaskPlan: 'checklist',

  // ── features/calendar/types.ts：SOURCE_META 是数据表，模板写 {{ }}，扫不到 ──
  // 这三个是日历里三类条目的图标。缺任何一个 → 字体子集把它裁掉 →
  // 真机显示成 CALENDAR_MONTH 这样的字面量而不是图标。
  calendarSourceEvent: 'event',
  calendarSourceTask: 'task_alt',
  calendarSourceScheduled: 'schedule',
  // ── features/messages/MessagesHubView.vue：顶部「时间线｜日历」分段控件 ──
  // 动态名，由 ViewSegmentBar 渲染成 {{ opt.icon }}，静态扫描抓不到，必须登记。
  // 原来叫 moreCalendar（More 宫格里的日历入口）。日历搬进「消息」tab 后
  // More 里那个入口被删掉，常量名跟着搬家——否则它会变成没人引用的死常量，
  // 而死常量正是「文档/注册表说有、代码里其实没接线」的最灵敏探针。
  viewCalendar: 'calendar_month',
  viewTimeline: 'notifications',

  // ── features/ai-chat/AIChatView.vue：跨行插值，正则扫不到 ──
  chatAgent: 'person',
} as const

/**
 * 任一动态图标的字面量类型。
 *
 * **覆盖范围要说准**：只有把字段显式标注成 `IconName`（或 `: Record<string, IconName>`）
 * 的地方，写错名字才会在 `vue-tsc` 阶段报错。目前已标注的只有 3 个组件
 * （`SessionStatusBar` / `ToolCallCard` / `StudyHubView`）——
 * 它们的图标是 computed/map/switch 里的返回值，最容易写错也最难被扫描器发现。
 *
 * 其余数据表（`MoreHubView` / `SettingsView` / `BottomNav` / `SettingsMenuDrawer` /
 * `useSessionDrafts` / `FlashcardEditView` / `SessionComposer`）目前仍是裸字符串，
 * 它们由**两个门禁脚本**兜底（`check:icons`），而不是由类型兜底。
 * 把它们也标注成 `IconName` 是后续可做的加固。
 */
export type IconName = (typeof ICON)[keyof typeof ICON]

