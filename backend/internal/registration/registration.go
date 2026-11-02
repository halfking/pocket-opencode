// Package registration 把这个进程注册进 goacos 的实例注册面。
//
// 它为什么存在
// ------------
// goacos 的 SDK（sdk/registry）把跨仓共享的注册策略收在一处：什么时候注册、
// 发布哪个端口、发布哪个地址、什么时候注销、注册失败要不要挡住服务。
// 这五个决定每一个都有一种「看起来成功、实际是错」的失败方式。
//
// 本包是**它与 openpocket 自己的部署口径之间**的那一层：SDK 说的是通用规则，
// 本包说的是「openpocket 在 252 / 154 / 245 上到底长什么样」。
//
// 三个必须记住的口径
// ------------------
//
//  1. **对外端口不是进程监听端口，而且进程自己读不到它。**
//
//     仓内编排（deploy/bin/docker-compose.opp.yml）的写法是：
//
//     environment:
//     # 后端把 POCKET_HTTP_PORT 当监听端口读；compose environment 优先级
//     # 高于 env_file，固定容器内监听 8088，宿主端口只由 ports 映射决定。
//     POCKET_HTTP_PORT: "8088"
//     ports:
//     - "${POCKET_PORT_BIND_IP:-0.0.0.0}:${POCKET_HTTP_PORT:-8090}:8088"
//
//     即：**容器内固定听 8088**，而**宿主发布出去的端口**由宿主侧的
//     POCKET_HTTP_PORT 决定（252/154 = 8090，245 = 8091）。
//     compose 的 environment 优先级高于 env_file ⇒ 容器里那个
//     POCKET_HTTP_PORT 已经被钉成 8088（内部端口）⇒ **进程读自己的
//     POCKET_HTTP_PORT 读不到对外端口**。
//
//     ⇒ 对外端口只认显式的 GOACOS_PORT。部署面用
//     `GOACOS_PORT: ${POCKET_HTTP_PORT:-8090}` 把它注进来 —— 那样每个主机
//     各自发布多少就是多少，代码里不用写死任何一个端口。
//     没有 GOACOS_PORT 时退回监听端口，但那是**兜底**：注册面里看不出
//     「这个端口跨机拨不通」，实例会显示 healthy，请求过去却是 502。
//
//  2. **地址必须显式给。**
//
//     容器里自动探测得到的是容器网段地址（172.18.x.x 之类），同机拨得通、
//     **跨机拨不通**。别的机器拨的是**宿主**在 netbird 虚拟局域网里的地址
//     （活体注册面上 openpocket 的实例行就是这个形态）⇒ 自动探测出来的
//     地址一律不用。GOACOS_IP 没给就不注册 —— 发布一个拨不通的实例，
//     比不发布更糟。
//
//  3. **名字跟应用模型走。**
//
//     本包注册成 openpocket-api，与 myvpn/deploy/goacos/applications.itestu.json
//     里 openpocket 应用的 api 组件同名。活体注册面上还有老台账那套名字
//     （openpocket / pocket-api，同占 8090），那条冲突已登记在决策表 G1。
//     本包不去制造第四个名字。
//
// 环境变量
// --------
//
//	GOACOS_ENABLED     总开关。**默认关**（SDK 自身也是默认关；「默认改开」
//	                    是 D5，未获 owner 确认 ⇒ 仓内取保守值）。
//	GOACOS_SERVER_ADDR 注册中心地址，如 http://100.106.190.139:8848。
//	GOACOS_IP          对外可拨的地址（宿主的 netbird 虚拟局域网地址）。
//	GOACOS_PORT        对外端口（**不是**容器内的 8088）。
//	GOACOS_NAMESPACE   命名空间，默认 public（与 reconcile 侧同口径）。
//	GOACOS_CLUSTER     集群名。goacos 的实例唯一键里有它，
//	                    多主机部署时它决定这些实例能不能被区分开。
//	GOACOS_GROUP       分组名。
//	GOACOS_METADATA    逗号分隔的 k=v。
package registration

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/halfking/goacos/sdk/registry"
)

// ServiceName 是本服务在 goacos 里的名字。
//
// 与应用模型（openpocket/api 组件）与声明面（opencode-pocket-pocketd 容器）
// 一致。详见包注释第 3 条。
const ServiceName = "openpocket-api"

// Options 是注册时用得上的几个事实。
type Options struct {
	// ListenAddr 是进程实际监听的地址，例如 ":8088"。
	// 它只用来兜底推导端口（GOACOS_PORT 没给时），以及写进日志。
	ListenAddr string

	// Logger 是日志出口。nil 时用 slog.Default()。
	Logger *slog.Logger
}

// resolved 是一次注册解析出来的全部结论。
type resolved struct {
	// cfg 是要交给 SDK 的配置。
	cfg registry.Config
	// portSource 说明对外端口是从哪儿来的 —— 写进日志，
	// 因为「发布了哪个端口」和「为什么是那个端口」是两件事。
	portSource string
	// ipExplicit 记录地址是操作员写的还是探测出来的。
	ipExplicit bool
}

// 端口来源的取值。写进日志，便于事后回答「这个实例的端口是谁定的」。
const (
	portSourceEnv    = "GOACOS_PORT"
	portSourceListen = "listen_addr"
)

