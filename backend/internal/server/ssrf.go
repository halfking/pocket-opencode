package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxLLMGatewayResponseBytes = 1 << 20

// gatewayAllowPrivate 表示是否允许网关节点位于私网/loopback。
//
// 默认关闭，行为与 validateOutboundURL 完全一致。运维把 llm-gateway-go 部署在
// 内网时（POCKET_LLM_GATEWAY_URL 默认值就是 http://llm-gateway.internal）必须
// 显式打开这个开关，这是一次有意的安全放宽，仅对"已注册网关节点"这一条出站
// 路径生效，其它出站校验不受影响。
//
// 即使开关打开，云元数据端点依然被拦截 —— 那是纯粹的凭据泄露面，没有任何
// 合理的网关会部署在那里。
func gatewayAllowPrivate() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// isCloudMetadataHost 匹配始终禁止的元数据地址，无论 allowPrivate 如何设置。
func isCloudMetadataHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	switch host {
	case "metadata.google.internal", "metadata", "instance-data":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isCloudMetadataIP(ip)
	}
	return false
}

func isCloudMetadataIP(ip net.IP) bool {
	// 169.254.169.254 (AWS/GCP/Azure/OpenStack) 与 fd00:ec2::254 (AWS IPv6)。
	return ip.Equal(net.IPv4(169, 254, 169, 254)) ||
		ip.Equal(net.ParseIP("fd00:ec2::254"))
}

// sttAllowPrivate 读 POCKET_STT_ALLOW_PRIVATE。
//
// 为什么必须是**独立**开关，不能蹭 POCKET_LLM_GATEWAY_ALLOW_PRIVATE：
// 后者的语义是「本实例的 LLM 网关部署在内网，放行以便连上」——那是**管理员**
// 为了让实例能工作而做的部署决定。而 STT 外部服务地址是**每个用户自己填的**
// 任意 URL，转发过去的是用户的录音（会议、语音输入）。
//
// 2026-10-01 实测的缺陷：STT 用了 validateGatewayURL，于是任何为了连内网网关
// 而打开网关开关的部署，STT 的 SSRF 防护被**静默关掉** —— 用户可以把
// externalBaseURL 填成 http://127.0.0.1:<本机任意端口>/v1，后端就把录音
// POST 过去。能打到 loopback 就意味着能打到实例自己暴露的内部管理面。
// 仓库里 TestValidateOutboundURLUnaffectedByGatewaySwitch 正是为守住这条
// 不变量而写的，但它只覆盖 validateOutboundURL，漏了这条路径。
//
// 自建 ASR（局域网里的 whisper）仍然是正当需求，所以这里给一个**名字独立、
// 语义明确**的开关，而不是取消这个能力。默认值仍是严格拒绝。
func sttAllowPrivate() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POCKET_STT_ALLOW_PRIVATE"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// validateSTTOutboundURL 校验用户填写的外部语音转写服务地址。
//
// 规则与 validateOutboundURL 一致（scheme / host / userinfo+query+fragment），
// 私网与 loopback 的放行条件换成 sttAllowPrivate()：
//
//	默认            → 拒绝私网与 loopback
//	POCKET_STT_ALLOW_PRIVATE=true → 放行私网/loopback（自建 ASR 场景）
//	云元数据端点    → 无论开关与否始终拒绝（那是纯粹的凭据泄露面）
func validateSTTOutboundURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("URL host is required")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("URL must not contain userinfo, query, or fragment")
	}
	if isCloudMetadataHost(u.Hostname()) {
		return fmt.Errorf("URL host is not allowed")
	}
	if !sttAllowPrivate() && isBlockedOutboundHost(u.Hostname()) {
		return fmt.Errorf("URL host is not allowed (set POCKET_STT_ALLOW_PRIVATE=true to use a self-hosted ASR service on a private address)")
	}
	return nil
}

// validateOutboundURL 校验网关节点的 base URL。与 validateOutboundURL 的区别只有
// 一点：当 POCKET_LLM_GATEWAY_ALLOW_PRIVATE 打开时，私网/loopback 目标被放行。
//
// 这里不接受 query/fragment/userinfo —— 节点 base URL 只应该是 scheme://host[:port][/path]，
// 携带 query 往往意味着调用方在拼接上游路径时出了错。
func validateGatewayURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("URL host is required")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("URL must not contain userinfo, query, or fragment")
	}
	if isCloudMetadataHost(u.Hostname()) {
		return fmt.Errorf("URL host is not allowed")
	}
	if !gatewayAllowPrivate() && isBlockedOutboundHost(u.Hostname()) {
		return fmt.Errorf("URL host is not allowed (set POCKET_LLM_GATEWAY_ALLOW_PRIVATE=true to reach a private-network gateway)")
	}
	return nil
}

// gatewayHTTPClient 返回访问网关节点用的 client。
//
// timeout 由调用方决定：普通 admin API 用 15s，SSE 长连接必须传 0（否则
// http.Client.Timeout 会在中途掐断整个流，而不只是握手阶段）。
func gatewayHTTPClient(timeout time.Duration) *http.Client {
	allowPrivate := gatewayAllowPrivate()
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			// DNS 重绑定防护：解析出的每一个 IP 都要过校验，然后直接拨已校验的
			// IP，避免"校验时解析到公网、拨号时解析到内网"的时间差攻击。
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				ips, err := net.LookupIP(host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("host has no addresses")
				}
				for _, ip := range ips {
					if isCloudMetadataIP(ip) {
						return nil, fmt.Errorf("resolved address is not allowed")
					}
					if !allowPrivate && isBlockedOutboundIP(ip) {
						return nil, fmt.Errorf("resolved address is not allowed")
					}
				}
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
			},
			// SSE 需要即时看到每个 chunk，禁用响应缓冲压缩带来的延迟。
			DisableCompression:  true,
			MaxIdleConnsPerHost: 4,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validateOutboundURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("URL host is required")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("URL must not contain userinfo, query, or fragment")
	}
	if isBlockedOutboundHost(u.Hostname()) {
		return fmt.Errorf("URL host is not allowed")
	}
	return nil
}

func isBlockedOutboundHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "localhost" || host == "localhost.localdomain" || host == "metadata.google.internal" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isBlockedOutboundIP(ip)
	}
	return false
}

func isBlockedOutboundIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func safeOutboundHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				ips, err := net.LookupIP(host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if isBlockedOutboundIP(ip) {
						return nil, fmt.Errorf("resolved address is not allowed")
					}
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("host has no addresses")
				}
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func outboundModelsURL(baseURL string) (string, error) {
	if err := validateOutboundURL(baseURL); err != nil {
		return "", err
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/models"
	return u.String(), nil
}
