> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/2026-08-27-mobile-ux-design-v2.md`](docs/2026-08-27-mobile-ux-design-v2.md)
> Do NOT use this doc for current implementation decisions.
> 移动端交互/布局/导航/UI 设计面已由 v2 统一设计方案取代。

# OpenCode Pocket 页面导航结构规划

**文档日期**: 2026-07-04  
**版本**: 1.0

---

## 📱 应用导航架构

### 路由模式
- **Hash模式**: `createWebHashHistory()`
- **适用于**: 移动端Capacitor应用，避免需要服务器配置

### 路由守卫机制
1. **登录认证守卫**: `requiresAuth: true`
2. **龙虾初始化守卫**: `requiresLobster: true` (用于本地加密存储功能)
3. **自动跳转**: 已登录访问/login自动跳转到/ai

---

## 🗺️ 完整路由树

```
/ (重定向到 /ai)
│
├── /login                    [登录页]
│
├── /ai                       [AI工具控制台 - 首页] ⭐ bottomNav
│   └── 显示任务聚合看板
│
├── /notes                    [笔记列表] ⭐ bottomNav 🦞
│   ├── /notes/new            [新建笔记] 
│   ├── /notes/:id            [笔记详情] ⭐ bottomNav
│   └── /notes/:id/edit       [编辑笔记]
│
├── /email                    [邮箱收件箱] ⭐ bottomNav 🦞
│   ├── /email/:id            [邮件详情]
│   ├── /email/summary        [每日摘要列表]
│   ├── /email/summary/:date  [摘要详情]
│   └── /email/accounts       [邮箱账户配置]
│
├── /vault                    [密码箱列表] ⭐ bottomNav 🦞
│   ├── /vault/:id            [密码详情]
│   └── /vault/:id/edit       [编辑密码]
│
├── /meetings                 [会议记录] ⭐ bottomNav 🦞
│   └── [Phase 6A - Coming Soon]
│
├── /servers                  [服务器选择]
│
├── /instances                [实例列表]
│
├── /tasks                    [任务列表]
│   └── /tasks/:id            [任务详情]
│
├── /sessions                 [会话列表]
│
└── /settings                 [设置] ⭐ bottomNav
```

**图例**:
- ⭐ `bottomNav`: 显示底部导航栏
- 🦞 `requiresLobster`: 需要龙虾加密存储初始化
- 🔒 `requiresAuth`: 需要登录认证

---

## 📊 底部导航栏 (Bottom Navigation)

### 导航Tab配置

应用有5个主要Tab，显示在底部导航栏：

| Tab | 路由 | 图标 | 标题 | 功能描述 |
|-----|------|------|------|----------|
| 1 | `/ai` | 🤖 | AI工具 | 任务聚合看板，首页 |
| 2 | `/notes` | 📝 | 笔记 | 语音笔记、文本笔记 |
| 3 | `/email` | 📧 | 邮箱 | 邮箱助手、每日摘要 |
| 4 | `/vault` | 🔐 | 密码箱 | 密码管理、安全存储 |
| 5 | `/meetings` | 🎙️ | 会议 | 会议记录 (Coming Soon) |

**设计原则**:
- 每个Tab都是独立的功能模块
- 点击Tab直接切换到对应页面
- 不显示底部导航的页面：详情页、编辑页、新建页

---

## 🔄 页面导航流程

### 1. 启动流程

```
[应用启动]
    ↓
[检查登录状态]
    ├─ 未登录 → /login
    └─ 已登录 → /ai (首页)
        ↓
    [检查更新] (后台)
    [加载初始数据]
```

### 2. 登录流程

```
/login
    ↓
[输入用户名密码]
    ↓
[提交登录]
    ↓
[初始化龙虾存储] (如需要)
    ↓
[保存Token]
    ↓
[跳转到 /ai]
```

### 3. Tab切换流程

```
[点击底部Tab]
    ↓
[router.push('/target-path')]
    ↓
[目标页面加载]
    ↓
[底部导航高亮更新]
```

### 4. 详情页导航流程

```
[列表页] (如 /notes)
    ↓
[点击某项]
    ↓
[router.push('/notes/:id')]
    ↓
[详情页显示] (隐藏底部导航)
    ↓
[点击返回]
    ↓
[router.back() 或 router.push('/notes')]
    ↓
[返回列表页] (显示底部导航)
```

### 5. 编辑页导航流程

```
[详情页] (/notes/:id)
    ↓
[点击编辑按钮]
    ↓
[router.push('/notes/:id/edit')]
    ↓
[编辑页显示] (隐藏底部导航)
    ↓
[保存或取消]
    ↓
[router.back() 返回详情页]
```

---

## 🎯 关键导航问题及解决方案

### 问题1: 页面卡死/无响应

**可能原因**:
1. API请求阻塞UI线程
2. 组件加载缓慢
3. 路由守卫死循环
4. 大量数据渲染

**解决方案**:
```typescript
// 1. 使用异步加载
const routes = [
  {
    path: '/notes',
    component: () => import('../features/notes/NoteListView.vue')
  }
]

// 2. 添加Loading状态
// 3. 使用虚拟滚动处理大列表
// 4. 优化路由守卫逻辑
```

### 问题2: 返回按钮不工作

**原因**: 
- 没有历史记录
- 路由守卫拦截

**解决方案**:
```typescript
// 方案A: 使用router.back()并提供fallback
const goBack = () => {
  if (window.history.length > 1) {
    router.back()
  } else {
    router.push('/ai') // fallback to home
  }
}

