package rss

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultMaxBodyBytes int64 = 8 << 20

type HTTPOptions struct {
	Timeout      time.Duration
	MaxBodyBytes int64
	DialTimeout  time.Duration
}

func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	v4 := ip.To4()
	if v4 != nil {
		n := uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
		// CGNAT, benchmarking, documentation, IETF protocol and reserved ranges.
		return (n >= 0x64400000 && n <= 0x647fffff) || (n >= 0xc0000000 && n <= 0xc00000ff) || (n >= 0xc0000200 && n <= 0xc00002ff) || (n >= 0xc6120000 && n <= 0xc613ffff) || (n >= 0xcb007100 && n <= 0xcb0071ff) || n >= 0xf0000000
	}
	return false
}
func blockedHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	switch h {
	case "localhost", "localhost.localdomain", "metadata", "metadata.google.internal", "instance-data":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return blockedIP(ip)
	}
	return false
}

// ValidateURL checks the URL before any network operation. Hostnames are also
// checked again after DNS resolution by NewSafeHTTPClient to prevent rebinding.
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("rss: invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("rss: URL scheme must be http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("rss: URL host is required")
	}
	if u.User != nil {
		return fmt.Errorf("rss: URL userinfo is not allowed")
	}
	if len(u.String()) > MaxURLLength {
		return fmt.Errorf("rss: URL is too long")
	}
	if blockedHost(u.Hostname()) {
		return fmt.Errorf("rss: URL host is not allowed")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("rss: invalid port")
		}
	}
	return nil
}
func IsBlockedIP(ip net.IP) bool { return blockedIP(ip) }

func NewSafeHTTPClient(opts HTTPOptions) *http.Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	tr := &http.Transport{Proxy: nil, DisableCompression: false, MaxIdleConnsPerHost: 4}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if blockedHost(host) {
			return nil, fmt.Errorf("rss: destination host is not allowed")
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("rss: host has no addresses")
		}
		for _, ip := range ips {
			if blockedIP(ip) {
				return nil, fmt.Errorf("rss: resolved address is not allowed")
			}
		}
		return (&net.Dialer{Timeout: opts.DialTimeout}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	client := &http.Client{Transport: tr, Timeout: opts.Timeout}
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if err := ValidateURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	}
	return client
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("rss: response exceeds %d bytes", max)
	}
	return b, nil
}
