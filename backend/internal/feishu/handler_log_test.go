package feishu

// handler_log_test.go asserts that the feishu handler never writes raw
// verify-token bytes to the standard logger. The original implementation
// did log.Printf("[feishu] url_verification token mismatch: got=%q want=%q", ...)
// which leaks whatever the caller passed as the Feishu verify token.
// The helper logTokenMismatch now prints lengths and a 6-character
// prefix only, never the raw token.

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// captureLog runs fn with the standard logger redirected to a buffer
// and returns whatever was written.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	prevOut := log.Writer()
	prevFlags := log.Flags()
	buf := &bytes.Buffer{}
	log.SetOutput(buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

func TestLogTokenMismatch_NeverLogsRawSecret(t *testing.T) {
	const rawToken = "verylongfeishuVERIFYtoken_secret_DO_NOT_LOG_PLEASE_42"
	const wantToken = "expectedchallengebyteVALUE_42"

	got := captureLog(t, func() {
		logTokenMismatch(len(rawToken), len(wantToken), rawToken, wantToken)
	})

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one log line, got %d: %q", len(lines), got)
	}
	line := lines[0]

	if strings.Contains(line, rawToken) {
		t.Fatalf("raw token leaked into log line: %s", line)
	}
	if strings.Contains(line, wantToken) {
		t.Fatalf("want token leaked into log line: %s", line)
	}
	if !strings.Contains(line, "got_len=") || !strings.Contains(line, "want_len=") {
		t.Fatalf("expected length fields in log line: %s", line)
	}
	if !strings.Contains(line, "got_prefix=") || !strings.Contains(line, "want_prefix=") {
		t.Fatalf("expected prefix fields in log line: %s", line)
	}
}

func TestLogTokenMismatch_OnlyPrefixIsLogged(t *testing.T) {
	// 6-character prefix policy: even a long token must show only the
	// first six bytes verbatim.
	const rawToken = "abcdefghij_long_tail_DO_NOT_LEAK"
	got := captureLog(t, func() {
		logTokenMismatch(len(rawToken), len(rawToken), rawToken, rawToken)
	})

	if strings.Contains(got, "ghij") {
		t.Fatalf("suffix beyond 6-char prefix leaked: %s", got)
	}
	if !strings.Contains(got, "abcdef") {
		t.Fatalf("expected 6-char prefix in log: %s", got)
	}
}