// Announce 把本进程注册进 goacos，返回注销用的函数。
//
// 返回的函数永远非 nil，可以直接 defer，也可以反复调用。它在注册被跳过时
// 也是一个空操作 —— 所以调用方不需要写任何分支。
//
// 注销要在**停服务之前**调用：注册面还留着这个实例的那几秒里，网关会继续
// 往一个已经不再接请求的进程上打。
func Announce(ctx context.Context, opt Options) func() {
	logger := opt.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r, err := resolve(os.Getenv, opt)
	if err != nil {
		// 只有操作员打错才会走到这里。注册失败不挡服务。
		logger.Warn("goacos 注册参数无效，继续对外服务", slog.Any("err", err))
		return func() {}
	}

	if !r.cfg.Enabled {
		// Debug 而不是 Warn：关是文档化的默认值，每次启动都警告，
		// 警告就没人看了。SDK 自己也是这么处理的。
		logger.Debug("goacos 注册未启用", slog.String("env", "GOACOS_ENABLED 未开启"))
		return func() {}
	}

	if r.cfg.IP == "" {
		// 交给 SDK 的既有失败路径（它会打 "no address peers can dial" 并跳过注册），
		// 这里只补一句本服务自己的理由：为什么不采用自动探测。
		logger.Warn("goacos 注册未执行：本服务不采用自动探测出来的地址",
			slog.String("service", r.cfg.ServiceName),
			slog.String("hint", "显式设置 GOACOS_IP（宿主对外可拨的 netbird 地址）"),
			slog.String("为什么", "容器里自动探测得到的是容器网段地址，跨机拨不通；"+
				"发布一个拨不通的实例比不发布更糟 —— 网关会挑中它然后 502"))
		return func() {}
	}

	if r.portSource == portSourceListen {
		// 兜底到监听端口时必须说清楚：这个端口**多半**是错的
		//（容器内 8088 ≠ 宿主发布的 8090/8091）。
		logger.Warn("goacos 注册用的端口是进程监听端口，很可能不是对外端口",
			slog.String("service", r.cfg.ServiceName),
			slog.Int("port", r.cfg.Port),
			slog.String("hint", "显式设置 GOACOS_PORT（宿主发布端口）"),
			slog.String("为什么", "容器内监听端口与宿主发布端口不是同一个；"+
				"发布错端口 ⇒ 实例在注册面里显示 healthy，跨机却拨不通"))
	}

	logger.Info("goacos 注册参数已解析",
		slog.String("service", r.cfg.ServiceName),
		slog.String("ip", r.cfg.IP),
		slog.Int("port", r.cfg.Port),
		slog.String("port_source", r.portSource),
		slog.String("namespace", r.cfg.NamespaceID),
		slog.String("cluster", r.cfg.ClusterName),
		slog.String("group", r.cfg.GroupName),
		slog.Bool("ip_explicit", r.ipExplicit))

	return registry.Announce(ctx, r.cfg, opt.ListenAddr, logger)
}

// resolve 把环境变量解析成一次注册的全部结论。
//
// 纯函数：不碰网络、不读全局变量 ⇒ 可以直接对着假环境测。
func resolve(getenv func(string) string, opt Options) (resolved, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	cfg, err := registry.ConfigFromEnv(getenv, registry.Service{
		Name:       ServiceName,
		ListenAddr: opt.ListenAddr,
	})
	if err != nil {
		return resolved{}, err
	}

	out := resolved{cfg: cfg}

	// ── 命名空间：默认 public ────────────────────────────────────────────
	//
	// 与 reconcile 侧（myvpn/deploy/goacos/goacos-reconcile.sh 的 GOACOS_NS
	// 默认值）同口径。goacos 1.1.1 以前，缺省 namespaceId 会落成空串而不是
	// "public"（见 goacos internal/api/naming_v1.go 的 normalizeNamespace 注释），
	// 于是同一个服务在两套注册面里被拆成两个 namespace。
	if strings.TrimSpace(cfg.NamespaceID) == "" {
		cfg.NamespaceID = registry.DefaultNamespaceID
		if cfg.NamespaceID == "" {
			cfg.NamespaceID = "public"
		}
	}

	// ── 地址：只认显式给的那一个 ─────────────────────────────────────────
	//
	// SDK 的 ConfigFromEnv 在操作员没给 GOACOS_IP 时会自动探测一个地址。
	// 对 openpocket 来说那个地址一定是错的（容器网段地址，见包注释第 2 条），
	// 所以这里把它清掉：宁可不注册，也不发布一个拨不通的实例。
	out.ipExplicit = strings.TrimSpace(getenv(registry.EnvIP)) != ""
	if !out.ipExplicit {
		cfg.IP = ""
	}

	// ── 对外端口：只认 GOACOS_PORT ───────────────────────────────────────
	//
	// ⚠ **不能**用 POCKET_HTTP_PORT：compose 把容器内那个变量钉成了 8088
	//   （内部监听端口），而宿主发布出去的是 8090 / 8091 ⇒ 拿它发布出来的
	//   端口跨机拨不通。详见包注释第 1 条。
	//
	// 没给 GOACOS_PORT 时退回监听端口，但那是兜底（调用方会告警）。
	switch {
	case strings.TrimSpace(getenv(registry.EnvPort)) != "":
		out.portSource = portSourceEnv
	default:
		if port := portOf(opt.ListenAddr); port > 0 {
			cfg.Port = port
		}
		out.portSource = portSourceListen
	}

	out.cfg = cfg
	return out, nil
}

// portOf 从监听地址里取出端口。
//
// 只认「host:port」和裸端口两种写法；其余（空、带 scheme 的 URL）一律 0，
// 让上层按「端口定不下来」处理 —— 而不是猜一个。
func portOf(listenAddr string) int {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		return 0
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// 没有冒号的裸端口（"8088"）也认。
		portStr = addr
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}
