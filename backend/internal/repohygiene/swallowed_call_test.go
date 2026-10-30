package repohygiene

// swallowed_call_test.go — §945 卡口：后端**被丢弃的调用结果**（`_ = f(...)`）。
//
// # 这道卡口在量什么
//
//	一句话：**后端非测试代码里，任何「方法调用的结果被丢掉」都必须能被点名。**
//
//	被丢弃的调用分两类 ——
//
//	① **持久化写**：写库、写墓碑、写状态。丢掉它 ⇒ 数据**没落盘**而调用方
//	   **以为落了** ⇒ ⇒⇒ 这是**数据正确性**问题 ⇒ ⇒⇒ ⇒ §945 **全部修掉**。
//	② **非持久化**：关连接、清临时文件、序列化响应体、哈希/HMAC 写出、
//	   设置 socket 超时、回滚事务。丢掉它通常**无害**（那些调用的错误要么
//	   恒为 nil，要么已在别处兜底）⇒ ⇒⇒ ⇒ 但「无害」是**结论**，不是**默许**
//	   ⇒ ⇒⇒ ⇒ 必须**登记**，写清「为什么可以丢」⇒ ⇒⇒ ⇒ 后来人才知道这
//	   ⇒ ⇒⇒ ⇒ 是**判断**过的，而不是**漏掉的**。
//
//	⇒ ⇒⇒ 所以本卡口的形状是：**台账（登记过的丢弃）+ 不变量（台账里不许有
//	⇒ ⇒⇒ 持久化写）**。
//
// # 三层断言（缺一层就退化成「门绿 ≠ 量具有牙」）
//
//	① **台账双向相等**：扫出来的 (文件, 方法) → 条数 必须与台账**完全一致**
//	   ⇒ 少一条（有人偷偷删了台账行）或多一条（有人新加了一个丢弃）都红。
//	   ⇒ ★ 这一层是**通用网**：不管方法名多新，只要有人丢了一个结果就会红。
//	② **台账里不许有持久化写**：这是本卡口的**牙**
//	   ⇒ ⇒⇒ 「登记一下就放过」这条路被堵死 ⇒ 持久化写**只能修**。
//	③ **§945 参考集仍然成立**：那批修过的写调用
//	   ⇒ 必须**仍在源码里**（不许把调用整个删掉来消红）
//	   ⇒ 且**一份都不能是被丢弃的形态**。
//
// # 为什么用 go/parser 走 AST（而不是 grep）
//
//	★ §943 的教训（两次）：「我自己的 grep 普查是错的」——
//	  grep 会被**注释**里的字样骗到、也会漏掉跨字面量的写法。
//	⇒ ⇒⇒ 这里走 AST ⇒ ⇒⇒ **注释天然不算数**（不需要手写剥注释器，
//	⇒ ⇒⇒ ⇒ 而手写剥注释器在 CRLF 文件上会静默失效）。
//
//	⚠ 而且这一节**真的证明了 grep 不够**：本节最初用 grep 扫出「8 处」，
//	  换成 AST 后是 **97 处**（§945 文档里有完整对照）⇒ ⇒⇒⇒ 差别不在
//	  「grep 不准」，而在 **grep 只看到了我让它看的形态**。
//
// # 范围声明（是「不查」，不是「没风险」）
//
//	① **包级函数**（`os.Remove` / `json.Unmarshal` / …）不在本卡口范围
//	   ⇒ ⇒⇒ 本仓的持久化写**全在 store 的方法上** ⇒ ⇒⇒ ⇒ 故只扫方法调用。
//	② `backend/third_party/` 是**上游 vendored 代码**（identity-go）
//	   ⇒ ⇒⇒ 在本仓改它，下次同步上游就会被覆盖 ⇒ ⇒⇒ ⇒ 故排除。
//	③ `*_test.go` 与 `testdata/` 不是生产路径 ⇒ 排除。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// s945Discard 是一次「结果被丢弃」的方法调用。
type s945Discard struct {
	// File 是**仓库相对**路径（带 backend/ 前缀）。
	File string
	// Method 是被调用方法名。
	Method string
	// Line 是行号（只用于报错定位，不参与身份判定）。
	Line int
	// Expr 是被丢弃的表达式原文（只用于报错定位）。
	Expr string
}

// s945Key 是台账的键：**文件 + 方法名**（不含行号）。
//
// ⚠ 为什么不用行号做键：行号会随任何一次编辑漂移
//
//	⇒ ⇒⇒ 台账会在无关改动后飘红 ⇒ ⇒⇒ ⇒ 人就开始忽略这道门 ⇒ ⇒⇒
//	⇒ ⇒⇒ ⇒ 门一旦被忽略就等于没有 ⇒ ⇒⇒ ⇒ 所以键必须是**稳定**的。
//
// ⚠ 为什么方法名要带**条数**：同一个文件里 `w.WriteField(...)` 可能有 6 处
//
//	⇒ ⇒⇒ 只记「有 WriteField」会让「多了 3 处」这种漂移看不见
//	⇒ ⇒⇒ ⇒ ★ 扫描型判据必须钉住候选集的**绝对条数**。
type s945Key struct {
	File   string
	Method string
}

