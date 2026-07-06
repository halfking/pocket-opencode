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
)

// DefaultPorts is the ordered list of ports to scan for OpenCode instances.
var DefaultPorts = []int{14096, 14097, 14098, 14099, 14100}

// NetworkDiscovery returns a DiscoveryFunc that scans localhost and the host's
// LAN subnet for OpenCode instances. The scan is fast (< 2s per run with a
// 500ms timeout per probe) and safe for production use.
func NetworkDiscovery() DiscoveryFunc {
	return func(ctx context.Context) ([]InstanceConfig, error) {
		candidates := buildCandidates()
		return probeCandidates(ctx, candidates)
	}
}

// buildCandidates collects all (ip, port) pairs to probe.
func buildCandidates() []hostPort {
	var result []hostPort
	// Always scan localhost / 127.0.0.1
	hosts := []string{"127.0.0.1", "localhost"}

	// Scan LAN interfaces for additional hosts (only the first /24 of each IPv4)
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
				// Probe the host itself plus the gateway (.1)
				hostIP := ipNet.IP.String()
				hosts = appendUnique(hosts, hostIP)

				// Also probe the .1 gateway
				base := ipNet.IP.Mask(ipNet.Mask)
				gateway := make(net.IP, len(base))
				copy(gateway, base)
				gateway[3] = 1
				hosts = appendUnique(hosts, gateway.String())
			}
		}
	}

	for _, host := range hosts {
		for _, port := range DefaultPorts {
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

// probeOne checks whether host:port is an OpenCode instance by calling
// GET /api/health and parsing the response. Returns the instance config on
// success.
func probeOne(ctx context.Context, client *http.Client, host string, port int) (InstanceConfig, bool) {
	url := fmt.Sprintf("http://%s:%d/api/health", host, port)
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

	// 验证响应格式：{ "healthy": true } 或 { "status": "ok" }
	var result struct {
		Healthy bool   `json:"healthy"`
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return InstanceConfig{}, false
	}

	if !result.Healthy && result.Status != "ok" {
		return InstanceConfig{}, false
	}

	// Build a stable ID from host:port
	displayHost := host
	if displayHost == "127.0.0.1" {
		displayHost = "local"
	}
	id := fmt.Sprintf("discovered-%s-%d", displayHost, port)
	displayName := fmt.Sprintf("OpenCode (%s:%d)", host, port)
	if result.Version != "" {
		displayName = fmt.Sprintf("OpenCode %s (%s:%d)", result.Version, host, port)
	}

	return InstanceConfig{
		ID:          id,
		DisplayName: displayName,
		APIBaseURL:  fmt.Sprintf("http://%s:%d", host, port),
		Environment: "discovered",
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
