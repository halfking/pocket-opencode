# 🎨 OpenCode Pocket UI/UX 设计系统

**版本**: v1.0.0  
**日期**: 2026-07-02  
**状态**: 设计规范  
**基于**: Material Design 3 + iOS HIG + 业界最佳实践

---

## 📋 执行摘要

本文档定义 OpenCode Pocket 的完整 UI/UX 设计系统，包括：
- 🎨 设计原则与视觉语言
- 🎯 导航结构与信息架构
- 🤝 交互模式与手势系统
- 🌈 颜色系统（亮色/暗色）
- 📏 间距与排版规范
- 🔄 动画与过渡规则
- 🧩 核心组件库

**设计目标**：
- 快速：核心操作 < 2 次点击
- 简洁：减少认知负担 30%
- 流畅：动画 60fps，过渡自然
- 一致：跨平台体验统一

---

## 一、🎯 设计原则

### 1.1 核心原则

#### **快速访问 (Quick Access)**
- 语音录入一键触达（底部 FAB）
- 最近会话置顶显示
- 智能快捷操作（3 个以内）

#### **极简界面 (Minimalism)**
- 去除非必要元素
- 内容优先，UI 次之
- 留白比例 30-40%

#### **智能反馈 (Intelligent Feedback)**
- AI 处理状态实时可见
- 语音识别结果即时显示
- 错误提示友好具体

#### **离线优先 (Offline First)**
- 骨架屏代替空白
- 缓存策略透明提示
- 优雅降级不阻塞

### 1.2 用户体验目标

| 指标 | 目标值 | 测量方式 |
|------|-------|---------|
| 启动速度 | < 1.5s | 首屏渲染完成 |
| 操作响应 | < 100ms | 点击到视觉反馈 |
| 页面转场 | 300-400ms | 页面切换动画 |
| 学习成本 | < 5min | 新用户上手时间 |
| 任务完成率 | > 95% | 核心任务成功率 |

---

## 二、🧭 导航结构

### 2.1 信息架构

```
OpenCode Pocket
├─ 🏠 主页 (Home)
│  ├─ 快速录音入口 (FAB)
│  ├─ 最近会话列表
│  ├─ 智能建议卡片
│  └─ 快捷操作面板
│
├─ 🎙️ 笔记 (Notes)
│  ├─ 全部笔记 (按时间/分类)
│  ├─ 工作区视图
│  ├─ 标签云
│  └─ 搜索与过滤
│
├─ 🎤 会议 (Meetings)
│  ├─ 进行中的会议
│  ├─ 历史会议记录
│  ├─ 声纹管理
│  └─ 会议设置
│
├─ 📨 邮箱 (Inbox)
│  ├─ 未读邮件
│  ├─ 重要邮件
│  ├─ 邮件分类
│  └─ 每日摘要
│
└─ ⋮ 更多 (More)
   ├─ 🔐 密码箱
   ├─ 💻 AI 工具 (实例/任务/会话)
   ├─ ⚙️ 设置
   └─ 👤 个人中心
```

### 2.2 底部导航栏

**设计规范**：
- 4 个主要入口 + 1 个"更多"
- 图标 + 文字标签
- 当前选中状态：图标实心 + 主色
- 高度：60dp (含 safe area)

**布局**：
```
┌────────────────────────────────────────┐
│  🏠      🎙️      🎤      📨      ⋮     │
│ 主页    笔记    会议    邮箱    更多    │
└────────────────────────────────────────┘
     60dp 高度 (底部 safe area + 8dp)
```

---

## 三、🎨 颜色系统

### 3.1 亮色模式 (Light Theme)

```css
/* 主色 (Primary) */
--color-primary: #667eea;              /* 品牌主色 */
--color-primary-light: #8b9ef5;        /* 悬停态 */
--color-primary-dark: #4d5fcf;         /* 按下态 */

/* 背景 (Background) */
--color-bg-base: #f7f7fa;              /* 页面背景 */
--color-bg-surface: #ffffff;           /* 卡片/表面 */
--color-bg-elevated: #ffffff;          /* 悬浮元素 */
--color-bg-overlay: rgba(0,0,0,0.4);   /* 遮罩 */

/* 文字 (Text) */
--color-text-primary: #111827;         /* 主要文字 */
--color-text-secondary: #6b7280;       /* 次要文字 */
--color-text-tertiary: #9ca3af;        /* 辅助文字 */
--color-text-disabled: #d1d5db;        /* 禁用文字 */
--color-text-inverse: #ffffff;         /* 反色文字 */

/* 边框 (Border) */
--color-border-default: #e5e7eb;       /* 默认边框 */
--color-border-light: #f3f4f6;         /* 浅边框 */
--color-border-strong: #d1d5db;        /* 强调边框 */

/* 功能色 (Functional) */
--color-success: #10b981;              /* 成功 */
--color-warning: #f59e0b;              /* 警告 */
--color-error: #ef4444;                /* 错误 */
--color-info: #3b82f6;                 /* 信息 */

/* 语义色 (Semantic) */
--color-voice-recording: #ef4444;      /* 录音中 */
--color-ai-thinking: #8b5cf6;          /* AI 思考 */
--color-link: #667eea;                 /* 链接 */
--color-tag: #f3f4f6;                  /* 标签背景 */
```

