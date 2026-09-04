package marketplace

// deps.go — marketplace 依赖解析：精确 pinned 版本的传递闭包。
//
// 职责（ADR: docs/handoff/2026-09-05-marketplace-signing-chain-design.md §6）：
//   - 拓扑序输出（被依赖者在前，即 install 顺序）；
//   - 环检测（含自依赖），报错带完整环路径；
//   - 深度上限（默认 DefaultMaxDependencyDepth）；
//   - 缺失依赖聚合：遍历完整个可达图后一次性列出全部缺失项；
//   - 同一 manifest 内同 package 多版本声明视为 conflict。
//
// 本文件只做纯解析，不依赖 signing/blob 链路的任何符号。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// DefaultMaxDependencyDepth 是依赖链层数的默认上限（root 为第 0 层）。
// Publish 强制点调用 ResolveDependencies 时传该值：精确 pinned + 节点去重
// 已经抑制了扇出爆炸，剩下的风险是恶意构造的超长链式依赖，用深度上限兜底。
const DefaultMaxDependencyDepth = 32

// 依赖解析的 sentinel 错误。调用方用 errors.Is 判别类别，
// 具体上下文（环路径、超限包名、缺失清单）在包装错误的文本里。
var (
	// ErrDependencyCycle 依赖图中存在环（含自依赖）。
	ErrDependencyCycle = errors.New("marketplace: dependency cycle")

	// ErrDependencyTooDeep 依赖链层数超过 maxDepth。
	// maxDepth<=0 的非法入参也归入此类：对调用方来说同样是"拒绝解析"，
	// 且深度 0 连一层依赖都不允许，走 TooDeep 的失败语义最直观。
	ErrDependencyTooDeep = errors.New("marketplace: dependency too deep")

	// ErrDependenciesUnresolved 传递闭包中存在 marketplace 里找不到的依赖。
	// 聚合语义：不遇错即停，而是遍历完可达图后一次性列出全部缺失项
	// （pkg@ver），让提交方一轮修完所有问题。
	ErrDependenciesUnresolved = errors.New("marketplace: dependencies unresolved")

	// ErrDependencyConflict 同一 manifest 的 Dependencies 里同一 package
	// 被列出多个不同版本——pinned 语义下无法裁决该信谁，直接拒绝。
	ErrDependencyConflict = errors.New("marketplace: dependency conflict")
)

// DependencyResolver 按 (package_id, version) 精确取回一个版本的 manifest。
// 缺失时返回 ErrMarketplaceNotFound；其他非 nil 错误视为基础设施故障，
// 由 ResolveDependencies 原样透传（不参与缺失聚合）。
type DependencyResolver interface {
	GetVersion(ctx context.Context, packageID, version string) (*Manifest, error)
}

// ResolveDependencies 解析 root manifest 声明依赖的完整传递闭包。
//
// 语义：
//   - 返回拓扑序切片：被依赖者在前，可直接按顺序 install；root 自身不在结果里；
//   - 精确 (package_id, version) 匹配，不做任何区间/最新版推断——发布闭包
//     必须可复现，解析结果漂移等于发布后偷换内容；
//   - 已解析节点全局去重（diamond 场景共享依赖只解析一次、只出现一次）。
//
// maxDepth 限制依赖链层数（root 直接依赖为第 1 层）；resolver 为 nil 或
// maxDepth<=0 属于调用方编程错误，直接报错而不是 panic。
func ResolveDependencies(ctx context.Context, resolver DependencyResolver, root Manifest, maxDepth int) ([]Dependency, error) {
	if resolver == nil {
		return nil, errors.New("marketplace: dependency resolver must not be nil")
	}
	if maxDepth <= 0 {
		return nil, fmt.Errorf("%w: invalid maxDepth %d (must be >= 1)", ErrDependencyTooDeep, maxDepth)
	}

	w := &depWalk{
		resolver:   resolver,
		maxDepth:   maxDepth,
		visited:    make(map[string]bool),
		onStack:    make(map[string]bool),
		missingSet: make(map[string]bool),
	}
	if err := w.walk(ctx, root, "root", 0); err != nil {
		return nil, err
	}
	if len(w.missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrDependenciesUnresolved, strings.Join(w.missing, ", "))
	}
	if w.resolved == nil {
		// 空闭包也返回非 nil 切片，调用方 range 语义一致。
		w.resolved = []Dependency{}
	}
	return w.resolved, nil
}

// depWalk 是单次 ResolveDependencies 的遍历状态。
// 每次解析新建一个实例，不跨调用复用，避免残留状态污染下一次结果。
type depWalk struct {
	resolver DependencyResolver
	maxDepth int

	visited    map[string]bool // 已完整解析（后序完成）的 "pkg@ver"
	onStack    map[string]bool // 当前 DFS 路径上的 "pkg@ver"，环检测用
	path       []string        // 与 onStack 同步的有序路径，环报错时还原成 a@1 -> b@2 -> a@1
	resolved   []Dependency    // 后序 append：被依赖者自然排在依赖者之前
	missing    []string        // 聚合的缺失项（去重、保遍历序）
	missingSet map[string]bool
}