// s945LedgerRow 是台账的一行：**登记过的丢弃**。
type s945LedgerRow struct {
	File   string
	Method string
	// Count 是这个 (文件, 方法) 在源码里被丢弃的**处数**。
	Count int
}

// s945Ledger 是台账本体。
//
// ⚠ 台账只登记**非持久化**的丢弃。持久化写一条都不许出现在这里
//
//	（由 s945IsPersistenceMethod 这条不变量把住）。
//
// ⚠ 每一条的理由在 s945MethodReasons 里（按方法名给）——
//
//	同一个方法在不同文件里丢掉的是**同一类东西**，所以理由也是同一条。
//	⇒ ⇒⇒ 理由**不能**留空（见 §945 的断言 ②b）。
var s945Ledger = []s945LedgerRow{
	{"backend/cmd/wasmprobe/marshal/main.go", "Encode", 1},
	{"backend/internal/adapter/disk/sqlite_ro.go", "Close", 3},
	{"backend/internal/agent/transport_stdio.go", "Close", 1},
	{"backend/internal/agent/transport_stdio.go", "Kill", 1},
	{"backend/internal/agent/transport_ws.go", "SetReadDeadline", 1},
	{"backend/internal/agent/transport_ws.go", "WriteControl", 1},
	{"backend/internal/email/crypto.go", "Close", 1},
	{"backend/internal/email/crypto.go", "WriteString", 1},
	{"backend/internal/email/fetcher.go", "Close", 2},
	{"backend/internal/email/fetcher.go", "SetDeadline", 6},
	{"backend/internal/email/invoice_store.go", "Rollback", 1},
	{"backend/internal/email/mime.go", "SetDeadline", 1},
	{"backend/internal/email/pipeline_lock.go", "Close", 1},
	{"backend/internal/email/pop3_fetcher.go", "SetDeadline", 9},
	{"backend/internal/email/store.go", "Rollback", 4},
	{"backend/internal/identity/delegation.go", "Write", 1},
	{"backend/internal/llmgateway/audio_transform.go", "Close", 1},
	{"backend/internal/marketplace/blob.go", "Rollback", 1},
	{"backend/internal/meeting/pg_store.go", "Rollback", 1},
	{"backend/internal/notes/store.go", "Rollback", 1},
	{"backend/internal/notify/smtp.go", "Close", 7},
	{"backend/internal/notify/smtp.go", "Extension", 2},
	{"backend/internal/notify/smtp.go", "Quit", 1},
	{"backend/internal/opencode/config_merge.go", "Close", 5},
	{"backend/internal/pgmigrate/lock.go", "Close", 1},
	{"backend/internal/redclaw/auth_user.go", "Close", 1},
	{"backend/internal/scheduledtask/executors/webhook.go", "Write", 1},
	{"backend/internal/scheduledtask/store.go", "Write", 1},
	{"backend/internal/server/server.go", "SetWriteDeadline", 1},
	{"backend/internal/auth/resource.go", "Write", 1},
	{"backend/internal/server/resource_middleware.go", "Write", 2},
	{"backend/internal/server/resource_middleware.go", "resourceCurrent", 2},
	{"backend/internal/server/server_assistant.go", "Close", 3},
	{"backend/internal/server/server_audit_export.go", "Write", 2},
	{"backend/internal/server/server_llmbff.go", "SetWriteDeadline", 1},
	{"backend/internal/server/server_stt_settings.go", "loadSTTSettings", 4},
	{"backend/internal/server/smtp_probe.go", "Close", 1},
	{"backend/internal/server/smtp_probe.go", "Extension", 1},
	{"backend/internal/server/smtp_send.go", "Close", 4},
	{"backend/internal/server/smtp_send.go", "Extension", 1},
	{"backend/internal/server/smtp_send.go", "Quit", 1},
	{"backend/internal/stt/provider.go", "WriteField", 4},
	{"backend/internal/stt/provider_openai.go", "WriteField", 6},
	{"backend/internal/stt/transcribe.go", "WriteField", 6},
}

