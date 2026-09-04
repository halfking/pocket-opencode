package marketplace

// deps_test.go — 依赖解析单测。
//
// 主体是纯内存测试：用 map 模拟 resolver，不连 PG；最后一条 PG 集成测试
// 复用 store_test.go 的 newTestStore harness，无 DSN 时自动 skip。
// 测试名统一带 "Deps"，方便 `-run 'Dependency|Deps'` 过滤。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// depsMapResolver 纯内存 resolver：key 为 "pkg@ver"。calls 记录每个 key
// 被查询的次数，用于断言去重（同一节点不应重复取回）。
type depsMapResolver struct {
	manifests map[string]Manifest
	calls     map[string]int
}

func newDepsMapResolver() *depsMapResolver {
	return &depsMapResolver{
		manifests: make(map[string]Manifest),
		calls:     make(map[string]int),
	}
}

func (r *depsMapResolver) add(pkg, ver string, deps ...Dependency) {
	r.manifests[pkg+"@"+ver] = Manifest{Version: ver, Digest: "sha256:" + pkg, Dependencies: deps}
}

func (r *depsMapResolver) GetVersion(_ context.Context, packageID, version string) (*Manifest, error) {
	key := packageID + "@" + version
	r.calls[key]++
	m, ok := r.manifests[key]
	if !ok {
		// 缺失语义与 storeResolver 一致：ErrMarketplaceNotFound。
		return nil, ErrMarketplaceNotFound
	}
	// 返回副本，避免调用方误改共享 map 里的 manifest。
	cp := m
	return &cp, nil
}

// depsFailingResolver 在指定 key 上注入基础设施错误，其余走 map resolver。
type depsFailingResolver struct {
	*depsMapResolver
	failKey string
	err     error
}

func (r *depsFailingResolver) GetVersion(ctx context.Context, packageID, version string) (*Manifest, error) {
	if packageID+"@"+version == r.failKey {
		return nil, r.err
	}
	return r.depsMapResolver.GetVersion(ctx, packageID, version)
}

// depsChain 构造 root→dep-1→…→dep-n 的线性链（root 本身不入 resolver），
// 返回 root manifest 与 resolver。dep-i 位于第 i 层，共 n 层依赖。
func depsChain(n int) (Manifest, *depsMapResolver) {
	r := newDepsMapResolver()
	for i := 1; i <= n; i++ {
		pkg := fmt.Sprintf("dep-%d", i)
		if i < n {
			r.add(pkg, "1", Dependency{PackageID: fmt.Sprintf("dep-%d", i+1), Version: "1"})
		} else {
			r.add(pkg, "1")
		}
	}
	root := Manifest{
		Version:      "1",
		Digest:       "sha256:root",
		Dependencies: []Dependency{{PackageID: "dep-1", Version: "1"}},
	}
	return root, r
}

