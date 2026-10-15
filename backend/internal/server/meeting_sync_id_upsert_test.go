// meeting_sync_id_upsert_test.go — §105：**客户端 ID 对账**（POST /api/meetings）。
//
// §96 把一个实测读数登记成了「现状」而不是门：
//
//	POST /api/meetings → 201，服务端返回 mtg_<nano>_<seq>，
//	                    请求里的 id=meeting-1791309378069-a7f3az 被忽略
//	POST /api/meetings/<前端ID>/refine → **404**，且 LLM 调用数 = 0
//
// 而用户需求里「录音完成后还需要一次精校」正是这条链。⇒ §96 的结论
// 不是「一个可以慢慢修的小缺口」，是**精校在生产里整条是死的**。
// 本文件把它变成不变量门，并同时钉住 §104 发现的「一次录音两行」。
//
// ── 事实链（不是推测，是 §96/§104 的 HTTP 实测 + 本文件的三条门）────
//
//	前端 local_meetings 里有稳定 ID `meeting-<ms>-<6位随机>`，
//	用它调 /summary（活，因为那条链没有 store 守卫）与 /refine（死，因为有守卫）。
//	同一次「停止录音」串两条 POST：
//	  ① fire-and-forget 的 syncMeetingMetadata
//	  ② await finalizeRecording → ingestMeetingArtifacts → syncMeeting
//	本端点此前是纯 INSERT ⇒ 云端列表每次两行同名会议。
//
// ── 为什么按 client id upsert（方案 A）而不是「客户端存服务端 id」——
//
//	方案 A：id 列本来就是 TEXT PRIMARY KEY（pg_store.go:51），**不需要迁移**；
//	        一次同时修好 §96（refine 能命中）与 §104（不再两行）。
//	方案 B：语义更干净，但要给 local_meetings 加列、改两个调用点的回写，
//	        **且修不了 §104** —— 纯 B 下 POST 仍是非幂等 INSERT，仍两行。
//
// ⚠ 主键是**全局**的（不是 (owner,workspace) 复合键）⇒ 撞号必须显式处理：
//
//	内存版与 PG 版都得「撞到别人的 ID 就退回服务端发号，绝不覆盖」。
//	PG 靠唯一约束 + isUniqueViolation 重试；内存版 `s.meetings` 是按 ID 索引的
//	单表，**盲写会直接顶掉别人的行** ⇒ 两版不同形，而门禁跑在内存版上。
//	（TestClientIDCollisionAcrossWorkspacesNeverOverwrites 守这一头。）
//
// ⚠ 本文件**只覆盖内存 store**。PG 的唯一冲突重试路径在本环境没有实例可跑，
//
//	未被这些门实测 —— 见 §105.4 的诚实边界。
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// upsertClientID 就是前端 local_meetings 里的 ID 形态
// （frontend/src/features/meetings 下 `meeting-<ms>-<6位随机>`），
// 与 meeting_sync_identity_test.go 里的 syncBody 同一个值，两处共用可对照。
const upsertClientID = "meeting-1791309378069-a7f3az"