// s945MethodReasons 是**按方法名**给出的「为什么可以丢」的理由。
//
// ⚠ 为什么按方法名而不是按行给理由：同一个方法在不同文件里丢的是
//
//	**同一类东西** ⇒ ⇒⇒ 理由也是同一条 ⇒ ⇒⇒ 按行写理由只会制造
//	⇒ ⇒⇒ 97 份复制粘贴 ⇒ ⇒⇒ 而复制粘贴的理由**会互相漂移**
//	⇒ ⇒⇒ ⇒ （一处改了、其余 96 处没改 ⇒ 台账就说谎了）。
//
// ⚠ 每条理由都必须回答**同一个问题**：丢掉之后，**有没有东西会变错**？
var s945MethodReasons = map[string]string{
	"Close": "关闭句柄/连接/文件。失败之后**没有可执行的后续动作**（句柄已经不可用），" +
		"且不影响任何持久化状态 ⇒ 登记不修。",
	"Write": "写出。⚠ §946 之后仓里**剩下**四类：① CSV 编码器（`cw.Write`，审计导出）② 哈希（`h.Write`）" +
		"③ HMAC（`sig.Write` / `mac.Write`，签名摘要喂入）④ 冲突兜底响应体（`w.ResponseWriter.Write`，" +
		"原响应已被判废 409，写出失败无从补救）。都**不是持久化** ⇒ 登记不修。" +
		"⚠⚠ 「正常的 HTTP 响应体写出」**已在 §946 修掉**（写失败必须留痕）⇒ 不再登记在这里。",
	"resourceCurrent": "授权探针。返回 (code, role)，判定只消费 code；role 在这条分支里" +
		"没有下游（真正的角色解析在另一处拿）⇒ 丢弃不改变行为。",
	"WriteString": "往本地临时文件写一行（PID）。同一个文件后面还有 `f.Close()`，" +
		"真写坏了会在关闭/后续读取时暴露 ⇒ 登记不修。",
	"WriteField": "multipart 表单字段写出（stt 转发到上游）。底层 writer 已坏时后续字段也写不出去，" +
		"上游请求整体会失败并被看见 ⇒ 登记不修。",
	"WriteControl": "websocket 关闭帧写出。**尽力而为**的控制帧，失败时这条连接本来就要关 ⇒ 登记不修。",
	"Encode": "JSON 编码器写出（`cmd/wasmprobe/marshal` 这个 CLI 探针写 stdout）。" +
		"⚠ 审计导出的 JSON Encode 已由 §946 修掉；SSE 事件编码也由 §946 修掉。",
	"SetDeadline": "设置 socket 超时。失败 ⇒ 该连接后续读写的保护失效（应记日志），" +
		"但**不影响任何持久化状态** ⇒ 登记不修。",
	"SetReadDeadline":  "设置 socket 读超时（同 `SetDeadline`）⇒ 登记不修。",
	"SetWriteDeadline": "设置 socket 写超时（同 `SetDeadline`）⇒ 登记不修。",
	"Rollback": "事务回滚。pgx 语义：回滚失败（连接已断 / ctx 已取消）⇒ 连接**不会**回到池里，" +
		"所以**不存在锁泄漏**；而回滚本身是**正常路径**（每个错误分支都会走）" +
		"⇒ 记日志会变成常态噪声 ⇒ 登记不修。",
	"Extension": "SMTP 扩展探测，`smtp.Client.Extension` 返回 `(bool, string)`——" +
		"**第二个值是扩展参数串，不是 error** ⇒ 丢掉它无害；关键的 bool 已被调用方使用 ⇒ 登记不修。",
	"Quit": "SMTP `QUIT`。**尽力而为**的收尾命令，失败不影响任何状态 ⇒ 登记不修。",
	"Kill": "结束子进程。失败 ⇒ 进程可能残留；调用方紧接着就返回该错误 ⇒ 登记不修。",
	"loadSTTSettings": "第二个返回值 `ok` 只表示「**有没有设置记录**」；没有记录时用零值 + 默认配置" +
		"是**既定语义**（读失败已在函数**内部**记日志，见 §945 修复）⇒ 登记不修。",
}

// s945PersistenceVerbs 是「持久化写」的方法名前缀集合。
//
// ⚠ 这份列表**故意**不含 Write：仓里 `w.Write`（HTTP 响应体）、
//
//	`h.Write`（哈希）、`sig.Write`（HMAC）三种形态都叫 Write
//	⇒ ⇒⇒ 按名字分不出来 ⇒ ⇒⇒ ⇒ 而 `io.Writer` 的 Write **确实**返回 error
//	⇒ ⇒⇒ ⇒ ⇒ 所以「Write 丢弃」这件事**登记但不当作持久化写**。
//
// ⚠ `Set` 在列表里，但 `SetDeadline` / `SetReadDeadline` / `SetWriteDeadline`
//
//	用**精确排除**处理（socket 超时不是持久化）。
var s945PersistenceVerbs = []string{
	"Append", "Attach", "Create", "Delete", "Exec", "Export", "Import",
	"Insert", "Link", "Mark", "Move", "Record", "Rename", "Save", "Set",
	"Touch", "Update", "Upsert",
}

// s945BenignVerbs 是**精确排除**的方法名（它们看起来像持久化，但不是）。
var s945BenignVerbs = map[string]bool{
	"SetDeadline":      true,
	"SetReadDeadline":  true,
	"SetWriteDeadline": true,
}

// s945IsPersistenceMethod 判一个方法名**是不是**持久化写。
func s945IsPersistenceMethod(method string) bool {
	if s945BenignVerbs[method] {
		return false
	}
	for _, v := range s945PersistenceVerbs {
		if strings.HasPrefix(method, v) {
			return true
		}
	}
	return false
}

// s945FixedSite 是 §945 **修过**的一个写调用点。
type s945FixedSite struct {
	File   string
	Method string
	// Why 是「这一处为什么值得单列」的说明（必填）。
	Why string
	// Marker 非空时：文件里必须出现这段文字（**只出现一次**）。
	//
	// ⚠ 什么时候需要 Marker：有些修复**不是**「把调用改成检查过的形态」，
	//   而是「在**函数内部**把两种失败分开」（例如 STT 设置：读失败要在
	//   内部记日志，而调用点的 `p, _ :=` 本身是**既定语义**——ok 只表示
	//   「有没有记录」）⇒ ⇒⇒ ⇒ 这类点没法用「0 处被丢弃」来表达 ⇒ ⇒⇒ ⇒
	//   ⇒ 改用「文件里必须有这句留痕」。
	//
	// ⚠ Marker 必须**唯一**（同串出现多次 ⇒ 把上下文写进去）。
	Marker string
}

