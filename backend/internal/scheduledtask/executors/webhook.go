package executors

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// WebhookHTTPClient is injectable for tests. Production uses a client with a
// redirect policy that fails closed.
type WebhookHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type HTTPWebhookExecutor struct {
	client  WebhookHTTPClient
	timeout time.Duration
}

func NewHTTPWebhookExecutor(client WebhookHTTPClient, timeout time.Duration) *HTTPWebhookExecutor {
	if client == nil {
		client = &http.Client{Timeout: timeout, CheckRedirect: noRedirect}
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPWebhookExecutor{client: client, timeout: timeout}
}

func (e *HTTPWebhookExecutor) Kind() scheduledtask.Kind { return scheduledtask.KindWebhook }

type webhookPayload struct {
	URL        string            `json:"url"`
	Method     string            `json:"method,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	HMACSecret string            `json:"hmacSecret,omitempty"`
}

func (e *HTTPWebhookExecutor) Execute(ctx context.Context, t *scheduledtask.Task) (*scheduledtask.Result, error) {
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("webhook HTTP client is not configured")
	}
	var p webhookPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode webhook payload: %w", err)
	}
	u, err := validateWebhookURL(p.URL)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = http.MethodPost
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("webhook method %q is not allowed", method)
	}
	body := p.Body
	if body == nil {
		body = json.RawMessage(`{}`)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create webhook request: %w", err)
	}
	for k, v := range p.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.HMACSecret != "" {
		sig := hmac.New(sha256.New, []byte(p.HMACSecret))
		_, _ = sig.Write(body)
		req.Header.Set("X-Pocket-Signature", hex.EncodeToString(sig.Sum(nil)))
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()
	const maxResponse = 1 << 20
	response, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if readErr != nil {
		return nil, fmt.Errorf("read webhook response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(response)))
	}
	output, _ := json.Marshal(map[string]interface{}{
		"status": resp.StatusCode,
		"body":   json.RawMessage(normalizeJSON(response)),
	})
	return &scheduledtask.Result{Output: output}, nil
}

func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func validateWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("webhook URL must use http or https")
	}
	if u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("webhook URL must contain only scheme, host, port and path")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || host == "localhost.localdomain" || host == "metadata" || host == "metadata.google.internal" || host == "instance-data" {
		return nil, fmt.Errorf("webhook target is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil && blockedWebhookIP(ip) {
		return nil, fmt.Errorf("webhook target is not allowed")
	}
	// DNS rebinding is addressed by the production transport below only when
	// this helper is used by the default constructor. Custom test clients are
	// accepted because they never perform a real network lookup.
	return u, nil
}

func blockedWebhookIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4(169, 254, 169, 254)) || ip.Equal(net.ParseIP("fd00:ec2::254"))
}

func normalizeJSON(b []byte) []byte {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == 0 {
		return []byte(`""`)
	}
	if json.Valid(b) {
		return b
	}
	out, _ := json.Marshal(string(b))
	return out
}

// NewSafeHTTPWebhookExecutor creates a production executor with DNS
// rebinding protection. The transport resolves every address and refuses
// blocked IP ranges before dialing; redirects are never followed.
func NewSafeHTTPWebhookExecutor(timeout time.Duration, allowPrivate bool) *HTTPWebhookExecutor {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: noRedirect, Transport: &http.Transport{
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
			if len(ips) == 0 {
				return nil, fmt.Errorf("webhook host has no addresses")
			}
			for _, ip := range ips {
				if ip.Equal(net.IPv4(169, 254, 169, 254)) || ip.Equal(net.ParseIP("fd00:ec2::254")) || (!allowPrivate && blockedWebhookIP(ip)) {
					return nil, fmt.Errorf("resolved webhook address is not allowed")
				}
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}}
	return NewHTTPWebhookExecutor(client, timeout)
}

// WebhookTimeoutHeader is optional metadata useful to receivers and tests.
func WebhookTimeoutHeader(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }
