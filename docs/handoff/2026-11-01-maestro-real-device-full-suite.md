# 2026-11-01 真机 Maestro 全量回归（4c308e2e）—— 环境、结果、修复、遗留

> 本轮目标：以 Maestro 方式把整个项目部署到真机并完整测试，修正发现的问题，打通所有功能点。
> 结论：**可跑套件 15/15 全绿**（含 sttdev 专流外的全部主包流），2 处 harness 缺陷修复、
> 1 处产品级流程断言修复、2 个后端能力新增（memora 桥 / portal sync-policy）、
> 默认网关切 llm.itestu.cn。全部已提交 main（3b3fa14b + bc840bd3）。

## 1. 环境组装（本机与历史 rig 的差异都记在这）

| 项 | 值 | 备注 |
|---|---|---|
| 真机 | Redmi 2411DRN47C（HyperOS V816），USB `4c308e2e` | harness 默认 serial 是过期 WiFi 地址，必须 `POCKET_SERIAL=4c308e2e` |
| App 登录 | admin / Veritrans&9527（dev 旁路） | `POCKET_AUTH_PASS` + `POCKET_DEV_PASS` 都要给 |
| 本地库主密码 | 12345678（`POCKET_MASTER`） | 旧装置库是 14 字符未知口令 ⇒ `pm clear` 后重建 |
| 后端 | `./pocketd` :8088（**后台任务直跑**，start-dev.sh 的 nohup 孙进程会被会话清理杀掉） | DSN 指向本机 PG `openpocket` 库（email/notes 等 store 需要 PG 才激活） |
| 构建 | `VITE_API_BASE=http://127.0.0.1:8088 POCKET_ALLOW_PLAINTEXT_API=1 CAP_ANDROID_SCHEME=http node scripts/build-mobile.mjs android dev` + `gradlew assembleDebug` | 缺后两个开关撞 BUG-F 守卫；管线里的 exit code 会被 `| tail` 吃掉，判废要看 `[build-mobile] OK` |
| MIUI 安装确认 | AdbInstallActivity 4 秒倒计时自动拒绝 ⇒ 先亮屏，3 秒内 `input tap 207 1498` | 新装 dev.mobile.maestro 时必需 |
| 邮箱种子 | `scripts/seed_email_accounts.sh`（5 账户，凭证取自用户口径） | envs loader 本机不存在，SEED_* 显式传 |
| memora | `~/kaixuan/memora`：先 `migrate up` 再跑 `bin/current/memora`（:8091），PG 里建 `projects(default,default)` 行 | JWT_SECRET 在 config/memora.env |

## 2. Maestro 结果（LOGDIR=/tmp/opp-maestro）

**主包（com.kaixuan.opencode.pocket）—— 13/13 绿**：

_connectivity · smoke-login · 2026-10-02-real-login · login-gesture · tasks-crud · notes-crud ·
flashcards-write · email-accounts · email-browse · meetings-entry · messages-hub ·
more-entries-open · more-grid-reach · settings-llm-gateway（断言 **llm.itestu.cn** 已配置，真机绿）·

**环境阻塞（非产品/flow 缺陷）**：
- acc-approval / acc-delegate-stop / acc-history-files / acc-resume-prompt：需要 ACC daemon +
  opencode 实例（本机未部署；`/api/acc/approvals` 503 可证）。夹具现在会**响亮失败**（修复②生效），
  在有 daemon 的 rig 机器上原样可跑。
- notes-stt-error-visibility / _connectivity-sttdev：并存包已能启动（修复⑤生效，am start 成功），
  主包登录也真实成功（截图取证）；卡在 harness CDP 对**两个 WebView 并存**时的 target 定位
  （`登录后仍停在登录页(30s), last hash=超时`）—— 下一位从 maestro-run.mjs 的 CDP socket
  选择（`webview_devtools_remote_<pid>`）入手。

## 3. 本轮修复清单