// s945FixedSites 是 §945 的修复参考集。
//
// ⚠ 这一张表的作用是**防「把调用删掉来消红」**：
//
//	⇒ 每一条都必须断言「该方法在本文件里**仍然被调用**」
//	⇒ ⇒⇒ 且「**一份都不能**是被丢弃的形态」。
//
// ⚠ 为什么是「文件 + 方法」而不是行号：同 §945 台账的键约定（行号会漂）。
var s945FixedSites = []s945FixedSite{
	{File: "backend/internal/server/server_meeting.go", Method: "UpdateScoped",
		Why: "会议状态写：6 处（4 处终态 ⇒ 500 + 可区分文本；2 处进度 ⇒ 只记日志）。终态写失败会让会议**永远停在中间态**并被同步到别的设备。",
	},
	{File: "backend/internal/server/task_from_source_handler.go", Method: "AppendEvent",
		Why: "溯源活动条目写：失败 ⇒ 溯源只活在 origin_kind/origin_ref 两列里，审计链里看不到；任务已建成 ⇒ 不改响应但必须留痕。",
	},
	{File: "backend/internal/server/server_email_pipeline.go", Method: "MarkInvoiceExported",
		Why: "发票导出标记写：失败 ⇒ 下一轮会**重导同一张票**（选取口径是「已下载且 exported_at=0」）⇒ 200 + 可选 unmarked 字段。",
	},
	{File: "backend/internal/server/server_email_ops.go", Method: "CountPendingOps",
		Why: "待同步操作数**读**：失败 ⇒ 响应里的 remaining=0 是**假值**（产品口径未拍 ⇒ 登记不代拍 ⇒ 只留痕）。",
	},
	{File: "backend/internal/server/intent_executor.go", Method: "SetEmailsFolderScoped",
		Why: "本地邮件归属写：失败 ⇒ 邮件在本地视图里还留在原文件夹（IMAP 已迁走）⇒ 只记日志（IMAP 成功不能被否定）。",
	},
	{File: "backend/internal/email/invoice_harvest.go", Method: "UpdateInvoiceHarvest",
		Why: "发票采集状态写（3 处静默 + 2 处本来就记日志）：失败 ⇒ 库里状态没更新 ⇒ 下一轮**重采同一张票**且失败原因丢失。",
	},
	{File: "backend/internal/agentbridge/bridge.go", Method: "UpdateStatus",
		Why: "agent 实例状态写（5 处）：失败 ⇒ 库里状态没更新 ⇒ 健康探测**仍按旧状态判断**（比如明明连不上却还写着 online）⇒ 派活会一直失败。",
	},
	{File: "backend/internal/migration/service.go", Method: "execLink",
		Why: "会话迁移链写（2 条边）：失败 ⇒ 迁移**已经发生**但链上看不到（可能只剩**半条链**）⇒ 只记日志（不能否定已发生的事实）。",
	},
	{File: "backend/internal/llmbff/service.go", Method: "RecordUsage",
		Why: "用量（分账）写（3 处）：失败 ⇒ **那一笔 token 从账上消失**（账单只会偏小 ⇒ 没人会查）⇒ 保持不阻断 chat（既有契约）但必须留痕。",
	},
	{File: "backend/internal/auth/identity.go", Method: "UpdateProfile",
		Why: "影子用户资料写：失败 ⇒ 显示名/主邮箱停留在旧值；该函数契约写着「Failures are **logged**」而实现却是无声 ⇒ 契约与实现矛盾。",
	},
	{File: "backend/internal/redclaw/file_exporter.go", Method: "ExportOnce",
		Why: "审计导出（写文件）：失败 ⇒ 审计条目没落盘而游标未推进 ⇒ 每轮都失败时**永远不出声**（导出彻底失效却看起来正常）。",
	},
	{File: "backend/internal/scheduledtask/store.go", Method: "Exec",
		Why: "定时任务删除墓碑写：失败 ⇒ **删除只发生了一半**（服务端删了，别的设备永远不知道）⇒ 只记日志（不能否定已发生的删除）。",
	},
	{File: "backend/internal/email/scheduler.go", Method: "GetSummaryByDateScoped",
		Why: "当天已有 summary 的**读**：失败 ⇒ 与「没有旧行」混为一谈 ⇒ **亲手制造**出注释要防的「旧行变孤儿」⇒ 必须中止本轮。",
	},
	{File: "backend/internal/server/server_email_classify.go", Method: "CountUnclassifiedScoped",
		Why: "未分类邮件数**读**：失败 ⇒ 响应里的 remaining=0 是**假值**（UI 会读成「全部分类完了」）。",
	},
	{File: "backend/internal/server/server_stt_settings.go", Method: "loadSTTSettings",
		Why:    "STT 设置读：失败与「用户没设置」混成同一个 ok=false ⇒ **读失败被伪装成未设置** ⇒ 用户改了设置却不生效 ⇒ 必须在函数内部记日志。",
		Marker: "读 STT 设置失败"},
	{File: "backend/internal/server/server_assistant.go", Method: "readCachedEmailBody",
		Why: "缓存邮件正文读：失败 ⇒ 回源抓取（多一次 IMAP 往返，慢但**正确**）⇒ 降级可接受，但要看得见「为什么慢」。",
	},
	{File: "backend/cmd/pocketd/main.go", Method: "CountUsers",
		Why: "用户数**读**（first-user bootstrap 前置）：失败 ⇒ 被当成「库里一个人都没有」⇒ **可能多插一个 admin** ⇒ 改成 fail-closed（读失败不 bootstrap）。",
	},
	{File: "backend/cmd/pocketd/main.go", Method: "Register",
		Why: "agent adapter 注册（5 处）：失败 ⇒ 那个 agent 类型**根本没接上**，现象却像下游问题 ⇒ 注册失败必须留痕。",
	},
	{File: "backend/internal/server/mobile_approval_handler.go", Method: "requireMobileWorkspace",
		Why: "守卫的 **fail-closed 标志**被丢掉（3 处）：守卫已写了 401/400 响应而 handler 继续跑 ⇒ 今天因 validateMobileApproval 已先校验而**不可利用**，但是**潜伏的 fail-open** ⇒ 按契约补 `if !ok`。",
	},
}

