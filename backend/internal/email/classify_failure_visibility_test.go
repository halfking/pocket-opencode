package email

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

// classify_failure_visibility_test.go —— 分类**逐条失败**必须可见。
//
// ## 缺陷
//
// `ClassifyUnclassified` 原本有三处裸 `continue`：kx 调用出错/返回空、写库出错、
// BuildClassifyWrites 产出空。函数照常返回 (成功数, nil)，而三个调用点又都写成
// `if _, err := ...`，把成功数也丢了：
//
//	「这批没有待分类邮件」   → (0, nil)
//	「20 封全部分类失败」   → (0, nil)   ← 与上一行完全相同
//
// 后果是**分类整体失效不留任何痕迹**：日志没有、报告没有、error 是 nil。
// 而需求 4 完全依赖 importance 被写进去——写不进去就永远不提醒，
// 报告上只剩 RemindersUnclassified 一个数字，读起来与「还没轮到分类」
// 一模一样。
//
// 真实库 2026-10-02 观测到的正是这个形态：当天 7 封到信，2 封有 importance，
// 5 封没有，其中 3 封已取回但从未被分类过。
//
// 本文件钉住「失败必须进 error」这个不变量。

// failingKxmem 的 ClassifyEmails 永远报错，模拟上游 5xx / 超时 / 断网。
type failingKxmem struct {
	fakeKxmem
	calls int
}

func (f *failingKxmem) ClassifyEmails(context.Context, kxmemory.ClassifyEmailsRequest) (*kxmemory.ClassifyEmailsResponse, error) {
	f.calls++
	return nil, errors.New("kxmemory: 503 service unavailable")
}

// 全部失败时**必须**返回非 nil error。
//
// 旧实现在这里返回 (0, nil)——本用例就是它的负控。
func TestClassifyUnclassified_SurfacesTotalFailure(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-fail", "u", "ws-fail")
	seedUnclassifiedEmail(t, store, "em-fail-1", "acct-fail", "ws-fail")

	kx := &failingKxmem{}
	n, err := ClassifyUnclassified(ctx, store, kx, "u", "ws-fail", 10)

	if err == nil {
		t.Fatalf("分类 100%% 失败却返回 err=nil —— 失败被吞掉了。"+
			"调用点的 `if _, err := ...` 会把整件事当成功，"+
			"日志和报告里都不会出现任何痕迹。classified=%d calls=%d", n, kx.calls)
	}
	// 错误文案必须带得上「失败了几封」，否则调用方打出来的日志只有一个
	// 光秃秃的 error，排查时还得回去数。
	if !strings.Contains(err.Error(), "1/1") {
		t.Errorf("错误未带失败计数: %v（期望形如 \"1/1 封分类失败\"）", err)
	}
	if n != 0 {
		t.Errorf("classified=%d，want 0（全部失败时不得计入成功数）", n)
	}
	if kx.calls != 1 {
		t.Errorf("kx 调用 %d 次，want 1 —— 前提断言：待分类邮件没被取到，结论不成立", kx.calls)
	}
	assertStillUnclassified(t, store, "em-fail-1")
}

