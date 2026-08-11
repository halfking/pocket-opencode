package server

import (
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

func TestBuildMessageWithHeadersSanitizesAndSortsHeaders(t *testing.T) {
	message := email.OutgoingMessage{
		From:    "sender@example.com",
		To:      []string{"recipient@example.com"},
		Subject: "Re: hello\r\nBcc: attacker@example.com",
		Body:    "reply",
		Headers: map[string]string{
			"References":     "<msg-1>",
			"Auto-Submitted": "auto-replied\r\nX-Injected: yes",
			"In-Reply-To":    "<msg-1>",
			"Bad\nName":      "ignored",
		},
	}
	got := string(buildMessageWithHeaders(message))
	if strings.Contains(got, "Bcc: attacker") || strings.Contains(got, "X-Injected: yes") {
		t.Fatalf("header injection survived: %q", got)
	}
	if !strings.Contains(got, "Auto-Submitted: auto-replied\r\n") {
		t.Fatalf("sanitized auto reply header missing: %q", got)
	}
	if strings.Index(got, "Auto-Submitted:") > strings.Index(got, "In-Reply-To:") {
		t.Fatalf("headers are not stable/sorted: %q", got)
	}
	if !strings.Contains(got, "References: <msg-1>\r\n") {
		t.Fatalf("references header missing: %q", got)
	}
}