// s945ScanResult 是一次扫描的产物。
type s945ScanResult struct {
	// Discards 是被丢弃的调用。
	Discards []s945Discard
	// Calls 是 (文件 → 方法名 → 调用次数)，**只数方法调用**。
	Calls map[string]map[string]int
}

// s945ScanAll 扫**全部**受检文件。
func s945ScanAll(t *testing.T, root string) s945ScanResult {
	t.Helper()
	res := s945ScanResult{Calls: map[string]map[string]int{}}
	for _, rel := range trackedFiles(t, root) {
		if !strings.HasPrefix(rel, "backend/") || !strings.HasSuffix(rel, ".go") {
			continue
		}
		if strings.Contains(rel, "/testdata/") {
			continue
		}
		// ⚠ `backend/third_party/` 是**上游 vendored 代码**（identity-go）。
		//   ⇒ ⇒⇒ 在本仓改它，下一次同步上游就会**被覆盖** ⇒ ⇒⇒ ⇒ 而本节
		//   ⇒ ⇒⇒ ⇒ 修的都是「本仓自己写的代码」⇒ ⇒⇒ ⇒ 故排除。
		//   ⇒ ⇒⇒ ⚠ 这是一个**范围声明**，不是「那里没风险」：
		//   ⇒ ⇒⇒ ⇒ §945 文档里已登记了那里的一处（shadow dao 的 audit 丢弃）。
		if strings.HasPrefix(rel, "backend/third_party/") {
			continue
		}
		// ⚠ 测试文件里的调用不是生产路径 ⇒ 不计入候选集。
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		sub := s945ScanFile(t, root, rel)
		res.Discards = append(res.Discards, sub.Discards...)
		res.Calls[rel] = sub.Calls[rel]
	}
	sort.Slice(res.Discards, func(i, j int) bool {
		if res.Discards[i].File != res.Discards[j].File {
			return res.Discards[i].File < res.Discards[j].File
		}
		return res.Discards[i].Line < res.Discards[j].Line
	})
	return res
}

// s945ScanFile 扫一个文件。
func s945ScanFile(t *testing.T, root, rel string) s945ScanResult {
	t.Helper()
	path := root + "/" + rel
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", rel, err)
	}
	return s945ScanAST(fset, f, rel)
}

// s945ScanAST 在**已解析的 AST** 上找被丢弃的调用。
//
// ⚠ 单独抽出来是为了让「自证臂」能拿**合成源码**喂进来
//
//	（⇒ 证明扫描器不是恒空的）。
func s945ScanAST(fset *token.FileSet, f *ast.File, rel string) s945ScanResult {
	res := s945ScanResult{Calls: map[string]map[string]int{}}
	res.Calls[rel] = map[string]int{}
	pkgs := s945ImportedPkgs(f)
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		discarded := s945DiscardsResult(as)
		for _, rhs := range as.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			method := sel.Sel.Name
			// ⚠ 包级函数不在本节范围（见文件头「范围声明」）。
			if !s945IsMethodReceiver(sel.X, pkgs) {
				continue
			}
			res.Calls[rel][method]++
			if !discarded {
				continue
			}
			res.Discards = append(res.Discards, s945Discard{
				File:   rel,
				Method: method,
				Line:   fset.Position(call.Pos()).Line,
				Expr:   exprText(fset, call),
			})
		}
		return true
	})
	return res
}

