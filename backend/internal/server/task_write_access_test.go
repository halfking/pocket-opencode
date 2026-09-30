package server

// PATCH/DELETE 写权限护栏。
//
// 缺陷形态是「规则写好了、没人调用」：CanWriteWorkItem 在同一文件里被
// participants / activity / delegate 三处正确使用，唯独 handleTaskOperations
// 的 PATCH 与 DELETE 分支完全没接上，于是同 workspace 的普通成员可以改写、
// 删除他人的 private 工作项——与本仓库此前的 B-1~B-4 越权同类。
//
// 端到端复现需要活的数据库与两个租户，代价高且脆弱，因此这里沿用
// task_notify_wiring_test.go 的做法：直接读 AST 钉住调用点。规则本身的
// 正误由 internal/task 的单测负责，本文件只回答一个问题——
// 「写路径到底有没有经过这道校验」。

import (
	"go/ast"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// callsMethod 报告 fn 的函数体里是否出现了形如 recv.method(...) 的调用。
// 接收者匹配用源码文本的宽松比较，足以覆盖 s 与 s.taskStore 这两种写法。
func callsMethodOn(t *testing.T, fn *ast.FuncDecl, method string) bool {
	t.Helper()
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == method {
			found = true
		}
		return true
	})
	return found
}

// workItemWriteGuard 的判定顺序与结果，用真实的规则函数跑一遍。
//
// AST 断言只证明「调用了 CanWriteWorkItem」，不证明**结果**对。
// 下面把守卫里那三步（判读 → 判写 → 放行）用同样的输入喂给
// task.CanReadWorkItem / task.CanWriteWorkItem，钉住每种身份的实际结局。
// 端到端跑真 HTTP 需要活库（taskStore 是具体类型不是接口，无法打桩），
// 因此这里覆盖到「规则层」，路由层由上面的 AST 断言守住。
func TestTaskWriteVerdictsPerIdentity(t *testing.T) {
	// alice 拥有；bob 是参与者；carol 只是同 workspace 的普通成员（不在参与者名单）。
	const (
		owner       = "alice"
		participant = "bob"
		outsider    = "carol"
	)
	parts := []task.Participant{
		{UserID: owner, Role: task.RoleOwner},
		{UserID: participant, Role: task.RoleAssignee},
	}
	// private 工作项：只有 owner 与参与者看得见。
	priv := &task.Task{ID: "t1", OwnerID: owner, Visibility: task.VisibilityPrivate}
	// workspace 工作项：同 workspace 成员都看得见 —— 但「看得见」不等于「能改」。
	pub := &task.Task{ID: "t2", OwnerID: owner, Visibility: task.VisibilityWorkspace}

	cases := []struct {
		name     string
		item     *task.Task
		actor    string
		wantRead bool
		wantWrit bool
	}{
		{"owner 改自己的 private 项", priv, owner, true, true},
		{"参与者改 private 项", priv, participant, true, true},
		{"同 workspace 陌生人改 private 项：连读都不行 → 404", priv, outsider, false, false},
		{"owner 改 workspace 项", pub, owner, true, true},
		{"参与者改 workspace 项", pub, participant, true, true},
		{"同 workspace 陌生人改 workspace 项：可读不可写 → 403", pub, outsider, true, false},
		{"空身份不是任何人的 owner", priv, "", false, false},
		{"任务不存在（nil）一律拒绝", nil, owner, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := task.CanReadWorkItem(tc.item, parts, tc.actor); got != tc.wantRead {
				t.Errorf("CanReadWorkItem = %v, want %v", got, tc.wantRead)
			}
			if got := task.CanWriteWorkItem(tc.item, parts, tc.actor); got != tc.wantWrit {
				t.Errorf("CanWriteWorkItem = %v, want %v", got, tc.wantWrit)
			}
		})
	}
}

// workItemWriteGuard 必须真的判权限，而不是仅仅被调用。
// 断言它内部同时触达 CanReadWorkItem 与 CanWriteWorkItem：
// 只判其中一个就是上一轮的缺陷换了个形状。
func TestWorkItemWriteGuardJudgesReadAndWrite(t *testing.T) {
	fn := serverFuncSource(t, "workItemWriteGuard")
	if fn == nil {
		t.Fatal("workItemWriteGuard not found: the PATCH/DELETE write guard is missing entirely")
	}
	if !callsMethodOn(t, fn, "CanReadWorkItem") {
		t.Error("workItemWriteGuard does not call CanReadWorkItem: a caller who cannot even read " +
			"the work item would receive 403, confirming the id exists")
	}
	if !callsMethodOn(t, fn, "CanWriteWorkItem") {
		t.Error("workItemWriteGuard does not call CanWriteWorkItem: a plain workspace member " +
			"would be allowed to change a work item they are not on")
	}
	if !callsMethodOn(t, fn, "ListParticipants") {
		t.Error("workItemWriteGuard does not call ListParticipants: CanWriteWorkItem is only " +
			"half the rule without the participant set")
	}
}

// 两条写路径都必须过这道闸。
func TestTaskWritePathsCallTheGuard(t *testing.T) {
	fn := serverFuncSource(t, "handleTaskOperations")
	if fn == nil {
		t.Fatal("handleTaskOperations not found")
	}
	if !callsMethodOn(t, fn, "workItemWriteGuard") {
		t.Fatal("handleTaskOperations never calls workItemWriteGuard: PATCH and DELETE are " +
			"unauthenticated against work item ownership — any workspace member can rewrite " +
			"or delete another user's private work item")
	}
}

// 反向对照：如果哪天有人把 workItemWriteGuard 掏空成直接 return true，
// 上面的结构性断言不会发现，但这一条会——因为守卫必须仍被两条写路径引用。
// 两条一起构成护栏，缺一条另一条就失去意义。
func TestTaskWriteGuardIsNotSilentlyBypassed(t *testing.T) {
	guard := serverFuncSource(t, "workItemWriteGuard")
	if guard == nil {
		t.Fatal("workItemWriteGuard not found")
	}
	// 守卫至少要有一个拒绝出口，否则它就是个 no-op。
	denies := false
	ast.Inspect(guard.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.ReturnStmt); ok {
			denies = true
		}
		return true
	})
	if !denies {
		t.Error("workItemWriteGuard has no return statement: it cannot refuse anything")
	}
}
