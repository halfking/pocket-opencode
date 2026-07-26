package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxLLMGatewayResponseBytes = 1 << 20

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
