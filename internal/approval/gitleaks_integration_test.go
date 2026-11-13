//go:build integration

package approval

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// Exercise the installed local scanner with fabricated request bytes only.
// No candidate shell process or HTTP request is ever executed.
func TestInstalledGitleaksGET(t *testing.T) {
	if !(Gitleaks{}).Discover() {
		t.Skip("Gitleaks not installed")
	}
	token := "ghp_" + "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4"
	for _, tt := range []struct {
		name, query, reason string
		outcome             Outcome
	}{
		{"clean", "public", "get.scan_clean", Approve},
		{"synthetic token", token, "get.secret_detected", Decline},
		{"encoded token", base64.StdEncoding.EncodeToString([]byte(token)), "get.secret_detected", Decline},
		{"token with suppression", token + " gitleaks:allow", "get.validation_incomplete", Decline},
		{"percent encoded suppression", token + "%20gitleaks%3Aallow", "get.validation_incomplete", Decline},
		{"base64 encoded suppression", base64.StdEncoding.EncodeToString([]byte(token + " gitleaks:allow")), "get.validation_incomplete", Decline},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op := agent.HTTPOperation{Method: "GET", URL: "https://example.com/?value=" + tt.query}
			got := (Policy{}).Evaluate(context.Background(), op)
			if got.Outcome != tt.outcome || got.Reason != tt.reason {
				t.Fatalf("got %+v; want %s %s", got, tt.outcome, tt.reason)
			}
			if strings.Contains(got.ScannerVersion+got.ScannerConfig+got.Reason, token) {
				t.Fatal("secret in scanner metadata")
			}
			if got.Outcome == Approve {
				t.Logf("scanner=%s config=%s", got.ScannerVersion, got.ScannerConfig)
			}
		})
	}
	// Explicit outbound fields use the same suppression protection as URL data.
	for _, op := range []agent.HTTPOperation{
		{Method: "GET", URL: "https://example.com", Headers: map[string]string{"X-Input": token + " gitleaks:allow"}},
		{Method: "GET", URL: "https://example.com", Body: url.QueryEscape(token + " gitleaks:allow")},
	} {
		if got := (Policy{}).Evaluate(context.Background(), op); got != decline("get.validation_incomplete") {
			t.Fatalf("got %+v", got)
		}
	}
}