// 部分失败：**成功的那几封必须真的落库**，同时整体仍要报出失败。
//
// 这一条防的是「为了让错误可见，把整批都判失败」这种过度反应——
// 那会把已经分类好的邮件也拖下水。
func TestClassifyUnclassified_PartialFailureKeepsSuccesses(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-mix", "u", "ws-mix")
	seedUnclassifiedEmail(t, store, "em-mix-ok", "acct-mix", "ws-mix")
	seedUnclassifiedEmail(t, store, "em-mix-bad", "acct-mix", "ws-mix")

	kx := &okOnOneFailingKxmem{okID: "em-mix-ok"}
	n, err := ClassifyUnclassified(ctx, store, kx, "u", "ws-mix", 10)

	if err == nil {
		t.Fatal("1/2 失败却返回 err=nil —— 部分失败同样不可见")
	}
	if !strings.Contains(err.Error(), "1/2") {
		t.Errorf("错误未带失败计数: %v（期望 \"1/2 封分类失败\"）", err)
	}
	if n != 1 {
		t.Errorf("classified=%d，want 1（成功的那封必须照常计入）", n)
	}
	// 关键：成功的必须真的写进库了。不能为了「报告失败」把成功也一起回滚。
	var imp, cat string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(importance,''), COALESCE(category,'') FROM emails WHERE id='em-mix-ok'`,
	).Scan(&imp, &cat); err != nil {
		t.Fatalf("read back em-mix-ok: %v", err)
	}
	if imp != "high" || cat == "" {
		t.Fatalf("em-mix-ok importance=%q category=%q —— 部分失败把成功的也拖下水了", imp, cat)
	}
	assertStillUnclassified(t, store, "em-mix-bad")
}

// 对照组：全部成功时**不得**返回 error。
//
// 防的是过度修复：把所有 continue 都改成「记为失败」会让正常路径也开始报错，
// 每天的日志被无意义的告警淹没，真出事时反而看不见。
func TestClassifyUnclassified_AllSuccessReturnsNoError(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-ok", "u", "ws-ok")
	seedUnclassifiedEmail(t, store, "em-ok-1", "acct-ok", "ws-ok")
	seedUnclassifiedEmail(t, store, "em-ok-2", "acct-ok", "ws-ok")

	kx := &classifyReasonKxmem{reason: "含截止日期"}
	n, err := ClassifyUnclassified(ctx, store, kx, "u", "ws-ok", 10)

	if err != nil {
		t.Fatalf("全部成功却返回 err=%v —— 判据被写坏了，告警会淹没真故障", err)
	}
	if n != 2 {
		t.Errorf("classified=%d，want 2", n)
	}
}

// 上游返回了结果但我们判它不可用（EmailID 空 / 归一化后 category 空），
// 同样属于「邮件停在未分类」，必须可见。
//
// 这条覆盖的是第三处 continue。它曾经连「上游成功」这个事实都不记录，
// 于是从外部看与「上游超时」完全一样。
func TestClassifyUnclassified_SurfacesUnusableResult(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-bad", "u", "ws-bad")
	seedUnclassifiedEmail(t, store, "em-bad", "acct-bad", "ws-bad")

	kx := &emptyCategoryKxmem{}
	n, err := ClassifyUnclassified(ctx, store, kx, "u", "ws-bad", 10)

	if err == nil {
		t.Fatal("上游返回了结果但不可用，却返回 err=nil —— 这封会永远停在未分类且无人知晓")
	}
	if !strings.Contains(err.Error(), "category") {
		t.Errorf("错误文案没有点出是 category 的问题: %v", err)
	}
	if n != 0 {
		t.Errorf("classified=%d，want 0", n)
	}
	assertStillUnclassified(t, store, "em-bad")
}

// okOnOneFailingKxmem 只对 okID 返回有效结果，其余一律报错。
type okOnOneFailingKxmem struct {
	fakeKxmem
	okID string
}

func (f *okOnOneFailingKxmem) ClassifyEmails(_ context.Context, req kxmemory.ClassifyEmailsRequest) (*kxmemory.ClassifyEmailsResponse, error) {
	results := make([]kxmemory.EmailClassificationResult, 0, len(req.Emails))
	for _, e := range req.Emails {
		if e.EmailID != f.okID {
			return nil, errors.New("kxmemory: upstream timeout")
		}
		results = append(results, kxmemory.EmailClassificationResult{
			EmailID: e.EmailID, Category: "work", Importance: "high",
			Summary: "需回复", ActionReason: "含截止日期",
		})
	}
	return &kxmemory.ClassifyEmailsResponse{Results: results}, nil
}

// emptyCategoryKxmem 返回一条「结果存在但不可用」的分类：category 归一化后为空。
type emptyCategoryKxmem struct {
	fakeKxmem
}

func (f *emptyCategoryKxmem) ClassifyEmails(_ context.Context, req kxmemory.ClassifyEmailsRequest) (*kxmemory.ClassifyEmailsResponse, error) {
	results := make([]kxmemory.EmailClassificationResult, 0, len(req.Emails))
	for _, e := range req.Emails {
		// Importance=high 但 Category="（无）"——归一化后仍非空时会通过，
		// 故这里用一个必然被 NormalizeCategory 判为空的字面量。
		results = append(results, kxmemory.EmailClassificationResult{
			EmailID: e.EmailID, Category: "", Importance: "high",
		})
	}
	return &kxmemory.ClassifyEmailsResponse{Results: results}, nil
}

// seedUnclassifiedEmail 造一封「已入库但未归类」的邮件。
// 只写最小列：importance / category 留空即代表未分类，与生产状态一致。
func seedUnclassifiedEmail(t *testing.T, store *Store, id, accountID, workspaceID string) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ($1, $2, $3, $4, 's@example.com', '需要你确认预算', 'snippet',
		        1700000000, 1700000000)`, id, accountID, workspaceID, id+"@m"); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func assertStillUnclassified(t *testing.T, store *Store, id string) {
	t.Helper()
	var imp, cat string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT COALESCE(importance,''), COALESCE(category,'') FROM emails WHERE id=$1`, id,
	).Scan(&imp, &cat); err != nil {
		t.Fatalf("read back %s: %v", id, err)
	}
	if imp != "" || cat != "" {
		t.Fatalf("%s 分类失败却留下了 importance=%q category=%q", id, imp, cat)
	}
}