// walk 遍历 manifest 声明的全部依赖。depth 是当前 manifest 所在链层
// （root 为 0），其依赖位于 depth+1。id 仅用于错误信息定位（root 或 pkg@ver）。
func (w *depWalk) walk(ctx context.Context, m Manifest, id string, depth int) error {
	// 先做本 manifest 内的声明校验，再递归：同 pkg 不同版本的声明必须在
	// 进入任何分支前拒绝，否则两条分支可能各自解析成功、问题被推迟到更深处。
	seen := make(map[string]string, len(m.Dependencies))
	for _, dep := range m.Dependencies {
		if prev, ok := seen[dep.PackageID]; ok {
			if prev != dep.Version {
				return fmt.Errorf("%w: %s declares %s at both %s and %s",
					ErrDependencyConflict, id, dep.PackageID, prev, dep.Version)
			}
			continue // 完全相同的重复声明：幂等去重
		}
		seen[dep.PackageID] = dep.Version

		key := dep.PackageID + "@" + dep.Version
		if w.onStack[key] {
			// 环：当前 DFS 路径 + 回边目标完整打印，定位是哪条链成环。
			cycle := append([]string(nil), w.path...)
			cycle = append(cycle, key)
			return fmt.Errorf("%w: %s", ErrDependencyCycle, strings.Join(cycle, " -> "))
		}
		if w.visited[key] {
			continue // diamond 等共享依赖：闭包里只出现一次
		}
		if depth+1 > w.maxDepth {
			return fmt.Errorf("%w: %s at depth %d exceeds limit %d",
				ErrDependencyTooDeep, key, depth+1, w.maxDepth)
		}

		depManifest, err := w.resolver.GetVersion(ctx, dep.PackageID, dep.Version)
		if err != nil {
			if errors.Is(err, ErrMarketplaceNotFound) {
				// 缺失不立即失败：继续遍历兄弟分支，最后聚合报告全部缺失项。
				if !w.missingSet[key] {
					w.missingSet[key] = true
					w.missing = append(w.missing, key)
				}
				continue
			}
			// 其他错误是 resolver 基础设施故障（DB 不可用等）：透传给调用方，
			// 不混进"依赖缺失"的聚合错误里掩盖真实原因。
			return fmt.Errorf("marketplace: resolve %s: %w", key, err)
		}

		w.onStack[key] = true
		w.path = append(w.path, key)
		err = w.walk(ctx, *depManifest, key, depth+1)
		w.path = w.path[:len(w.path)-1]
		delete(w.onStack, key)
		if err != nil {
			return err
		}
		w.visited[key] = true
		w.resolved = append(w.resolved, Dependency{PackageID: dep.PackageID, Version: dep.Version})
	}
	return nil
}

// storeResolver 把 *Store 适配成 DependencyResolver。
type storeResolver struct {
	store *Store
}

// NewStoreResolver 返回基于 PG store 的 DependencyResolver。
// Publish 的依赖可解析强制点用它查询 marketplace_versions。
func NewStoreResolver(store *Store) DependencyResolver {
	return &storeResolver{store: store}
}

// GetVersion 按 (package_id, version) 精确查版本 manifest。
// LIMIT 1 + ORDER BY submitted_at DESC 是防御：正常 (package_id, version)
// 唯一，但万一历史数据出现重复行，取最新提交的一条而不是让解析报错。
func (r *storeResolver) GetVersion(ctx context.Context, packageID, version string) (*Manifest, error) {
	if r.store == nil || r.store.pool == nil {
		return nil, fmt.Errorf("marketplace: store resolver: pool not configured")
	}
	var manifestJSON []byte
	err := r.store.pool.QueryRow(ctx, `
		SELECT manifest FROM marketplace_versions
		WHERE package_id = $1 AND version = $2
		ORDER BY submitted_at DESC
		LIMIT 1
	`, packageID, version).Scan(&manifestJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMarketplaceNotFound
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("marketplace: store resolver: unmarshal manifest for %s@%s: %w", packageID, version, err)
	}
	return &m, nil
}

// ResolveDependenciesWithStore 是 ResolveDependencies 的 store 便捷入口：
// 直接用 PG store 做 resolver（Publish 强制点的调用形态）。
func ResolveDependenciesWithStore(ctx context.Context, store *Store, root Manifest, maxDepth int) ([]Dependency, error) {
	return ResolveDependencies(ctx, NewStoreResolver(store), root, maxDepth)
}
