// Package registry — network-based OpenCode instance discovery.
//
// Scans localhost and the host's LAN subnet for OpenCode instances on known
// ports (14096-14099). For each candidate IP:port, a GET /api/health probe
// verifies whether it is a running OpenCode instance. Discovered instances
// are returned as InstanceConfig so the Registry can merge them.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// DefaultPorts is the ordered list of ports to scan for OpenCode instances.
// OpenCode 真实默认端口是 4096（port=0 时的优先值，见 opencode server.ts:117）。
// 14096-14100 是历史误用值（Pocket 早期假设），保留作向后兼容。
// 3000/8080 是 opencode serve / opencode web 可能的显式配置端口。
var DefaultPorts = []int{4096, 14096, 14097, 14098, 14099, 14100, 3000, 8080}

// DefaultAPIPort is the canonical OpenCode HTTP API port (4096). Used by
// handler callers (e.g. dynamic OpenCode instance bootstrap) when no port is
// provided by the user.
const DefaultAPIPort = 4096

// ZCodePorts 是 ZCode 桌面版/守护进程常见的监听端口（与 OpenCode 共存）。
var ZCodePorts = []int{4096, 3000, 8080, 9000}

// discoveryOptions 控制扫描行为。零值即退化到原有的"本机+网关"行为，
// 保证向后兼容；只有显式开启 fullSubnet 才会扫完整 /24（开销较大）。
type discoveryOptions struct {
	fullSubnet bool  // 是否扫描每个接口的完整 /24
	ports      []int // 待探测端口列表（空=DefaultPorts）
	extraHosts []string
}

// DiscoveryOption 配置 NetworkDiscovery 行为。
type DiscoveryOption func(*discoveryOptions)

// WithFullSubnetScan 开启完整 /24 子网扫描（默认仅扫本机+网关）。
func WithFullSubnetScan(enable bool) DiscoveryOption {
	return func(o *discoveryOptions) { o.fullSubnet = enable }
}

// WithPorts 覆盖默认端口列表。
func WithPorts(ports []int) DiscoveryOption {
	return func(o *discoveryOptions) {
		if len(ports) > 0 {
			o.ports = ports
		}
	}
}

// WithExtraHosts 追加额外主机（例如 ACC/NPS 返回的内网穿透目标）。
func WithExtraHosts(hosts []string) DiscoveryOption {
	return func(o *discoveryOptions) { o.extraHosts = hosts }
}

// NetworkDiscovery returns a DiscoveryFunc that scans localhost and the host's
// LAN subnet for OpenCode instances. The scan is fast (< 2s per run with a
// 500ms timeout per probe) and safe for production use.
//
// 默认仅扫描本机 + 网关，向后兼容；通过 WithFullSubnetScan(true) 可启用完整 /24 扫描。
func NetworkDiscovery(opts ...DiscoveryOption) DiscoveryFunc {
	o := discoveryOptions{ports: DefaultPorts}
	for _, opt := range opts {
		opt(&o)
	}
	return func(ctx context.Context) ([]InstanceConfig, error) {
		candidates := buildCandidates(o)
		return probeCandidates(ctx, candidates)
	}
}

// buildCandidates collects all (ip, port) pairs to probe.
// 默认行为（向后兼容）：本机 + 各接口网关。
// 当 opts.fullSubnet=true：扫描每个 Up 接口的完整 /24（1-254）。
// opts.ports 为空时使用 DefaultPorts。
func buildCandidates(opts discoveryOptions) []hostPort {
	var result []hostPort
	// Always scan localhost / 127.0.0.1
	hosts := []string{"127.0.0.1", "localhost"}

	// 追加额外配置主机（ACC/NPS 注入的内网穿透目标）
	for _, h := range opts.extraHosts {
		hosts = appendUnique(hosts, h)
	}

	ports := opts.ports
	if len(ports) == 0 {
		ports = DefaultPorts
	}

	// Scan LAN interfaces for additional hosts
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok || ipNet.IP.To4() == nil {
					continue
				}
				hostIP := ipNet.IP.String()
				hosts = appendUnique(hosts, hostIP)

				base := ipNet.IP.Mask(ipNet.Mask)

				if opts.fullSubnet {
					// 完整 /24：扫描 .1 - .254
					for i := 1; i <= 254; i++ {
						ip := make(net.IP, len(base))
						copy(ip, base)
						ip[3] = byte(i)
						hosts = appendUnique(hosts, ip.String())
					}
				} else {
					// 默认：仅网关 .1（向后兼容）
					gateway := make(net.IP, len(base))
					copy(gateway, base)
					gateway[3] = 1
					hosts = appendUnique(hosts, gateway.String())
				}
			}
		}
	}

	for _, host := range hosts {
		for _, port := range ports {
			result = append(result, hostPort{Host: host, Port: port})
		}
	}
	return result
}