// s945DiscardsResult 判一条赋值是不是「把结果丢掉了」。
//
// ⚠⚠ 判据落在**最后一个**左值上，不是「有没有 `_`」——
//
//	（我第一版写成「第二个位置」，被 `d.Record` 那种**四返回值**打脸：
//	 `_, _, _, err := d.Record(...)` 的 error 在**最后一位**，
//	 而第二个位置是 `_` ⇒ 第一版会把它误报成「丢弃」⇒ ⇒⇒ 而误报会把
//	 ⇒ ⇒⇒ 卡口的信誉毁掉 ⇒ ⇒⇒ ⇒ 人就不信这道门了。）
//
//	⇒ ⇒⇒ 正确规则（Go 的惯例：error 是**最后一个**返回值）：
//	`_ = f()`          ⇒ 单返回值 ⇒ **丢弃**。
//	`_, _ = f()`       ⇒ 两个都丢 ⇒ **丢弃**。
//	`v, _ = f()`       ⇒ 丢的是最后一个 ⇒ **丢弃**。
//	`_, err = f()`     ⇒ error **留下了** ⇒ **不算**丢弃。
//	`_, _, _, err = f()` ⇒ error **留下了** ⇒ **不算**丢弃。
//
//	⚠ 严格地说，判据只能确定「**最后一个返回值**被丢弃」，
//	  不能确定「那个返回值**是不是 error**」（要类型信息才有）⇒ ⇒⇒
//	  ⇒ 「是不是 error」由**台账的理由**逐条判定 ⇒ ⇒⇒ ⇒ 这也正是
//	  ⇒ ⇒⇒ ⇒ 台账必须存在的原因。
func s945DiscardsResult(as *ast.AssignStmt) bool {
	if len(as.Lhs) == 0 {
		return false
	}
	last := as.Lhs[len(as.Lhs)-1]
	id, ok := last.(*ast.Ident)
	return ok && id.Name == "_"
}

// s945ImportedPkgs 收集本文件 import 的包名。
func s945ImportedPkgs(f *ast.File) map[string]bool {
	m := map[string]bool{}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		name := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			name = p[i+1:]
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		m[name] = true
	}
	return m
}

// s945IsMethodReceiver 判 receiver 是不是「某个对象的方法」。
//
// ⚠ 三种 receiver 形态：
//
//	① `x.M(...)`（x 是标识符）⇒ x 若是被 import 的包名 ⇒ **包级函数**
//	   ⇒ ⇒⇒ 不算方法调用。
//	② `a.b.M(...)`（selector 链）⇒ **方法调用**（例如 `s.pool.Exec`）。
//	③ 其它形态（CallExpr / IndexExpr …）⇒ 例如 `json.NewEncoder(w).Encode(v)`
//	   ⇒ ⇒⇒ 它的根仍然是**包级函数** ⇒ ⇒⇒ 不算方法调用。
func s945IsMethodReceiver(x ast.Expr, pkgs map[string]bool) bool {
	switch recv := x.(type) {
	case *ast.Ident:
		return !pkgs[recv.Name]
	case *ast.SelectorExpr:
		return true
	default:
		return false
	}
}

// exprText 取一段源码的原文（只用于报错定位）。
func exprText(fset *token.FileSet, n ast.Node) string {
	start := fset.Position(n.Pos())
	var sb strings.Builder
	if err := printer.Fprint(&sb, fset, n); err != nil {
		return "?"
	}
	text := sb.String()
	if len(text) > 120 {
		text = text[:120] + "…"
	}
	return fmt.Sprintf("%d: %s", start.Line, text)
}

// s945Keyed 把扫描结果按 (文件, 方法) 聚成台账键 → 条数。
func s945Keyed(ds []s945Discard) map[s945Key]int {
	m := map[s945Key]int{}
	for _, d := range ds {
		m[s945Key{File: d.File, Method: d.Method}]++
	}
	return m
}

// s945FormatKeys 把台账键渲染成可读文本（报错用）。
func s945FormatKeys(m map[s945Key]int) string {
	keys := make([]s945Key, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].File != keys[j].File {
			return keys[i].File < keys[j].File
		}
		return keys[i].Method < keys[j].Method
	})
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString("  " + k.File + "  ::" + k.Method + "  x" + strconv.Itoa(m[k]) + "\n")
	}
	return sb.String()
}

// s945FormatRows 把台账行渲染成可读文本（报错用）。
func s945FormatRows(rows []s945LedgerRow) string {
	var sb strings.Builder
	for _, r := range rows {
		sb.WriteString("  " + r.File + "  ::" + r.Method + "  x" + strconv.Itoa(r.Count) + "\n")
	}
	return sb.String()
}