### 3.2 暗色模式 (Dark Theme)

```css
/* 主色 (Primary) - 提高亮度 */
--color-primary: #818cf8;              /* 品牌主色 */
--color-primary-light: #a5b4fc;        /* 悬停态 */
--color-primary-dark: #6366f1;         /* 按下态 */

/* 背景 (Background) - 避免纯黑 */
--color-bg-base: #0f0f14;              /* 页面背景 */
--color-bg-surface: #1a1a22;           /* 卡片/表面 */
--color-bg-elevated: #24242e;          /* 悬浮元素 */
--color-bg-overlay: rgba(0,0,0,0.6);   /* 遮罩 */

/* 文字 (Text) - 降低对比度 */
--color-text-primary: #f3f4f6;         /* 主要文字 */
--color-text-secondary: #9ca3af;       /* 次要文字 */
--color-text-tertiary: #6b7280;        /* 辅助文字 */
--color-text-disabled: #4b5563;        /* 禁用文字 */
--color-text-inverse: #111827;         /* 反色文字 */

/* 边框 (Border) */
--color-border-default: #2a2a35;       /* 默认边框 */
--color-border-light: #1f1f29;         /* 浅边框 */
--color-border-strong: #374151;        /* 强调边框 */

/* 功能色 (Functional) - 降低饱和度 */
--color-success: #34d399;              /* 成功 */
--color-warning: #fbbf24;              /* 警告 */
--color-error: #f87171;                /* 错误 */
--color-info: #60a5fa;                 /* 信息 */

/* 语义色 (Semantic) */
--color-voice-recording: #f87171;      /* 录音中 */
--color-ai-thinking: #a78bfa;          /* AI 思考 */
--color-link: #818cf8;                 /* 链接 */
--color-tag: #1f1f29;                  /* 标签背景 */
```

---

## 四、📏 间距与排版

### 4.1 间距系统 (Spacing Scale)

使用 4px 基础单位：

```css
--space-1: 4px;    /* 微小间距 */
--space-2: 8px;    /* 小间距 */
--space-3: 12px;   /* 中等间距 */
--space-4: 16px;   /* 标准间距 */
--space-5: 20px;   /* 大间距 */
--space-6: 24px;   /* 更大间距 */
--space-8: 32px;   /* 章节间距 */
--space-10: 40px;  /* 区块间距 */
--space-12: 48px;  /* 页面边距 */
--space-16: 64px;  /* 超大间距 */
```

### 4.2 排版系统 (Typography)

**字体家族**：
- 中文：PingFang SC / Noto Sans SC
- 英文：SF Pro / Inter / Roboto
- 代码：SF Mono / Fira Code

**字号系统**：
```css
--font-size-xs: 12px;    /* 辅助文字 */
--font-size-sm: 14px;    /* 次要文字 */
--font-size-base: 16px;  /* 正文 */
--font-size-lg: 18px;    /* 小标题 */
--font-size-xl: 20px;    /* 标题 */
--font-size-2xl: 24px;   /* 大标题 */
--font-size-3xl: 30px;   /* 页面标题 */
--font-size-4xl: 36px;   /* 特大标题 */
```

**字重系统**：
```css
--font-weight-regular: 400;  /* 正常 */
--font-weight-medium: 500;   /* 中等 */
--font-weight-semibold: 600; /* 半粗 */
--font-weight-bold: 700;     /* 粗体 */
```

**行高系统**：
```css
--line-height-tight: 1.25;   /* 紧凑 */
--line-height-normal: 1.5;   /* 正常 */
--line-height-relaxed: 1.75; /* 宽松 */
```

### 4.3 圆角系统 (Border Radius)

```css
--radius-sm: 4px;    /* 小圆角 - 按钮、标签 */
--radius-md: 8px;    /* 中圆角 - 卡片 */
--radius-lg: 12px;   /* 大圆角 - 面板 */
--radius-xl: 16px;   /* 超大圆角 - 模态框 */
--radius-full: 9999px; /* 完全圆角 - 头像、徽章 */
```

