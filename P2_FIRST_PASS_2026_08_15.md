# P2 首轮验收记录（2026-08-15）

## 范围

本轮完成路线图 `09` 的 E5-S1 / E5-S3 / E5-S4 第一阶段：

- 横屏/平板会话 master-detail 双栏，窄屏单栏回退；
- unified diff 按 hunk 分段渲染；
- 会话服务端搜索、设备本地归档/恢复。

会议录音/摘要、企业集成只读状态、可选 TTS 不在本轮范围。

## 验收结果

### E5-S1 双栏平板布局：通过

Android `pocket_test` AVD + WebView CDP 测量：

| Viewport | 结果 | 横向溢出 |
|---|---|---|
| 393x竖屏 | 单栏；detail `display:none` | 无（scrollWidth=393） |
| 754x365 横屏手机 | 单栏回退 | 无（scrollWidth=754） |
| 1024x768 平板模拟 | 371px / 605px 双栏 | 无（scrollWidth=1024） |

平板 master/detail 两个 pane 均为独立 `overflow:auto` 滚动容器；任何视口下切换
实例都会清空旧详情（宽窄切换不残留 selected query）；窄屏不挂载 detail 组件，
避免后台残留 store 会话与轮询；嵌入详情顶栏提供关闭按钮作为详情态退出路径。
窄屏选中会话继续使用 `/sessions/:id` 路由。

### E5-S3 Diff 分段：部分通过

- `diffParse.ts` 使用 unified diff `@@` hunk 头做保守识别；普通日志不误判。
- `DiffBlock.vue` 每个 hunk 独立折叠，只默认展开首段；单个 hunk 也按 250 行
  渐进挂载，避免 5,000 行一次性进入 DOM；全部文本用 Vue 插值，不使用 HTML 注入。
- 解析层测试覆盖单文件、多文件、边界内容行、对象字段提取、单 hunk 5,000 行和
  100 hunk / 5,000 行输入，测试通过。

**仍待真实设备性能基线**：本轮验证了 5,000 行解析正确性和折叠 DOM 策略，尚未用
真实 OpenCode 5,000 行工具输出测量帧率/交互延迟；按路线图不得将桌面 Node 测试
宣称为真实设备性能通过。

### E5-S4 搜索/归档：通过（归档为本地语义）

- 选择实例后，搜索调用现有
  `GET /api/mobile/sessions/search?instance_id=&q=`，350ms 防抖并在输入发生时立即
  作废旧请求；选定实例结果支持本地分页。
- 离线且已解锁时从 SQLCipher `local_mobile_sessions` 读取按 workspace/instance
  隔离的加密缓存；未解锁或无缓存时明确显示暂无缓存，不发虚假网络搜索。
- 归档 ID 按 `workspace + instance` 分区写入 localStorage；损坏值 fail-open，不隐藏
  会话；提供“当前 / 已归档”页签与恢复动作。
- OpenCode 上游无 archive 写接口，因此本轮归档明确为**当前设备列表元数据**：不会
  删除、修改服务端会话，也不冒充服务端状态。所有实例视图要求先选实例再打开或归档，
  避免会话 ID 与实例错配。

## 自动验证

```text
frontend npm run typecheck                         PASS
frontend npm run build:fast                        PASS（仅既有 chunk warning）
frontend node --test（native/composables/utils）   93/93 PASS
backend go test ./... -count=1                     PASS
Android assembleDebug + install                    PASS
```

## 审计轮次（2026-08-15 第二轮）

对 fde1293 的独立审计发现 14 项（3 P1 / 11 P2），已修复：

- 切换"当前/已归档"页签不重置 offset、归档后 offset 未钳制 → 分页 offset
  钳制到最后一页页首，页签切换归零。
- 离线 + 所有实例视图清空已有数据 → 保留已加载列表（08 §4.1）。
- 宽屏详情态无退出路径 → 嵌入顶栏加关闭按钮。
- 窄屏 selected query 残留与 detail 组件常挂载 → 选中态与断点解耦、窄屏卸载。
- CRLF diff 行尾未剥离；useBreakpoint 多消费者监听泄漏；diff 提取双次求值加缓存；
  离线缓存查询补部分索引；缓存读取失败给出可重试错误；有数据时刷新降级为内联状态。

已知残留（记录在案，不阻塞）：

- `isLobsterReady()` 非响应式：当前解锁流程必经 /login（路由变化触发重算），
  但后台锁定功能接入前需把它改为响应式 ref。
- "所有实例"视图离线只保留内存数据（本地缓存按实例隔离，无法安全合并）。

## 已知限制 / 下一步

1. 用真实设备 + 真实 5,000 行 OpenCode diff 输出建立滚动帧率和展开延迟基线。
2. 归档如需跨设备同步，应先在服务端增加 workspace-scoped session metadata 契约，
   再迁移 localStorage 数据；不能复用 OpenCode `status=inactive` 猜测归档语义。
3. “所有实例”旧列表 API 未返回 instance_id，无法安全打开/归档；当前 UI 强制先选实例。
4. 搜索后端当前是标题/ID 子串扫描，实例达到生产规模后需加分页或索引策略。
