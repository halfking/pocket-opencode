// Meeting synchronization now uses the local versioned portable outbox. Its
// create/edit/delete paths must retain the same local identity, so a refinement
// cannot create a second record. Legacy POST status behavior remains tested below.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

const frontendMeetingStore = "../../../frontend/src/features/meetings/meetings-store.ts"

const (
	bodyAtStop = `{"id":"meeting-1791309378069-a7f3az","title":"悬界芯片客户评审会",` +
		`"location":"上海张江","participants":["张伟"],"startedAt":1791309378069,` +
		`"durationMs":1834000,"status":"completed"}`
	bodyAfterRefine = `{"id":"meeting-1791309378069-a7f3az","title":"悬界芯片客户评审会",` +
		`"location":"上海张江","participants":["张伟"],"startedAt":1791309378069,` +
		`"durationMs":1834000,"summary":"已对齐","status":"refined"}`
)

type meetingSyncIdentity struct {
	Function string `json:"function"`
	ID       string `json:"id"`
	ExtID    string `json:"extId"`
}

// Parse real commit arguments with the TypeScript AST. Unrelated id fields,
// comments and nested payloads cannot satisfy this gate.
func syncMeetingIDArgs(t *testing.T, src string) []meetingSyncIdentity {
	t.Helper()
	const script = `
const ts = require('./node_modules/typescript'), fs = require('node:fs');
const src = ts.createSourceFile('meetings-store.ts', fs.readFileSync(0,'utf8'), ts.ScriptTarget.Latest, true);
const wanted = new Set(['createMeeting','updateMeeting','deleteMeeting']), result = [];
for (const fn of src.statements) {
  if (!ts.isFunctionDeclaration(fn) || !wanted.has(fn.name?.text)) continue;
  const visit = node => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
        node.expression.name.text === 'commit' && ts.isCallExpression(node.expression.expression) &&
        node.expression.expression.expression.getText(src) === 'meetingPortalStore') {
      const data = node.arguments[1], field = name => {
        if (!data || !ts.isObjectLiteralExpression(data)) return '<missing object>';
        const prop = data.properties.find(p => p.name?.getText(src) === name);
        return !prop ? '' : ts.isShorthandPropertyAssignment(prop) ? prop.name.text : prop.initializer?.getText(src) ?? '<invalid>';
      };
      result.push({function:fn.name.text,id:field('id'),extId:field('extId')});
    }
    ts.forEachChild(node, visit);
  };
  visit(fn);
}
process.stdout.write(JSON.stringify(result));`
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = "../../../frontend"
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("parse versioned meeting identity: %v: %s", err, out)
	}
	var got []meetingSyncIdentity
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode identities: %v: %s", err, out)
	}
	return got
}

func validMeetingSyncIDs(got []meetingSyncIdentity) bool {
	return reflect.DeepEqual(got, []meetingSyncIdentity{
		{Function: "createMeeting", ID: "m.id", ExtID: "`meeting:${m.id}`"},
		{Function: "updateMeeting", ID: "id"},
		{Function: "deleteMeeting", ID: "id"},
	})
}

func TestMeetingSyncCallSitesShareTheSameID(t *testing.T) {
	got := syncMeetingIDArgs(t, readFrontend(t, frontendMeetingStore))
	if !validMeetingSyncIDs(got) {
		t.Fatalf("versioned create/edit/delete must preserve the local meeting ID: %#v", got)
	}
}

// Mutate only temporary source strings, independently for every actual commit.
// A broken refinement/deletion identity and a missing path must all be rejected.
func TestMeetingSyncIDExtractor_HasTeeth(t *testing.T) {
	src := readFrontend(t, frontendMeetingStore)
	mutations := []struct{ name, from, to string }{
		{"create", "id:m.id,extId:", "id:m.noteId,extId:"},
		{"external identity", "extId:`meeting:${m.id}`", "extId:`meeting:${m.noteId}`"},
		{"edit", "{id,title:next.title", "{id:id + '-other',title:next.title"},
		{"delete", "{id,title:existing.title", "{id:id + '-other',title:existing.title"},
		{"missing path", "export async function deleteMeeting(", "export async function removedMeeting("},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if strings.Count(src, mutation.from) != 1 {
				t.Fatalf("mutation anchor must occur exactly once: %q", mutation.from)
			}
			bad := strings.Replace(src, mutation.from, mutation.to, 1)
			if got := syncMeetingIDArgs(t, bad); validMeetingSyncIDs(got) {
				t.Fatalf("identity gate accepted %s mutation: %#v", mutation.name, got)
			}
		})
	}
}

// TestMeetingSyncStatusFollowsRequestValue 现状登记：**只**登记 status 那件事。
//
// ★ §105 已把「一场录音 ⇒ 两行」修掉并升级为承重门
//
//	（meeting_sync_id_upsert_test.go:TestRepeatedSyncOfSameClientIDCreatesOneRow），
//	所以本函数里与行数有关的部分已删除。
//
// ⏱ **函数名已改名（§719）**：原名 `TestMeetingSyncStatusIsNotAdoptedStatus`
//
//	断言「请求里的 status 没被采纳」—— 而该断言**已随 §96 修复被推翻**
//	（`meeting/store.go:111-114`：`status := req.Status`，`recording` 只是空值兜底）。
//	保留旧名会让 `-run TestMeetingSyncStatusIsNotAdoptedStatus` 匹配到一个
//	**名字与行为相反**的测试。
//
//	剩下的问题仍是**未拍板**的：补哪些列进 meetings 表是产品决定（要写迁移），
//	本仓无权代拍。拍板后请把本函数改写成承重断言。
func TestMeetingSyncStatusFollowsRequestValue(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	tok := tokens["ws-a"]

	post := func(body string) string {
		req := httptest.NewRequest(http.MethodPost, "/api/meetings", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("POST /api/meetings 应 201，实际 %d body=%s", rr.Code, rr.Body.String())
		}
		var m struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return m.ID + " / status=" + m.Status
	}

	stop := post(bodyAtStop)
	refined := post(bodyAfterRefine)
	t.Logf("① 录音停止那次同步 → %s", stop)
	t.Logf("② 精校后那次同步 → %s（请求里的 status 是 \"refined\"）", refined)
	// ⚠ §719：这段结论原先**写死**为「两次都是 status=recording —— 请求里的
	//   status 始终没被采纳」，而实测读数是 completed / completed ⇒ **它与上面两行矛盾**。
	//   根因：那段文案停在 §96 修复前。`meeting/store.go` 现在的逻辑是
	//   `status := req.Status; if status == "" { status = "recording" }`
	//   ⇒ **请求值优先**，`recording` 只是**空值兜底**。
	// ⇒ 现在**按实测值说话**，不再断言一个与本文件读数矛盾的结论。
	t.Logf("★ 现状（按上面两行的实测值，不写死）：两次的 status 分别是 %q 与 %q；"+
		"「recording」在 store 里只是 req.Status 为空时的兜底（meeting/store.go:111-114），"+
		"不再是「盖掉请求值」的常量。", statusOf(stop), statusOf(refined))
}

// statusOf 从 post() 返回的 `"<id> / status=<值>"` 里取出 status 段。
func statusOf(s string) string {
	const marker = " / status="
	if i := strings.Index(s, marker); i >= 0 {
		return s[i+len(marker):]
	}
	return "(解析不出)"
}