// TestDeps_LinearChainTopoOrder 线性链 root→b→c：拓扑序应为 c 在 b 前
// （install 顺序），且重复声明同一依赖不产生重复结果、不重复取回。
func TestDeps_LinearChainTopoOrder(t *testing.T) {
	r := newDepsMapResolver()
	r.add("b", "1.0.0", Dependency{PackageID: "c", Version: "2.0.0"})
	r.add("c", "2.0.0")
	root := Manifest{
		Version: "0.1.0",
		Digest:  "sha256:root",
		Dependencies: []Dependency{
			{PackageID: "b", Version: "1.0.0"},
			{PackageID: "b", Version: "1.0.0"}, // 完全相同的重复声明：应去重
		},
	}

	got, err := ResolveDependencies(context.Background(), r, root, DefaultMaxDependencyDepth)
	if err != nil {
		t.Fatalf("ResolveDependencies: %v", err)
	}
	want := []Dependency{
		{PackageID: "c", Version: "2.0.0"},
		{PackageID: "b", Version: "1.0.0"},
	}
	if len(got) != len(want) {
		t.Fatalf("closure = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("closure[%d] = %+v, want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}
	if r.calls["b@1.0.0"] != 1 {
		t.Errorf("b@1.0.0 fetched %d times, want 1 (dedup)", r.calls["b@1.0.0"])
	}
}

// TestDeps_DiamondDedupTopoOrder diamond：A→{B,C}，B→D，C→D。
// D 只解析一次且排在 B、C 之前。
func TestDeps_DiamondDedupTopoOrder(t *testing.T) {
	r := newDepsMapResolver()
	r.add("d", "1", Dependency{PackageID: "e", Version: "1"})
	r.add("b", "1", Dependency{PackageID: "d", Version: "1"})
	r.add("c", "1", Dependency{PackageID: "d", Version: "1"})
	r.add("e", "1")
	root := Manifest{Version: "1", Digest: "sha256:a", Dependencies: []Dependency{
		{PackageID: "b", Version: "1"},
		{PackageID: "c", Version: "1"},
	}}

	got, err := ResolveDependencies(context.Background(), r, root, DefaultMaxDependencyDepth)
	if err != nil {
		t.Fatalf("ResolveDependencies: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("closure = %+v, want 4 entries [e d b c]", got)
	}
	indexOf := func(pkg string) int {
		for i, d := range got {
			if d.PackageID == pkg {
				return i
			}
		}
		return -1
	}
	if indexOf("d") != 1 || indexOf("e") != 0 {
		t.Errorf("shared deps must come first: e@%d d@%d in %+v", indexOf("e"), indexOf("d"), got)
	}
	if indexOf("b") == -1 || indexOf("c") == -1 {
		t.Errorf("b and c missing from closure: %+v", got)
	}
	if r.calls["d@1"] != 1 {
		t.Errorf("d@1 fetched %d times, want 1", r.calls["d@1"])
	}
}

// TestDeps_CycleDetection 自依赖与两节点环都必须报 ErrDependencyCycle，
// 且错误信息带环路径。
func TestDeps_CycleDetection(t *testing.T) {
	ctx := context.Background()

	// 自依赖：a@1 依赖自己（resolver 对 a@1 返回同一 manifest）。
	self := newDepsMapResolver()
	self.add("a", "1", Dependency{PackageID: "a", Version: "1"})
	rootSelf := Manifest{Version: "1", Digest: "d", Dependencies: []Dependency{
		{PackageID: "a", Version: "1"},
	}}
	_, err := ResolveDependencies(ctx, self, rootSelf, DefaultMaxDependencyDepth)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("self dependency: want ErrDependencyCycle, got %v", err)
	}
	if !strings.Contains(err.Error(), "a@1 -> a@1") {
		t.Errorf("cycle path missing from error: %v", err)
	}

	// 两节点环：a→b→a。
	two := newDepsMapResolver()
	two.add("a", "1", Dependency{PackageID: "b", Version: "1"})
	two.add("b", "1", Dependency{PackageID: "a", Version: "1"})
	rootTwo := Manifest{Version: "1", Digest: "d", Dependencies: []Dependency{
		{PackageID: "a", Version: "1"},
	}}
	_, err = ResolveDependencies(ctx, two, rootTwo, DefaultMaxDependencyDepth)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("two-node cycle: want ErrDependencyCycle, got %v", err)
	}
	if !strings.Contains(err.Error(), "a@1 -> b@1 -> a@1") {
		t.Errorf("cycle path missing from error: %v", err)
	}
}

// TestDeps_MissingAggregated 一次缺 2 个依赖：错误必须是聚合的
// ErrDependenciesUnresolved，文本同时列出两个缺失项。
func TestDeps_MissingAggregated(t *testing.T) {
	r := newDepsMapResolver() // 空表：全都缺
	root := Manifest{Version: "1", Digest: "d", Dependencies: []Dependency{
		{PackageID: "ghost-x", Version: "1"},
		{PackageID: "ghost-y", Version: "2"},
	}}

	_, err := ResolveDependencies(context.Background(), r, root, DefaultMaxDependencyDepth)
	if !errors.Is(err, ErrDependenciesUnresolved) {
		t.Fatalf("want ErrDependenciesUnresolved, got %v", err)
	}
	for _, m := range []string{"ghost-x@1", "ghost-y@2"} {
		if !strings.Contains(err.Error(), m) {
			t.Errorf("error text missing %q: %v", m, err)
		}
	}
}