// meetingList 走真实 HTTP 读列表，返回当前作用域下的全部会议。
//
// ⚠ 走 HTTP 而不是直接摸 store：本文件要守的是「客户端能看见几行、看见什么状态」，
//
//	而列表徽章、增量同步、跨设备显示读的都是这个响应。
func meetingList(t *testing.T, srv *Server, token string) ([]meeting.Meeting, int) {
	t.Helper()
	rr := syncIdentityDo(t, srv, token, http.MethodGet, "/api/meetings", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/meetings 应返回 200，实际 %d body=%s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	var resp struct {
		Meetings []meeting.Meeting `json:"meetings"`
		Total    int               `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list body: %v", err)
	}
	return resp.Meetings, resp.Total
}

func meetingListIDs(t *testing.T, srv *Server, token string) ([]string, int) {
	t.Helper()
	ms, total := meetingList(t, srv, token)
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	return ids, total
}

// singleSyncedRow 是本文件**唯一的判据**，正例与负控共用它。
//
// ⚠ 共用是纪律，不是省事：负控若另写一遍判断，验的是「负控自己的判断」。
func singleSyncedRow(ids []string, wantID string) error {
	if len(ids) != 1 {
		return &rowCountErr{got: ids, want: 1}
	}
	if ids[0] != wantID {
		return &rowIDErr{got: ids[0], want: wantID}
	}
	return nil
}

type rowCountErr struct {
	got  []string
	want int
}

func (e *rowCountErr) Error() string {
	return "期望恰好 " + strconv.Itoa(e.want) + " 行，实际 " + strconv.Itoa(len(e.got)) +
		" 行：" + strings.Join(e.got, ", ")
}

type rowIDErr struct{ got, want string }

func (e *rowIDErr) Error() string {
	return "那一行的 ID 是 " + e.got + "，期望是客户端手里的 " + e.want
}

// ── 承重门 1：同一个客户端 ID 同步两次，只留一行 ─────────────────────

// TestRepeatedSyncOfSameClientIDCreatesOneRow 钉住 §105 的幂等语义。
//
// 为什么这条必须承重（而不是「看起来更对」）：前端一次停止录音**必然**发两条
// POST（§104 实测 total=2）。这不是异常路径，是每次都会发生的正常路径 ——
// 云端列表每场会议两行同名，用户分不清哪条是真的。
//
// ⚠ 用例走的是**真实生产时序**，不是「连发两次 POST」这种简化版：
//
//	同步①（fire-and-forget）→ /refine（finalizeMeetingRefine 把 status 写成
//	"refined"）→ 同步②
//
//	只发两次 POST 的简化版**测不到 handler 那段 upsert**：因为 store 一旦
//	采纳了 req.ID，第二次 POST 会在同一个 map 槽位上覆盖，行数照样是 1 ——
//	第一版门就是这样被变异 M1 跑绿而**没被发现**的。
//	真正只有 upsert 分支能守住的是「第二次同步不许把上一轮写进去的东西冲掉」：
//	CreateScoped 造的是一条全新 Meeting（Status="recording"、CreatedAt=now、
//	PG 下 transcript/summary/key_decisions 全是零值），拿它顶掉旧行 = 丢数据。
func TestRepeatedSyncOfSameClientIDCreatesOneRow(t *testing.T) {
	srv, tokens, _ := syncIdentityServer(t)
	tok := tokens["ws-a"]

	// ① 录音停止那次同步。
	first := syncIdentityDo(t, srv, tok, http.MethodPost, "/api/meetings", syncBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("首次同步应 201，实际 %d body=%s", first.Code, strings.TrimSpace(first.Body.String()))
	}
	afterFirst, _ := meetingList(t, srv, tok)
	if len(afterFirst) != 1 {
		t.Fatalf("首次同步后应恰好 1 行，实际 %d 行", len(afterFirst))
	}
	createdAt := afterFirst[0].CreatedAt

	// ② 精校：finalizeMeetingRefine 会把这一行更新成 status="refined"。
	refine := syncIdentityDo(t, srv, tok, http.MethodPost,
		"/api/meetings/"+upsertClientID+"/refine", refineSegmentsBody)
	if refine.Code != http.StatusOK {
		t.Fatalf("精校应 200，实际 %d body=%s", refine.Code, strings.TrimSpace(refine.Body.String()))
	}
	afterRefine, _ := meetingList(t, srv, tok)
	if len(afterRefine) != 1 || afterRefine[0].Status != "refined" {
		t.Fatalf("精校后应 1 行且 status=refined，实际 %d 行 / status=%q —— "+
			"用例前提没建立，后面「不许被冲掉」这条就没有对照", len(afterRefine),
			func() string {
				if len(afterRefine) == 0 {
					return ""
				}
				return afterRefine[0].Status
			}())
	}

	// ③ 精校后那次同步（§104 的同步②）。
	second := syncIdentityDo(t, srv, tok, http.MethodPost, "/api/meetings", syncBody)
	if second.Code != http.StatusCreated {
		t.Fatalf("二次同步应 201，实际 %d body=%s", second.Code, strings.TrimSpace(second.Body.String()))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode 201 body: %v", err)
	}
	if created.ID != upsertClientID {
		t.Fatalf("第二次同步返回的 id=%q，期望就是客户端手里那个 %q —— "+
			"服务端另发号时客户端无从得知该用哪个 ID 调 refine", created.ID, upsertClientID)
	}

	rows, total := meetingList(t, srv, tok)
	if total != len(rows) {
		t.Fatalf("total=%d 与 meetings 数组长度 %d 对不上 —— 判据读的口径不一致", total, len(rows))
	}
	ids := make([]string, 0, len(rows))
	for _, m := range rows {
		ids = append(ids, m.ID)
	}
	if err := singleSyncedRow(ids, upsertClientID); err != nil {
		t.Fatalf("同一次录音同步两次后不该留两行：%v", err)
	}

	// ★ 上面这条「只有 upsert 分支能守」的半句：重新同步不许回退已写入的状态。
	if rows[0].Status != "refined" {
		t.Fatalf("二次同步把 status 从 refined 冲回了 %q ⇒ 刚做完的精校在列表上"+
			"又显示成「录音中」，用户会以为没生效（CreateScoped 造的是全新 Meeting）\n"+
			"  第二次 POST 的响应体（upsert 分支会带回原 status，create 分支是 recording）：%s",
			rows[0].Status, strings.TrimSpace(second.Body.String()))
	}
	if rows[0].CreatedAt != createdAt {
		t.Fatalf("二次同步把 created_at 从 %q 改成了 %q ⇒ 同一条会议的创建时间"+
			"被重新同步刷新了，列表排序会把它顶到最前面", createdAt, rows[0].CreatedAt)
	}
	t.Logf("同步①→refine→同步② ⇒ 列表 %d 行，id=%s status=%s created_at 未变",
		total, ids[0], rows[0].Status)
}

// ── 承重门 2：客户端手里的 ID 必须能直接调精校（§96 那条 404 的反面）──

// TestRefineRunsWithTheClientIDTheFrontendHolds 是本文件里最直接对应用户需求的一条：
// 「录音完成后还需要一次精校」—— 前端就是拿 local_meetings 的 ID 去调 /refine。
//
// ⚠ 断言两件事，缺一不可（§96 M2 的教训）：
//
//	① 状态码不是 404；
//	② **LLM 真被调了**。只钉状态码的话，把守卫挪到 LLM 之后、只在写响应时
//	   返回 404 同样全绿 —— 而那意味着每次无效精校都先烧一份真实 token
//	   与 90s 预算。「没报错」与「钱没白花、链真通了」是两件事。
func TestRefineRunsWithTheClientIDTheFrontendHolds(t *testing.T) {
	srv, tokens, llm := syncIdentityServer(t)
	tok := tokens["ws-a"]

	create := syncIdentityDo(t, srv, tok, http.MethodPost, "/api/meetings", syncBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("创建会议应 201，实际 %d body=%s", create.Code, strings.TrimSpace(create.Body.String()))
	}

	before := llm.calls
	rr := syncIdentityDo(t, srv, tok, http.MethodPost,
		"/api/meetings/"+upsertClientID+"/refine", refineSegmentsBody)

	if rr.Code == http.StatusNotFound {
		t.Fatalf("用客户端自己的 ID 调 refine 却 404 ⇒ 精校链在生产是死的（§96 的读数回归了）：%s",
			strings.TrimSpace(rr.Body.String()))
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("refine 应 200，实际 %d body=%s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if llm.calls == before {
		t.Fatal("refine 返回 200 但一次 LLM 都没调 ⇒ 读到的是兜底原文，" +
			"「精校」这个名字是假的（断言状态码不够，得钉昂贵副作用）")
	}
	t.Logf("refine 200 且 LLM +%d ⇒ 客户端 ID 直通精校链", llm.calls-before)
}

// ── 承重门 3：跨作用域撞号必须退回服务端 ID，绝不覆盖 ─────────────────

// TestClientIDCollisionAcrossWorkspacesNeverOverwrites 钉住方案 A 唯一的真实风险：
// `id` 是**全局**主键，不是 (owner, workspace) 复合键。
//
// 两个工作区各有一个用户，各自用 `meeting-<ms>-<6位随机>`。随机撞上的概率很低，
// 但撞上时如果「有 ID 就更新」，就会把**别人工作区的会议顶掉** ——
// workspace 隔离当场失效，而且没有任何报错。
//
// ⚠ 两个 token 共用同一个 owner（shared-user）、只有 workspace 不同，
//
//	所以这条正好测的是「同 owner 跨 workspace」这一头：它最容易被
//	「按 owner 判重」的写法漏掉。
func TestClientIDCollisionAcrossWorkspacesNeverOverwrites(t *testing.T) {
	srv, tokens, _ := syncIdentityServer(t)

	// ws-a 先占住客户端 ID。
	a := syncIdentityDo(t, srv, tokens["ws-a"], http.MethodPost, "/api/meetings", syncBody)
	if a.Code != http.StatusCreated {
		t.Fatalf("ws-a 创建应 201，实际 %d body=%s", a.Code, strings.TrimSpace(a.Body.String()))
	}
	var first struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(a.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode ws-a 201 body: %v", err)
	}
	if first.ID != upsertClientID {
		t.Fatalf("ws-a 应占住客户端 id，实际 %q", first.ID)
	}

	// ws-b 拿**同一个**客户端 ID 来同步 —— 这就是撞号。
	collide := strings.Replace(syncBody, `"悬界芯片客户评审会"`, `"完全不同的另一场会"`, 1)
	b := syncIdentityDo(t, srv, tokens["ws-b"], http.MethodPost, "/api/meetings", collide)
	if b.Code != http.StatusCreated {
		t.Fatalf("撞号不应报错，应退回服务端发号后 201；实际 %d body=%s",
			b.Code, strings.TrimSpace(b.Body.String()))
	}
	var second struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode ws-b 201 body: %v", err)
	}
	if second.ID == upsertClientID {
		t.Fatal("ws-b 拿到了已被 ws-a 占用的 ID ⇒ 内存 store 盲写覆盖了别人的行，" +
			"workspace 隔离当场失效")
	}

	// ws-a 那一行必须原封不动。
	gotA, err := srv.meetingStore.GetScoped(upsertClientID, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("ws-a 的会议被 ws-b 的同步冲掉了：%v", err)
	}
	if gotA.Title != "悬界芯片客户评审会" {
		t.Fatalf("ws-a 的 title 被改成了 %q ⇒ 别人的行被覆盖", gotA.Title)
	}

	// 两个作用域各自都应该只有自己那一行。
	idsA, _ := meetingListIDs(t, srv, tokens["ws-a"])
	idsB, _ := meetingListIDs(t, srv, tokens["ws-b"])
	if len(idsA) != 1 || len(idsB) != 1 {
		t.Fatalf("撞号后 ws-a %d 行 %v / ws-b %d 行 %v ⇒ 各应恰好 1 行",
			len(idsA), idsA, len(idsB), idsB)
	}
	t.Logf("撞号处理：ws-a 保住 %s；ws-b 退回服务端 ID %s", gotA.ID, second.ID)
}

// ── 负控：判据有牙（不靠改生产代码） ─────────────────────────────────

// TestSingleSyncedRowPredicateHasTeeth 把上面那条判据 `singleSyncedRow`
// 喂给一个**违规世界**，必须报失败。
//
// ⚠ 违规世界怎么造才「同形」：直接连着调两次 `CreateScoped`（**绕开 handler
//
//	里的 upsert 分支**）—— 这就是 §105 修好之前这个端点的确切行为，
//	同一个 store、同一张表、同样的两次同步。不用变异脚本、不改生产代码，
//	因为负控要证明的是「判据能咬住」，不是「代码曾经坏过」。
//
// ⚠ 判据必须真的返回错误才算有牙；若它对两行也返回 nil，那三条承重门
//
//	里所有关于行数的断言就都是恒真的。
func TestSingleSyncedRowPredicateHasTeeth(t *testing.T) {
	srv, tokens, _ := syncIdentityServer(t)
	uid, ws := "shared-user", "ws-a"

	// 违规世界：handler 之外直接插两行，正是修复前的语义。
	for range 2 {
		if _, err := srv.meetingStore.CreateScoped(
			meeting.CreateMeetingRequest{Title: "同一次录音"}, uid, ws); err != nil {
			t.Fatalf("构造违规样本失败：%v", err)
		}
	}
	ids, _ := meetingListIDs(t, srv, tokens["ws-a"])
	if len(ids) != 2 {
		t.Fatalf("违规样本本身构造失败：期望 2 行，实际 %d 行 %v", len(ids), ids)
	}
	if err := singleSyncedRow(ids, upsertClientID); err == nil {
		t.Fatalf("判据无牙：对 %d 行也返回 nil ⇒ 所有行数断言恒真", len(ids))
	} else {
		t.Logf("判据在违规世界上如实报错：%v", err)
	}

	// 反面：ID 不对（行数对）也必须报错 —— 否则「返回的是客户端那个 ID」
	// 这半句是没被守住的。
	only := []string{"mtg_1791309724502601000_1"}
	if err := singleSyncedRow(only, upsertClientID); err == nil {
		t.Fatal("判据只数行数不核对 ID：对服务端自发的号也返回 nil ⇒ ID 对账这半句没被守住")
	} else {
		t.Logf("判据也核对 ID：%v", err)
	}
}