// 方案B: 明确指定返回路径
router.push('/notes') // 明确返回到列表页
```

### 问题3: 底部导航栏高亮错误

**原因**:
- 当前路径判断错误
- 子路由未正确匹配

**解决方案**:
```typescript
// 使用route.path.startsWith()匹配
const isActive = (path: string) => {
  return route.path.startsWith(path)
}
```

### 问题4: 页面切换动画卡顿

**解决方案**:
```css
/* 使用transform代替top/left */
.page-enter-active, .page-leave-active {
  transition: transform 0.3s ease;
}
.page-enter-from {
  transform: translateX(100%);
}
.page-leave-to {
  transform: translateX(-100%);
}
```

---

## 🔍 导航状态管理

### 使用Router实例

```typescript
import { useRouter, useRoute } from 'vue-router'

// 在组件中
const router = useRouter()
const route = useRoute()

// 导航操作
router.push('/notes')          // 跳转
router.replace('/notes')       // 替换当前
router.back()                   // 返回
router.go(-2)                   // 返回2级

// 获取当前路由信息
route.path                      // 当前路径
route.params                    // 路由参数
route.query                     // 查询参数
route.meta                      // 路由元数据
```

### 导航守卫执行顺序

```
1. beforeEach (全局前置)
    ↓
2. beforeEnter (路由独享)
    ↓
3. beforeRouteEnter (组件内)
    ↓
4. afterEach (全局后置)
```

---

## 🧪 导航测试计划

### 测试1: 基础导航

**步骤**:
1. 启动应用 → 应该到达 `/ai` 或 `/login`
2. 依次点击所有底部Tab
3. 验证每个页面正确加载

**预期**:
- [ ] 每个Tab点击后立即响应 (< 300ms)
- [ ] 页面切换流畅无卡顿
- [ ] 底部导航高亮正确

### 测试2: 深度导航

**步骤**:
1. 进入 `/notes`
2. 点击某个笔记 → `/notes/:id`
3. 点击编辑 → `/notes/:id/edit`
4. 点击返回 → 应该回到 `/notes/:id`
5. 再次点击返回 → 应该回到 `/notes`

**预期**:
- [ ] 每次导航都成功
- [ ] 返回按钮工作正常
- [ ] 数据状态保持正确

### 测试3: 路由守卫

**步骤**:
1. 未登录访问 `/notes` → 应该跳转到 `/login`
2. 登录后自动跳转到 `/ai`
3. 已登录访问 `/login` → 应该跳转到 `/ai`

**预期**:
- [ ] 守卫正确拦截
- [ ] 跳转逻辑正确
- [ ] 无死循环

### 测试4: 边界情况

**步骤**:
1. 在详情页刷新 (F5)
2. 直接输入URL访问深层页面
3. 快速连续点击多个Tab
4. 点击返回按钮直到无历史记录

**预期**:
- [ ] 刷新后页面正常
- [ ] 直接访问深层页面正常
- [ ] 连续点击无卡死
- [ ] 无历史记录时返回首页

---

## 📱 移动端导航优化

### 1. 手势支持

```typescript
// 滑动返回手势 (iOS-like)
// 使用Capacitor的App plugin监听
import { App } from '@capacitor/app'

App.addListener('backButton', () => {
  if (window.history.length > 1) {
    router.back()
  } else {
    App.exitApp() // 或显示退出确认
  }
})
```

### 2. 页面缓存

```typescript
// 使用keep-alive缓存常用页面
<router-view v-slot="{ Component }">
  <keep-alive :include="['NoteListView', 'TasksView']">
    <component :is="Component" />
  </keep-alive>
</router-view>
```

### 3. 预加载

```typescript
// 预加载下一个可能访问的页面
router.beforeEach((to, from, next) => {
  // 预加载相关资源
  if (to.name === 'note-detail') {
    preloadNoteData(to.params.id)
  }
  next()
})
```

---

## 🛠️ 实现检查清单

### 路由配置
- [x] 所有路由定义完整
- [x] 路由守卫配置正确
- [x] Meta信息完整 (requiresAuth, bottomNav, etc.)
- [ ] 懒加载配置 (性能优化)

### 组件实现
- [ ] 所有页面组件存在
- [ ] 底部导航组件实现
- [ ] 返回按钮组件实现
- [ ] Loading状态组件

### 用户体验
- [ ] 页面切换动画
- [ ] Loading指示器
- [ ] 错误页面 (404, etc.)
- [ ] 空状态页面

### 性能优化
- [ ] 路由懒加载
- [ ] 页面缓存策略
- [ ] 预加载关键资源
- [ ] 虚拟滚动 (长列表)

---

## 🚀 下一步行动

### 立即执行

1. **验证所有页面组件存在**
   ```bash
   # 检查features目录下的所有View组件
   ls -la src/features/*/
   ```

2. **检查底部导航实现**
   ```bash
   # 查找BottomNav相关组件
   find src -name "*Bottom*" -o -name "*Nav*" -o -name "*Tab*"
   ```

3. **测试基础导航**
   - 构建APK
   - 安装到手机
   - 逐个点击Tab测试

4. **修复发现的问题**
   - 记录所有卡顿点
   - 优化慢的页面
   - 修复导航bug

### 后续优化

5. **实现页面转场动画**
6. **添加页面缓存**
7. **实现手势返回**
8. **性能优化**

---

**文档状态**: ✅ 已完成  
**下一步**: 构建APK并测试所有导航流程