### 4.4 阴影系统 (Shadows)

```css
/* 亮色模式 */
--shadow-sm: 0 1px 2px 0 rgba(0, 0, 0, 0.05);
--shadow-md: 0 4px 6px -1px rgba(0, 0, 0, 0.1);
--shadow-lg: 0 10px 15px -3px rgba(0, 0, 0, 0.1);
--shadow-xl: 0 20px 25px -5px rgba(0, 0, 0, 0.1);

/* 暗色模式 */
--shadow-sm-dark: 0 1px 2px 0 rgba(0, 0, 0, 0.3);
--shadow-md-dark: 0 4px 6px -1px rgba(0, 0, 0, 0.4);
--shadow-lg-dark: 0 10px 15px -3px rgba(0, 0, 0, 0.5);
--shadow-xl-dark: 0 20px 25px -5px rgba(0, 0, 0, 0.6);
```

---

## 五、🤝 交互模式

### 5.1 手势系统

| 手势 | 触发区域 | 操作 | 反馈 |
|------|---------|------|------|
| **点击** | 按钮/卡片 | 主要操作 | 100ms 延迟 + 缩放 0.95 |
| **长按** | 列表项 | 进入选择模式 | 500ms 触发 + 触觉反馈 |
| **左滑** | 列表项 | 标记/收藏 | 滑动距离 > 30% 触发 |
| **右滑** | 列表项 | 删除/归档 | 滑动距离 > 30% 触发 |
| **下拉** | 列表顶部 | 刷新数据 | 下拉距离 > 60dp 触发 |
| **上拉** | 列表底部 | 加载更多 | 距底部 < 100dp 触发 |
| **双击** | 文本 | 选中单词 | 放大镜效果 |
| **捏合** | 图片/文本 | 缩放 | 平滑缩放动画 |

### 5.2 语音录音交互

**长按录音模式**（推荐）：
```
用户行为: 长按录音按钮
视觉反馈: 按钮放大 1.2x + 红色脉冲动画
触觉反馈: 50ms 震动
音频反馈: "滴" 声提示
实时反馈: 底部浮层显示转写文本
松开行为: 停止录音 + 50ms 震动
```

**点击录音模式**（备选）：
```
用户行为: 点击录音按钮
视觉反馈: 按钮变红 + "录音中" 标签
触觉反馈: 50ms 震动
松开行为: 再次点击停止
```

### 5.3 列表滑动操作

**左滑动作**（正向操作）：
- 🌟 标记为重要（黄色背景 #FFD60A）
- ✅ 标记为完成（绿色背景 #34C759）
- 📌 置顶（蓝色背景 #007AFF）

**右滑动作**（负向操作）：
- 🗑️ 删除（红色背景 #FF3B30）
- 📦 归档（灰色背景 #8E8E93）

---

## 六、🔄 动画与过渡

### 6.1 动画时长

```css
--duration-fast: 150ms;     /* 微交互 - 按钮点击 */
--duration-base: 250ms;     /* 标准 - 卡片展开 */
--duration-slow: 350ms;     /* 页面转场 */
--duration-slower: 500ms;   /* 复杂动画 */
```

### 6.2 缓动函数

```css
--ease-in: cubic-bezier(0.4, 0.0, 1, 1);           /* 加速 */
--ease-out: cubic-bezier(0.0, 0.0, 0.2, 1);        /* 减速 */
--ease-in-out: cubic-bezier(0.4, 0.0, 0.2, 1);     /* 先加速后减速 */
--ease-spring: cubic-bezier(0.68, -0.55, 0.265, 1.55); /* 弹簧 */
```

### 6.3 动画规则

**入场动画**（Entrance）：
- 淡入 (Fade In) + 向上移动 20px
- 时长：250ms
- 缓动：ease-out

**出场动画**（Exit）：
- 淡出 (Fade Out) + 缩小到 0.9x
- 时长：200ms
- 缓动：ease-in

**页面转场**（Page Transition）：
- 水平滑动（iOS 风格）
- 时长：350ms
- 缓动：ease-in-out

**微交互**（Micro-interaction）：
- 缩放反馈（按钮点击）
- 时长：150ms
- 缓动：ease-spring

---

## 七、🧩 核心组件

### 7.1 按钮 (Button)

**变体**：
- Primary：主要操作（品牌色填充）
- Secondary：次要操作（边框 + 透明）
- Ghost：辅助操作（纯文字）
- Danger：危险操作（红色）

**尺寸**：
- Large: 48dp 高度
- Medium: 40dp 高度
- Small: 32dp 高度

### 7.2 卡片 (Card)