// TestDeps_DepthLimit 深度边界：n 层链在 maxDepth=n 恰好通过，
// maxDepth=n-1 报 ErrDependencyTooDeep（带超限包名），
// maxDepth=0 / 负数视为非法并归入 TooDeep。
func TestDeps_DepthLimit(t *testing.T) {
	ctx := context.Background()
	root, r := depsChain(3) // root→dep-1→dep-2→dep-3，3 层

	// 恰好够。
	if _, err := ResolveDependencies(ctx, r, root, 3); err != nil {
		t.Fatalf("maxDepth=3 should pass, got %v", err)
	}
	// 差一层。
	_, err := ResolveDependencies(ctx, r, root, 2)
	if !errors.Is(err, ErrDependencyTooDeep) {
		t.Fatalf("maxDepth=2 on 3-deep chain: want ErrDependencyTooDeep, got %v", err)
	}
	if !strings.Contains(err.Error(), "dep-3@1") {
		t.Errorf("error should name the exceeding package: %v", err)
	}

	// 非法入参。
	for _, md := range []int{0, -1} {
		_, err := ResolveDependencies(ctx, r, root, md)
		if !errors.Is(err, ErrDependencyTooDeep) {
			t.Errorf("maxDepth=%d: want ErrDependencyTooDeep, got %v", md, err)
		}
	}
}

// TestDeps_DefaultDepthBoundary 默认上限 32：32 层链可用默认值通过，
// 33 层报 TooDeep。守护常量语义不被无意改动。
func TestDeps_DefaultDepthBoundary(t *testing.T) {
	ctx := context.Background()
	root, r := depsChain(DefaultMaxDependencyDepth)
	if _, err := ResolveDependencies(ctx, r, root, DefaultMaxDependencyDepth); err != nil {
		t.Fatalf("%d-deep chain should pass with default limit, got %v", DefaultMaxDependencyDepth, err)
	}

	root33, r33 := depsChain(DefaultMaxDependencyDepth + 1)
	_, err := ResolveDependencies(ctx, r33, root33, DefaultMaxDependencyDepth)
	if !errors.Is(err, ErrDependencyTooDeep) {
		t.Fatalf("%d-deep chain: want ErrDependencyTooDeep, got %v", DefaultMaxDependencyDepth+1, err)
	}
}

// TestDeps_VersionConflict 同一 manifest 里同 pkg 列出两个不同版本：
// root 与嵌套 manifest 两条路径都要拒绝。
func TestDeps_VersionConflict(t *testing.T) {
	ctx := context.Background()

	// root 自身声明冲突。
	root := Manifest{Version: "1", Digest: "d", Dependencies: []Dependency{
		{PackageID: "p", Version: "1.0.0"},
		{PackageID: "p", Version: "2.0.0"},
	}}
	r := newDepsMapResolver()
	r.add("p", "1.0.0")
	r.add("p", "2.0.0")
	if _, err := ResolveDependencies(ctx, r, root, DefaultMaxDependencyDepth); !errors.Is(err, ErrDependencyConflict) {
		t.Fatalf("root conflict: want ErrDependencyConflict, got %v", err)
	}

	// 嵌套 manifest 声明冲突。
	nested := newDepsMapResolver()
	nested.add("b", "1", Dependency{PackageID: "p", Version: "1.0.0"}, Dependency{PackageID: "p", Version: "2.0.0"})
	nested.add("p", "1.0.0")
	nested.add("p", "2.0.0")
	root2 := Manifest{Version: "1", Digest: "d", Dependencies: []Dependency{{PackageID: "b", Version: "1"}}}
	_, err := ResolveDependencies(ctx, nested, root2, DefaultMaxDependencyDepth)
	if !errors.Is(err, ErrDependencyConflict) {
		t.Fatalf("nested conflict: want ErrDependencyConflict, got %v", err)
	}
}

// TestDeps_ResolverErrorPassthrough resolver 的非 NotFound 错误必须透传
// （调用方能 errors.Is 到原始错误），不能被吞进聚合错误；且基础设施错误
// 优先于同遍历中已收集的缺失项（不掩盖真实故障）。
func TestDeps_ResolverErrorPassthrough(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db connection reset")

	r := &depsFailingResolver{
		depsMapResolver: newDepsMapResolver(),
		failKey:         "b@1",
		err:             boom,
	}
	root := Manifest{Version: "1", Digest: "d", Dependencies: []Dependency{
		{PackageID: "ghost", Version: "1"}, // 先收集一个缺失
		{PackageID: "b", Version: "1"},     // 再触发基础设施错误
	}}

	_, err := ResolveDependencies(ctx, r, root, DefaultMaxDependencyDepth)
	if !errors.Is(err, boom) {
		t.Fatalf("want passthrough of boom, got %v", err)
	}
	if errors.Is(err, ErrDependenciesUnresolved) {
		t.Errorf("infra error must not be wrapped as unresolved: %v", err)
	}
}