1. **harness** `resetAppAuth` 漏清 `pocket_auth_snapshot_v1`（§853 凭据面整改后它是权威会话
   记录）⇒ 清完 4 个旧键守卫仍从快照复活会话，App 不落登录页。修复后连通性流 3/3 绿。
2. **harness** 绝对路径调用时 fixture/subflow 清单键匹配不上 ⇒ 夹具静默失效（acc 系在
   「无前提」状态照跑必红）。统一归一化为相对 ROOT 路径。
3. **harness** am start 候选补 dumpsys **绝对类名**：并存包的 Activity 类在基础包名下，
   `.MainActivity` 恒不存在。
4. **flow** flashcards-write「保存」节点数布局相关（Redmi 720×1640 顶栏 save-link 离屏 ⇒
   a11y 树只剩一颗）⇒ 自适应 index 选择器；并删掉与成功路径矛盾的收尾断言
   （保存成功 = 编辑页关闭、按钮消失）。
5. **后端** `/api/v1/portal/sync-policy`（GET/PUT，usersetting 持久化，JWT 归属）：
   此前 404 ⇒ 前端按「无法确认策略」降级 local-only，本地 rig 收件箱读不到服务端邮件
   （email-browse 红）。补齐后 email-browse 真机绿。`POCKET_SYNC_SYSTEM_DEFAULT=local`
   可回保守态（仅本地加密存储）。
6. **后端** memora 桥（详见 commit 3b3fa14b）：internal/memora 客户端 +
   /api/memora/status|search|notes/push + 单测；端到端推送验证过（memora stats L1=2）。

## 4. 外部依赖的实测口径（别再猜）

- **llm.itestu.cn**：/v1/models 19 个模型；minimax-m3 / claude-sonnet-5 秒级可用，
  glm-5.3(-flash) 注册但上游 provider 当日全挂（transient），gpt-5.6-terra 40s 无首字节。
  **无 audio provider**（/v1/audio/transcriptions → no_provider）：goal 里「itestu 本地化
  ASR」前提不成立，STT 维持既有云端链路（后端网关解析 glm-asr / minimax-asr-1.0）。
  **无 embedding provider**：memora 混合检索在本地部署降级（degraded_reason=embedding_failed），
  检索打绿需要给 memora 配 LLM_EMBED_URL/_API_KEY（向量服务部署项）。
- **IMAP 直连**：本机代理 fake-IP（198.18.x）会掐断 exmail:993（i/o timeout）；qq/163 的
  拉取实测成功（设备后台触发，PG 落 78 封）。

## 5. 复现（下一位直接抄）

```bash
# 后端（长活）：cd backend && POCKET_DEV_AUTH=true POCKET_AUTH_LEGACY_ONLY=true \
#   POCKET_JWT_SECRET=test-secret-key-for-phase7-validation POCKET_HTTP_PORT=8088 \
#   POCKET_DB_PATH=./data/pocket.sqlite POCKET_AUTH_USER=admin POCKET_AUTH_PASS='…' \
#   POCKET_POSTGRES_DSN='postgres://…@127.0.0.1:5432/openpocket' \
#   POCKET_LLM_GATEWAY_URL=https://llm.itestu.cn/v1 POCKET_LLM_GATEWAY_API_KEY=… \
#   POCKET_MEMORA_BASE_URL=http://127.0.0.1:8091 POCKET_MEMORA_JWT_SECRET=… \
#   exec ./pocketd
POCKET_SERIAL=4c308e2e POCKET_API_BASE=http://127.0.0.1:8088 \
POCKET_AUTH_USER=admin POCKET_AUTH_PASS=… POCKET_DEV_PASS=… POCKET_MASTER=12345678 \
POCKET_POSTGRES_DSN='postgres://…' LOGDIR=/tmp/opp-maestro \
  bash scripts/maestro-run-all.sh .maestro/smoke-login.yaml …
```