**变体**：
- Default：默认卡片（白色背景 + 阴影）
- Outlined：边框卡片（无阴影）
- Elevated：悬浮卡片（更大阴影）

### 7.3 输入框 (Input)

**变体**：
- Filled：填充式（Material Design）
- Outlined：边框式
- Underlined：下划线式

### 7.4 列表项 (ListItem)

**结构**：
```
┌─────────────────────────────────┐
│ 🎯 [图标]  标题文字               │
│              副标题/描述           │
│              时间戳 · 标签     → │
└─────────────────────────────────┘
```

### 7.5 底部弹出框 (BottomSheet)

**用途**：
- 操作菜单（分享、删除、编辑）
- 选择器（单选、多选）
- 详情面板

**行为**：
- 从底部滑入
- 半屏/全屏两种模式
- 背景遮罩可点击关闭

### 7.6 Toast / Snackbar

**位置**：底部（距底部导航 80dp）
**时长**：3 秒自动消失
**类型**：
- Success（绿色）
- Error（红色）
- Info（蓝色）
- Warning（黄色）

---

## 八、🎯 关键页面设计

### 8.1 主页 (Home)

**布局**：
```
┌───────────────────────────────┐
│ 顶栏：问候语 + 头像           │
├───────────────────────────────┤
│ 快捷操作卡片（3个横向滚动）   │
├───────────────────────────────┤
│ 最近会话列表                  │
│ ┌─────────────────────────┐  │
│ │ 📝 笔记标题              │  │
│ │    摘要内容...           │  │
│ │    2 小时前 · 工作       │  │
│ └─────────────────────────┘  │
│                               │
│ [悬浮录音按钮]                │
└───────────────────────────────┘
│ 底部导航                      │
└───────────────────────────────┘
```

### 8.2 笔记详情

**布局**：
```
┌───────────────────────────────┐
│ 顶栏：← 返回  ...更多         │
├───────────────────────────────┤
│ 📝 笔记标题                   │
│ 2026-07-02 14:30 · 工作       │
├───────────────────────────────┤
│                               │
│ 笔记正文内容                  │
│ 支持 Markdown 渲染            │
│                               │
│ 🔗 关联笔记推荐               │
│ ┌──────────┐ ┌──────────┐    │
│ │ 相关笔记 │ │ 相关笔记 │    │
│ └──────────┘ └──────────┘    │
└───────────────────────────────┘
```

### 8.3 录音界面

**布局**：
```
┌───────────────────────────────┐
│                               │
│      [大圆形录音按钮]          │
│                               │
│  ━━━━━━━━━━━━━━━━━━━━━━━━   │
│       音频波形可视化           │
│  ━━━━━━━━━━━━━━━━━━━━━━━━   │
│                               │
│     00:15 / 05:00             │
│                               │
│  ┌─────────────────────────┐ │
│  │ 实时转写文本显示         │ │
│  │ "今天开会讨论了..."     │ │
│  └─────────────────────────┘ │
│                               │
│  [暂停] [完成]                │
└───────────────────────────────┘
```

---

## 九、♿ 可访问性 (Accessibility)

### 9.1 触摸目标

- 最小触摸目标：48x48dp (Material Design)
- 推荐触摸目标：56x56dp
- 间距：至少 8dp

### 9.2 对比度

- 正文文字：至少 4.5:1
- 大标题：至少 3:1
- 图标/图形：至少 3:1

### 9.3 语音辅助

- 所有交互元素添加 `accessibilityLabel`
- 图片添加 `accessibilityHint`
- 状态变化添加 `accessibilityLiveRegion`

---

## 十、📱 响应式设计

### 10.1 断点

```css
/* 手机竖屏 */
@media (max-width: 480px) { }

/* 手机横屏 */
@media (min-width: 481px) and (max-width: 767px) { }

/* 平板 */
@media (min-width: 768px) and (max-width: 1024px) { }

/* 桌面 */
@media (min-width: 1025px) { }
```

### 10.2 适配策略

- 手机：单列布局，底部导航
- 平板：两列布局，侧边导航
- 桌面：三列布局，顶部导航

---

## 🎉 总结

本设计系统遵循：
- ✅ Material Design 3 的现代化设计语言
- ✅ iOS HIG 的交互规范
- ✅ 业界最佳实践（Notion、Bear、Craft）
- ✅ 个人助理应用的特定需求

**下一步**：
1. 实现 CSS Tokens 文件
2. 创建 Vue 组件库
3. 编写 Storybook 文档
4. 集成到现有项目

---

**参考资料**：
- Material Design 3: https://m3.material.io/
- iOS HIG: https://developer.apple.com/design/human-interface-guidelines/
- Ant Design Mobile: https://mobile.ant.design/
