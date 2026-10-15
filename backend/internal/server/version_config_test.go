package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadVersionConfig 的默认路径是相对**进程工作目录**的，不是相对可执行文件。
// 这不是学术问题：两个启动脚本（start-pocketd-pg.ps1 /
// start-pocketd-email-verify.ps1）都从仓库根启 pocketd，原来的实现因此读不到
// backend/config/version.json，而失败分支曾经**静默回落默认值**——App 报 1.2.0，
// 日志里只有一行 Warning，没有任何东西指向「路径不对」。
//
// 路径修好之后，回落本身也被去掉了：拿不到配置就返回 ErrVersionConfigNotFound，
// 理由见 TestLoadVersionConfig_MissingReturnsErrorNotSilentDefaults。
//
// 这里钉住四件事：默认按 CWD 解析、环境变量优先于一切猜测、
// 错误里带路径、缺失不回落；外加 HTTP 层真的把它翻成 503。
func TestLoadVersionConfig_DefaultPathIsCWDRelative(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "version.json"),
		[]byte(`{"version":"9.9.9-cwd","buildNumber":77}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 环境变量必须为空，否则测的是另一条分支。
	t.Setenv("POCKET_VERSION_CONFIG_PATH", "")
	t.Chdir(root)

	v, err := (&Server{}).loadVersionConfig()
	if err != nil {
		t.Fatalf("loadVersionConfig: %v", err)
	}
	if v.Version != "9.9.9-cwd" {
		t.Fatalf("默认路径应相对 CWD 解析，实际拿到 version=%q（回落到默认值就说明它找的是别处）", v.Version)
	}
}

func TestLoadVersionConfig_EnvVarWins(t *testing.T) {
	// 从一个**有** config/version.json 的目录启动，但显式指向另一个文件：
	// 环境变量必须胜出，否则「显式设置会被默认路径悄悄盖掉」这种回归无从发现。
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "version.json"),
		[]byte(`{"version":"from-cwd","buildNumber":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(root, "elsewhere.json")
	if err := os.WriteFile(explicit, []byte(`{"version":"from-env","buildNumber":2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("POCKET_VERSION_CONFIG_PATH", explicit)
	t.Chdir(root)

	v, err := (&Server{}).loadVersionConfig()
	if err != nil {
		t.Fatalf("loadVersionConfig: %v", err)
	}
	if v.Version != "from-env" {
		t.Fatalf("显式 POCKET_VERSION_CONFIG_PATH 必须优先，实际 version=%q", v.Version)
	}
}

func TestLoadVersionConfig_MissingReturnsErrorNotSilentDefaults(t *testing.T) {
	// 这一条**改写过**。原用例钉的是「缺失时静默回落默认值」：
	//
	//	拿不到文件时**不报错**，返回内置的 1.2.0 / build 2 / 一个写死的下载 URL。
	//
	// 那个行为不是「保守」，是**说谎**：回落值与实际发版毫无关系，于是配置路径
	// 写错时（两个启动脚本都从仓库根启 pocketd，这正是当初的真实成因），
	// 真实版本已经到 1.5.0 的用户会被告知「当前已是最新版本」。
	// 一行 Warning 日志不会有人看，一个自信的假版本号会被所有人相信。
	//
	// 现在契约是返回 ErrVersionConfigNotFound，由 handleCheckUpdate 翻成 503。
	// 下面的断言必须用 errors.Is，而不是 err != nil：调用方靠 Is 区分
	// 「配置没配对」(503) 与「配置坏了」(500)，写不成 Is 就退化成 500 风暴。
	missing := filepath.Join(t.TempDir(), "nope.json")
	t.Setenv("POCKET_VERSION_CONFIG_PATH", missing)

	v, err := (&Server{}).loadVersionConfig()
	if !errors.Is(err, ErrVersionConfigNotFound) {
		t.Fatalf("缺失配置必须返回 ErrVersionConfigNotFound，实际 err=%v", err)
	}
	if v != nil {
		t.Fatalf("错误路径上不得同时返回一个「兜底」VersionInfo，实际拿到 %+v —— "+
			"调用方一旦误用这个值，就等于把假版本号又送回 App", v)
	}
	// 错误信息里必须带路径，否则排查者仍然只能猜。
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("错误信息必须含试过的路径 %q，实际 %q", missing, err.Error())
	}
}

// 上面那条只保证 loadVersionConfig 的契约；这条保证契约**真的传到了 HTTP 层**。
// 漏掉它就会出现「函数已经不回落了，但 handleCheckUpdate 还在 500」——
// 用户看到的还是「检查更新失败」，只是失败原因变得更难猜。
func TestHandleCheckUpdate_MissingConfigIs503WithDiagnosticBody(t *testing.T) {
	t.Setenv("POCKET_VERSION_CONFIG_PATH", filepath.Join(t.TempDir(), "nope.json"))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/app/check-update?version=1.2.0", nil)
	(&Server{}).handleCheckUpdate(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("配置缺失应是 503（部署问题），实际 %d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("错误响应也必须是 JSON：App 侧 checkUpdate() 会走 assertNotHTML，实际 Content-Type=%q", ct)
	}
	var body struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v body=%s", err, rr.Body.String())
	}
	if body.Error != "version_config_not_found" {
		t.Fatalf("错误码应可被前端/脚本识别，实际 %q", body.Error)
	}
	if !strings.Contains(body.Detail, "nope.json") {
		t.Fatalf("响应体必须回带试过的路径，实际 %q", body.Detail)
	}
}

// 反向：配置**在**时必须 200 且给出真实版本，不是 503。
// 只写上一条的话，「永远 503」也能全绿。
func TestHandleCheckUpdate_ValidConfigStillReturns200(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "version.json")
	if err := os.WriteFile(cfg, []byte(`{"version":"1.5.0","buildNumber":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POCKET_VERSION_CONFIG_PATH", cfg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/app/check-update?version=1.2.0&build=2", nil)
	(&Server{}).handleCheckUpdate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("配置存在时必须 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		HasUpdate bool `json:"hasUpdate"`
		Latest    *struct {
			Version string `json:"version"`
		} `json:"latest"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.HasUpdate || body.Latest == nil || body.Latest.Version != "1.5.0" {
		t.Fatalf("必须按真实配置判定更新，实际 %s", rr.Body.String())
	}
}

// 客户端**已经是**最新版时，GET 分支必须回 false。
//
// 这条钉的是一个已实测的缺陷，且它解释了为什么上面那些用例抓不到：
// handleCheckUpdate 的 GET 分支只从 query 读 version，**从不读 build**，
// CurrentBuild 因此停在 0；而 hasUpdateAvailable 里有
// `currentBuild < latestBuild` 这一项，latestBuild 来自 version.json。
// 于是只要 latestBuild > 0，这一项恒成立，hasUpdate 变成**恒真**。
//
// 症状不是「少推一次」而是**多推一次**：已经是最新的客户端被告知「发现新版本」。
// 上一条用例（客户端 1.2.0/2 对服务端 1.5.0/9）无论 GET 分支怎么坏都会绿，
// 因为那个样本里 version 与 build 两个方向都指向「有更新」——
// 换句话说，它测不出「两个实现分叉」的地方。
//
// 样本取自实测读数（latest=1.2.0/build=2）：
//
//	version=1.0.0 / 1.2.0 / 1.3.0 / 2.0.0 / 99.0.0  全部 hasUpdate=true
//
// 99.0.0 尤其重要：它比服务端**更新**，正确结果仍是 false（不许通知降级）。
func TestHandleCheckUpdate_GetPath_SameVersionNoUpdate(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "version.json")
	if err := os.WriteFile(cfg, []byte(`{"version":"1.2.0","buildNumber":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POCKET_VERSION_CONFIG_PATH", cfg)

	cases := []struct {
		name    string
		query   string
		want    bool
		comment string
	}{
		{"完全相同", "?version=1.2.0&build=2", false, "客户端与服务端同版本同 build ⇒ 无更新"},
		{"客户端更新", "?version=2.0.0&build=2", false, "比服务端新 ⇒ 不得通知降级"},
		{"远新于服务端", "?version=99.0.0&build=2", false, "同上，且差距悬殊"},
		{"build 缺失", "?version=1.2.0", true, "不传 build 时按 0 算，而 latest build=2 ⇒ 判有更新。保守方向：宁可多提示一次，不漏"},
		{"确实较旧", "?version=1.0.0&build=1", true, "真的旧 ⇒ 必须推"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/app/check-update"+tc.query, nil)
			(&Server{}).handleCheckUpdate(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("必须 200，实际 %d body=%s", rr.Code, rr.Body.String())
			}
			var body struct {
				HasUpdate bool   `json:"hasUpdate"`
				Message   string `json:"message"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.HasUpdate != tc.want {
				t.Fatalf("GET %s：hasUpdate 期望 %v，实际 %v（%s）。%s",
					tc.query, tc.want, body.HasUpdate, body.Message, tc.comment)
			}
		})
	}
}

// 缺 build 参数时行为必须明确且**可预期**：按 0 处理 ⇒ 当成「拿不到自己的
// build 号」，于是 build 这一路判不出更新，但 version 那一路仍要能判。
// 用 version 明显较旧的样本钉住：即便 build 缺失，也不能因此漏判。
//
// 这一条同时是上面那条的**反向对照**：如果实现把 build 缺失当成「最新」
// （例如直接返回 false），「确实较旧」用例会红——两条一起才抓得住「恒真」与
// 「恒假」这两个相反方向的坑。
func TestHandleCheckUpdate_GetPath_BuildMissingStillComparesVersion(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "version.json")
	if err := os.WriteFile(cfg, []byte(`{"version":"1.10.0","buildNumber":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POCKET_VERSION_CONFIG_PATH", cfg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/app/check-update?version=1.9.0", nil)
	(&Server{}).handleCheckUpdate(rr, req)

	var body struct {
		HasUpdate bool `json:"hasUpdate"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// 1.9.0 → 1.10.0 正是字典序会判错的那个样本（"1.9" > "1.10"）。
	// 这里它必须仍然判出更新，否则字典序回归无人发现。
	if !body.HasUpdate {
		t.Fatalf("1.9.0 对 1.10.0 必须判有更新（字典序陷阱），实际 %s", rr.Body.String())
	}
}
