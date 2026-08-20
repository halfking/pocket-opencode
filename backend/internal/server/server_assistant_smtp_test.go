package server

// Validation coverage for the SMTP fields on the email-account endpoints.
//
// The PUT handler treats smtpHost as the gate for writing any SMTP column, so
// port/credential arriving without a host would otherwise be silently dropped.
// smtpPort==0 is legal only as part of clearing the config (smtpHost=="").

import (
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func TestValidateSMTPInput(t *testing.T) {
	cases := []struct {
		name      string
		host      *string
		port      *int
		cred      *string
		wantError string
	}{
		// Nothing provided — the caller isn't touching SMTP at all.
		{"all absent", nil, nil, nil, ""},

		// Port/credential without host have no write target.
		{"port without host", nil, intPtr(587), nil, "smtpHost required when smtpPort is provided"},
		{"credential without host", nil, nil, strPtr("secret"), "smtpHost required when smtpPassword is provided"},
		{"zero port without host is inert", nil, intPtr(0), nil, ""},
		{"empty credential without host is inert", nil, nil, strPtr(""), ""},

		// Normal configuration.
		{"host and valid port", strPtr("smtp.example.com"), intPtr(587), nil, ""},
		{"host with implicit tls port", strPtr("smtp.example.com"), intPtr(465), strPtr("secret"), ""},
		{"host without port keeps stored port", strPtr("smtp.example.com"), nil, nil, ""},

		// Range checks.
		{"port too large", strPtr("smtp.example.com"), intPtr(70000), nil, "smtpPort must be between 1 and 65535"},
		{"port negative", strPtr("smtp.example.com"), intPtr(-1), nil, "smtpPort must be between 1 and 65535"},
		{"zero port with host is invalid", strPtr("smtp.example.com"), intPtr(0), nil, "smtpPort must be between 1 and 65535"},

		// Clearing: empty host is the explicit reset signal, so port 0 is legal.
		{"clear config", strPtr(""), intPtr(0), strPtr(""), ""},
		{"clear config whitespace host", strPtr("   "), intPtr(0), strPtr(""), ""},
		{"clear host but bogus port", strPtr(""), intPtr(70000), nil, "smtpPort must be between 1 and 65535"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSMTPInput(tc.host, tc.port, tc.cred)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected error containing %q, got %q", tc.wantError, err.Error())
			}
		})
	}
}
