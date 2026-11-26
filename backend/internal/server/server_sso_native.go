package server

import (
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

const nativeSSOReturnPath = "/auth/sso/app-return"

var nativeSSOReturnPage = template.Must(template.New("sso-app-return").Parse(`<!doctype html>
<html lang="zh-CN">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>返回 OpenPocket</title>
<p>企业账号已验证。</p>
<p><a href="{{.}}">返回 OpenPocket</a></p>
</html>`))

// The browser holds the existing login cookie. The verifier stays with the app.
func (s *Server) handleNativeSSOStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if !s.ssoEnabled() || s.oidcEnabled() || s.cfg.RedClawSsoAppOrigin == "" {
		writeError(w, http.StatusNotFound, "native sso not enabled")
		return
	}
	origin, err := url.Parse("https://" + r.Host)
	if !requestIsHTTPS(r) || err != nil || strings.EqualFold(origin.Hostname(), strings.TrimPrefix(s.cfg.RedClawSsoAppOrigin, "https://")) {
		writeError(w, http.StatusBadRequest, "native sso requires HTTPS on a separate host from the app link")
		return
	}
	values := r.URL.Query()["challenge"]
	if len(values) != 1 || !validNativeSSOProof(values[0]) {
		writeError(w, http.StatusBadRequest, "invalid app challenge")
		return
	}
	authorizeURL, err := s.startRedClawSSO(w, r, values[0])
	if err != nil || authorizeURL == "" {
		writeError(w, http.StatusServiceUnavailable, "failed to start sso login")
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func validNativeSSOProof(value string) bool {
	if len(value) != base64.RawURLEncoding.EncodedLen(sha256.Size) {
		return false
	}
	_, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil
}

func nativeSSOChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func writeNativeSSOReturn(w http.ResponseWriter, origin, code, challenge string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	target := origin + nativeSSOReturnPath + "#" + url.Values{"code": {code}, "challenge": {challenge}}.Encode()
	_ = nativeSSOReturnPage.Execute(w, target)
}

func (s *Server) handleNativeSSOReturnFallback(w http.ResponseWriter, r *http.Request) {
	if !s.ssoEnabled() || s.oidcEnabled() || s.cfg.RedClawSsoAppOrigin == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>在 OpenPocket 中登录</title>
<p>此设备未能打开 OpenPocket。请确认已安装发行版且允许打开应用链接，然后回到 App 重新发起企业登录。</p></html>`))
}