// TestS945LedgerMatchesSource 是断言 ①：台账与源码**双向相等**。
//
// ⚠ 用 `t.Errorf` 而不是 `t.Fatalf`：本测试要**一次报全**所有差异
//
//	（★ 同一层级的多个症状，第一条 Fatalf 会把后面的症状掐断）。
func TestS945LedgerMatchesSource(t *testing.T) {
	root := repoRoot(t)
	res := s945ScanAll(t, root)
	found := s945Keyed(res.Discards)

	ledger := map[s945Key]int{}
	for _, r := range s945Ledger {
		ledger[s945Key{File: r.File, Method: r.Method}] += r.Count
	}

	// ①a 台账里多出来的（源码里已经没有这一处了 ⇒ 台账过时了）。
	for k, n := range ledger {
		got, ok := found[k]
		if !ok {
			t.Errorf("台账登记了 %s ::%s x%d，但源码里**一处都没有**了（台账过时，或那一处被删了）", k.File, k.Method, n)
			continue
		}
		if got != n {
			t.Errorf("台账与源码条数不一致：%s ::%s 台账=%d 源码=%d", k.File, k.Method, n, got)
		}
	}
	// ①b 源码里多出来的（有人新丢了一个结果 ⇒ 必须有人判一次）。
	for k, n := range found {
		got, ok := ledger[k]
		if !ok {
			t.Errorf("源码里有 %d 处「%s ::%s」的结果被丢弃，但**台账里没有登记**\n"+
				"⇒ 要么改掉（持久化写必须改），要么在 s945Ledger 里登记并写清理由。",
				n, k.File, k.Method)
			continue
		}
		if got != n {
			t.Errorf("台账与源码条数不一致：%s ::%s 台账=%d 源码=%d", k.File, k.Method, got, n)
		}
	}

	// ①c 绝对条数（★ 扫描型判据必须钉住候选集的绝对条数）。
	//    ⇒ 只钉「台账条数」不够：条数不变但**总处数**变了也要能看见。
	// ⚠⚠ 97 → 86（§946）：§946 修掉了 11 处「HTTP 响应体写出」的丢弃
	//　（writeJSON 家族 + 直接 Encode + w.Write + SSE 的 fmt.Fprintf）
	//　⇒ ⇒⇒ 那 11 处**不再**是「被丢弃的调用」⇒ ⇒⇒ ⇒ 台账必须同步减掉。
	// ⚠ 86 → 91（§951）：§951 把 internal/notify/smtp.go 的 SMTP 拨号改成了
	// 「自己拨号 + 每一个错误分支都显式关连接」⇒ 那 5 条新出现的 `_ = conn.Close()`
	// 都是**放弃一条正在出错的连接**（错误本身已经是主信号，close 的错误无意义）
	// ⇒ 与既有的 `Close` 登记同族 ⇒ 台账同步 +5。
	// ⚠ 91 → 92（§976）：internal/pgmigrate/lock.go 的 Release() 在**解锁失败**时
	//	必须销毁那条连接（会话级 advisory lock 挂在连接上 ⇒ 归还给池子会让下一条
	//	业务查询继承一把「没人知道谁持有的」锁 ⇒ 同 schema 的别的实例再也拿不到锁）。
	//	销毁走 `raw := conn.Hijack(); _ = raw.Close(ctx)` ⇒ 多一处被丢弃的 Close
	//	⇒ 与 internal/email/pipeline_lock.go 的同类做法同族 ⇒ 台账登记 +1。
	// ⚠ 92 → 98（资源委托 2026-10-30）：resource_middleware 的两处 `_, _ = w.ResponseWriter.Write`
	//	是 409 冲突兜底响应（原响应已被判废，写出失败无从补救）；auth/resource.go 的一处是
	//	审计上下文写出（m 是内存 buffer，错误随后随 marshal 一并暴露）；
	//	两处 `code, _ := resourceCurrent` 丢的是 role（授权判定只看 code）。
	if got, want := len(res.Discards), 98; got != want {
		t.Errorf("被丢弃的调用总处数 = %d（台账登记的是 %d 处）⇒ 数量变了必须重新定性", got, want)
	}
	if got, want := len(s945Ledger), 44; got != want {
		t.Errorf("台账行数 = %d（期望 %d）⇒ 台账行数变了必须重新定性", got, want)
	}
}

// TestS945LedgerHasNoPersistenceWrite 是断言 ②：台账里不许有持久化写。
//
// ⛔ 这是本卡口的**牙**：光有「双向相等」不够——
//
//	否则「登记一下就放过」这条路是通的 ⇒ ⇒⇒⇒ 持久化写会被洗成 benign。
func TestS945LedgerHasNoPersistenceWrite(t *testing.T) {
	for _, r := range s945Ledger {
		if s945IsPersistenceMethod(r.Method) {
			t.Errorf("台账里出现了**持久化写** %s ::%s x%d\n"+
				"⇒ 持久化写的错误被丢掉 ⇒ 数据没落盘而调用方以为落了\n"+
				"⇒ 它**只能修**，不能登记。（若确实是例外，必须写进 s945BenignVerbs 并说明理由）",
				r.File, r.Method, r.Count)
		}
	}
}

// TestS945LedgerReasonsComplete 是断言 ②b：每条登记都有**理由**。
//
// ⚠ 理由不能留空：空理由 = 「不知道能不能丢」= **没判断过**。
func TestS945LedgerReasonsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range s945Ledger {
		reason, ok := s945MethodReasons[r.Method]
		if !ok {
			t.Errorf("台账里的 %s ::%s 没有登记理由（s945MethodReasons 里缺 %q）", r.File, r.Method, r.Method)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("台账里的 %s ::%s 的理由是空的", r.File, r.Method)
		}
		seen[r.Method] = true
	}
	// 反向：理由表里不该有台账用不到的方法（否则理由会悄悄漂）。
	for m := range s945MethodReasons {
		if !seen[m] {
			t.Errorf("s945MethodReasons 里的 %q 已经没有任何台账行在用（理由会悄悄漂 ⇒ 删掉它）", m)
		}
	}
}

// TestS945FixedSitesStillChecked 是断言 ③：§945 的参考集仍然成立。
//
// ⚠ 两条：
//
//	(a) 该方法在本文件里**仍然被调用** ⇒ 不许「把调用删掉」来消红。
//	(b) 该方法在本文件里**一份都不能**是被丢弃的形态。
func TestS945FixedSitesStillChecked(t *testing.T) {
	root := repoRoot(t)
	res := s945ScanAll(t, root)

	discardedByKey := s945Keyed(res.Discards)
	for _, fs := range s945FixedSites {
		if strings.TrimSpace(fs.Why) == "" {
			t.Errorf("参考集 %s ::%s 没有写 Why（理由必填）", fs.File, fs.Method)
		}
		calls := res.Calls[fs.File][fs.Method]
		if calls == 0 {
			t.Errorf("参考集 %s ::%s：源码里**已经不再调用**这个方法了（不许把调用删掉来消红）", fs.File, fs.Method)
		}
		if fs.Marker == "" {
			if n := discardedByKey[s945Key{File: fs.File, Method: fs.Method}]; n > 0 {
				t.Errorf("参考集 %s ::%s：还有 %d 处是**被丢弃**的形态（§945 修过它 ⇒ 必须仍然是「检查过」的形态）",
					fs.File, fs.Method, n)
			}
			continue
		}
		// ⚠ Marker 形态：修复是「在函数内部留痕」，所以断言的是**那句话在**。
		//   ⇒ Marker 必须**唯一**：出现 0 次 = 留痕被删了；出现 ≥2 次 = 锚点不唯一。
		src, err := os.ReadFile(root + "/" + fs.File)
		if err != nil {
			t.Errorf("参考集 %s：读文件失败 %v", fs.File, err)
			continue
		}
		n := strings.Count(string(src), fs.Marker)
		if n != 1 {
			t.Errorf("参考集 %s：留痕锚点 %q 出现 %d 次（期望**恰好 1 次**）⇒ 0 次 = 留痕被删了；≥2 次 = 锚点不唯一（要把上下文写进锚点）",
				fs.File, fs.Marker, n)
		}
	}
	// 参考集的**条数**也钉死（不许悄悄删一行）。
	if got, want := len(s945FixedSites), 19; got != want {
		t.Errorf("§945 参考集条数 = %d（期望 %d）⇒ 增减一条都要重新定性", got, want)
	}
}

// TestS945ScannerHasTeeth 是**自证臂**：证明扫描器不是恒空的。
//
// ⚠ 为什么必须单独写一条：★ 「门绿」≠「量具有牙」——
//
//	一条只断言「源码里没有 X」的测试，在**扫描器坏了**时同样会绿
//	（扫描器什么都不报 ⇒ 「没有」成立 ⇒ 绿）⇒ ⇒⇒⇒ 所以必须拿
//	**已知答案的输入**喂给扫描器，证明它**该报的会报**。
//
// ⚠ 两条臂：
//
//	① 正对照：合成源码里的「被丢弃的持久化写」**必须**被扫出来。
//	② 负对照：合成源码里的「错误被接住的写」**不能**被扫出来
//	   （否则这道门会因为「什么都报」而变成噪声门）。
func TestS945ScannerHasTeeth(t *testing.T) {
	src := `package p

import "os"

type store struct{}

func (s *store) UpdateStatus(id string) error { return nil }

func a(s *store) {
	_ = s.UpdateStatus("x") // ← 必须被扫出来（持久化写被丢弃）
}

func b(s *store) error {
	return s.UpdateStatus("x") // ← 不能被扫出来
}

func c(s *store) (string, error) {
	v, err := s.UpdateStatus("x")
	_ = v
	return "", err
}

func d() {
	_, _ = os.Remove("x") // ← 包级函数，不在范围
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("解析合成源码失败：%v", err)
	}
	res := s945ScanAST(fset, f, "synthetic.go")

	if len(res.Discards) != 1 {
		t.Errorf("扫描器在合成源码上报出 %d 处丢弃（期望 1 处）⇒ 扫描器可能坏了\n%s",
			len(res.Discards), s945FormatKeys(s945Keyed(res.Discards)))
		return
	}
	got := res.Discards[0]
	if got.Method != "UpdateStatus" {
		t.Errorf("扫描器报出的方法名 = %q（期望 UpdateStatus）", got.Method)
	}
	// ⚠ 正对照必须是**持久化写**：否则这道门可能在坏掉的同时还绿。
	if !s945IsPersistenceMethod(got.Method) {
		t.Errorf("正对照用的 %q 不算持久化写 ⇒ 断言 ② 的牙就没有被验到", got.Method)
	}
	// ⚠ 负对照：`os.Remove` 是包级函数 ⇒ 不该被算成方法调用。
	if n := res.Calls["synthetic.go"]["Remove"]; n != 0 {
		t.Errorf("扫描器把包级函数 os.Remove 当成了方法调用（%d 次）⇒ 范围判断坏了", n)
	}
}