// TestDeps_NilResolverAndEmptyClosure resolver 为 nil 必须报错而非 panic；
// 无依赖的 root 返回空闭包。
func TestDeps_NilResolverAndEmptyClosure(t *testing.T) {
	ctx := context.Background()
	root := Manifest{Version: "1", Digest: "d", Dependencies: []Dependency{
		{PackageID: "anything", Version: "1"},
	}}
	if _, err := ResolveDependencies(ctx, nil, root, DefaultMaxDependencyDepth); err == nil {
		t.Fatal("nil resolver must error")
	}

	empty := Manifest{Version: "1", Digest: "d"}
	got, err := ResolveDependencies(ctx, newDepsMapResolver(), empty, DefaultMaxDependencyDepth)
	if err != nil {
		t.Fatalf("empty closure: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty closure = %+v, want empty", got)
	}
}

// TestPGDeps_StoreResolverTransitive PG 集成：storeResolver 能从
// marketplace_versions 取回 manifest 并完成传递解析；缺失依赖走聚合错误。
// 无 DSN 自动 skip（newTestStore 内部处理）。
func TestPGDeps_StoreResolverTransitive(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 提交被依赖包 a（无依赖）与依赖 a 的包 b。Submit 直接写入
	// marketplace_versions，resolver 按 (package_id, version) 精确可查。
	if _, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-deps", Name: "lib-a", Kind: "skill", Version: "1.0.0",
		Digest: "sha256:a", Manifest: Manifest{Version: "1.0.0", Digest: "sha256:a"},
		Publisher: "alice",
	}); err != nil {
		t.Fatalf("submit lib-a: %v", err)
	}
	bManifest := Manifest{
		Version:      "2.0.0",
		Digest:       "sha256:b",
		Dependencies: []Dependency{{PackageID: "ws-deps/lib-a", Version: "1.0.0"}},
	}
	if _, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-deps", Name: "app-b", Kind: "skill", Version: "2.0.0",
		Digest: "sha256:b", Manifest: bManifest, Publisher: "alice",
	}); err != nil {
		t.Fatalf("submit app-b: %v", err)
	}

	resolver := NewStoreResolver(s)

	// 直接取回语义：存在 → manifest；不存在 → ErrMarketplaceNotFound。
	m, err := resolver.GetVersion(ctx, "ws-deps/lib-a", "1.0.0")
	if err != nil {
		t.Fatalf("GetVersion(lib-a@1.0.0): %v", err)
	}
	if m.Version != "1.0.0" || m.Digest != "sha256:a" {
		t.Errorf("GetVersion roundtrip = %+v", m)
	}
	if _, err := resolver.GetVersion(ctx, "ws-deps/lib-a", "9.9.9"); !errors.Is(err, ErrMarketplaceNotFound) {
		t.Errorf("GetVersion missing: want ErrMarketplaceNotFound, got %v", err)
	}

	// b 的闭包应恰为 [lib-a@1.0.0]。
	got, err := ResolveDependenciesWithStore(ctx, s, bManifest, DefaultMaxDependencyDepth)
	if err != nil {
		t.Fatalf("ResolveDependenciesWithStore: %v", err)
	}
	if len(got) != 1 || got[0] != (Dependency{PackageID: "ws-deps/lib-a", Version: "1.0.0"}) {
		t.Fatalf("closure = %+v, want [ws-deps/lib-a@1.0.0]", got)
	}

	// 缺失依赖：查不到 → 聚合错误。
	ghost := Manifest{
		Version:      "1.0.0",
		Digest:       "d",
		Dependencies: []Dependency{{PackageID: "ws-deps/ghost", Version: "9.9.9"}},
	}
	if _, err := ResolveDependencies(ctx, resolver, ghost, DefaultMaxDependencyDepth); !errors.Is(err, ErrDependenciesUnresolved) {
		t.Fatalf("missing dep via store resolver: want ErrDependenciesUnresolved, got %v", err)
	}
}