type hostPort struct {
	Host string
	Port int
}

// probeCandidates concurrently probes all candidates and returns configs for
// those that respond as OpenCode instances.
func probeCandidates(ctx context.Context, candidates []hostPort) ([]InstanceConfig, error) {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var (
		mu      sync.Mutex
		configs []InstanceConfig
		wg      sync.WaitGroup
		sem     = make(chan struct{}, 50) // limit concurrency
	)

	for _, c := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(hp hostPort) {
			defer wg.Done()
			defer func() { <-sem }()

			cfg, ok := probeOne(ctx, client, hp.Host, hp.Port)
			if ok {
				mu.Lock()
				configs = append(configs, cfg)
				mu.Unlock()
				log.Printf("[discovery] found OpenCode instance: %s:%d (%s)", hp.Host, hp.Port, cfg.ID)
			}
		}(c)
	}
	wg.Wait()

	return configs, nil
}

// healthProbePaths is the ordered list of endpoints probeOne will try when
// detecting OpenCode instances. The first non-error response is used. The
// canonical OpenCode endpoint is /global/health, but legacy /api/health and
// simple /healthz are kept for older forks / proxies.
var healthProbePaths = []string{"/global/health", "/api/health", "/healthz"}

// probeOne checks whether host:port is an OpenCode instance by calling the
// health endpoint (with fallbacks) and parsing the response. Returns the
// instance config on success. 探测时尽量从 health 响应中提取 version/capabilities/machine 等自描述字段，
// 缺失字段保持空值（由 Registry 兜底）。
func probeOne(ctx context.Context, client *http.Client, host string, port int) (InstanceConfig, bool) {
	for _, path := range healthProbePaths {
		cfg, ok := probeHealth(ctx, client, host, port, path)
		if ok {
			return cfg, true
		}
	}
	return InstanceConfig{}, false
}

func probeHealth(ctx context.Context, client *http.Client, host string, port int, path string) (InstanceConfig, bool) {
	url := fmt.Sprintf("http://%s:%d%s", host, port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return InstanceConfig{}, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return InstanceConfig{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return InstanceConfig{}, false
	}
	var result struct {
		Healthy      bool              `json:"healthy"`
		Status       string            `json:"status"`
		Version      string            `json:"version"`
		Product      string            `json:"product"`      // opencode / zcode
		Capabilities []string          `json:"capabilities"` // 真实能力
		Machine      model.MachineInfo `json:"machine"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return InstanceConfig{}, false
	}
	if !result.Healthy && result.Status != "ok" {
		return InstanceConfig{}, false
	}

	displayHost := host
	if displayHost == "127.0.0.1" {
		displayHost = "local"
	}
	id := fmt.Sprintf("discovered-%s-%d", displayHost, port)

	productLabel := "OpenCode"
	if result.Product != "" {
		productLabel = result.Product
	}
	displayName := fmt.Sprintf("%s (%s:%d)", productLabel, host, port)
	if result.Version != "" {
		displayName = fmt.Sprintf("%s %s (%s:%d)", productLabel, result.Version, host, port)
	}
	return InstanceConfig{
		ID:           id,
		DisplayName:  displayName,
		APIBaseURL:   fmt.Sprintf("http://%s:%d", host, port),
		Environment:  "discovered",
		Hostname:     host,
		IP:           host,
		Port:         port,
		Version:      result.Version,
		Machine:      result.Machine,
		Origin:       "discovered",
		Capabilities: result.Capabilities,
	}, true
}

func appendUnique(slice []string, val string) []string {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
