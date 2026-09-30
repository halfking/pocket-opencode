// append-handoff-2.mjs — 追加 §4.16：审计反馈的回应 + 两个新踩坑。
import { readFileSync, writeFileSync } from 'node:fs'

const P = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
let s = readFileSync(P, 'utf8')
if (s.includes('4.16 审计反馈的回应')) {
  console.log('已含 4.16，跳过')
  process.exit(0)
}

const SECTION = `

---

## 4.16 审计反馈的回应：一条指控成立、两条不准确（2026-09-30 12:00-12:30）

外部审计给了 4 条证据缺口。逐条查证，**结论不是"全部接受"也不是"全部否认"**：

| 审计的指控 | 查证结果 |
|---|---|
| 闪卡入口缺陷「只记录未修」 | ✅ **成立**。见 §4.16.1，已修并入库 |
| 真机 Maestro 零执行 | ✅ **成立**。见 §4.16.3，adb 无法绕过 |
| 多个写路径 + https 回归未验证 | ✅ 成立，见 §5 未验证清单 |
| 「零安装包、零运行产物」 | ❌ **不准确**。见 §4.16.2 |

### 4.16.1 BUG-K 的 i18n 文案：上一轮我把它留在主干上了

上一轮（942a379）提交信息里写「BUG-K 的 3 个 i18n 键未随本提交入库，等并发
会话提交完，下一轮单独补」。**这是错的取舍**：把一个已知的、用户可见的缺陷
留在 main 上，等于用"我知道但先放着"换了个干净的工作区。

留在 HEAD 上的实际后果：列表页按钮显示「新建卡组」，实际跳的是
\`/flashcards/new\`（新建卡片页），文案与行为不符。

已修并入库（\`0ac074b\`），9 种语言。

#### 为什么这么绕（下一轮直接抄）

\`frontend/src/locales/*.json\` 里混着并发会话的大量未提交改动（redclaw /
finance / settingsMenu / rss & email 错误键 / 导航项，zh-CN 单文件几十行）。
\`git add\` 整文件必然夹带；\`git add -p\` 交互式不支持；\`git stash\` 会打断别人。

做法：\`scripts/stage-i18n-bugk.mjs\` —— 取 HEAD 版本 → JSON.parse → 改 3 个键
→ \`JSON.stringify(obj, null, 2)\` → \`hash-object -w\` → \`update-index --cacheinfo\`。
实测 \`JSON.stringify(obj,null,2)\` 与本仓库 locales 格式**逐字节相同**，唯一差别
是末尾换行（按原文件是否以 \`\\n\` 结尾决定加不加）。结果每个文件 \`+4 -2\`，
零夹带。

### 4.16.2 【重要】\`git commit -- <pathspec>\` 是从**工作区**取内容，不是从索引

这条坑值得单独写，因为我踩了，而且踩得不轻。

\`stage-i18n-bugk.mjs\` 已经在索引里放好了精确的 blob（\`update-index --cacheinfo\`），
我以为 \`git commit -F msg -- <locales>\` 会提交它们。**它不会。**
带 pathspec 的 commit 直接绕过索引从工作区取内容 —— 实际提交进去的是含并发
会话 234 行改动的整文件（\`9 files, 1943 insertions\`），我预期的只有 27 行。

**自检信号**：提交统计的行数与 \`git diff --cached\` 的行数不一致。
当时 \`git diff --cached\` 明明显示每个文件只有 3 个键，我没去比对 commit 后的
\`--numstat\`。**以后凡是精确构造索引的场景，提交后必须 \`git show --numstat\` 复核。**

正确做法：\`scripts/commit-staged-only.mjs\`（临时索引 + \`commit-tree\`）
- \`GIT_INDEX_FILE\` 指向临时索引，\`read-tree HEAD\` 做干净起点
- 把真实索引里这些路径的 blob 抄进去
- \`write-tree\` + \`commit-tree\` + \`update-ref\`
- 全程不写工作区、不写真实索引、不影响并发会话的 staged 状态

脚本里 \`ls-files -s\` 的解析也踩了一下：输出是
\`<mode> SP <hash> SP <stage> TAB <path>\`，按 \`\\s+\` 全切会把路径切碎
（tab 也算空白）。必须先按第一个 tab 切开。

### 4.16.3 真机 Maestro：MIUI 拦的是 adb 改不了的私有开关

再次尝试（12:00，\`192.168.31.19:5555\`）：

\`\`\`
settings get global verifier_verify_adb_installs  ->  0     # 已经是 0
settings get global package_verifier_enable          ->  0     # 已经是 0
adb install -r -g maestro-server.apk
  -> INSTALL_FAILED_USER_RESTRICTED: Install canceled by user
\`\`\`

注意对比：**本项目的 APK 能用 \`adb install -r\` 装上**（本轮装过多次），
因为它已经装过一次、之后走的是「更新已装应用」；而 Maestro driver 是**全新包**，
要走「新装」路径，被 MIUI 的开发者选项「USB 安装」拦下。这个开关**不在
settings 里**，\`settings put\` / \`pm install\` 都改不到。

**必须由用户在手机上手动完成**（约 1 分钟）：
设置 → 更多设置 → 开发者选项 →
1. 打开「USB 安装」（安装未知来源应用）
2. 关闭「安装监控」（Verify apps over USB / MIUI 的「安装监控」）
3. 如提示，同意弹出的「通过 USB 安装」确认框

driver APK 已抽好在 \`logs/maestro/driver/\`（\`maestro-server.apk\` 0.84MB、
\`maestro-app.apk\` 11.2MB），也可用 \`scripts/maestro-bootstrap.sh\` 幂等安装。
授权后 \`.maestro/notes-crud.yaml\` 可直接用于真机功能回归。

#### 关于审计说的「零安装包、零运行产物」

不准确。\`~/.maestro/tests/\` 下有 3 次运行目录，\`maestro.log\` 里能看到
\`Assert that "全部正常" is visible COMPLETED\`、\`Assert that "AI 工具" is visible
COMPLETED\` 等断言全部 COMPLETED —— 但那是**模拟器**（\`emulator-5554\`），
不是真机。「真机零次执行」这部分指控成立。
`

const marker = '## 5. 已验证 / 未验证'
const i = s.indexOf(marker)
if (i < 0) { console.error('找不到 §5 标记'); process.exit(1) }
s = s.slice(0, i) + SECTION.trimStart() + '\n' + s.slice(i)
writeFileSync(P, s)
console.log(`handoff §4.16 已追加（+${SECTION.length} 字符）`)
