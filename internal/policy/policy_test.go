package policy_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/policy"
)

func TestSetEnable(t *testing.T) {
	for _, name := range []policy.Name{policy.SendNotices, policy.EchoInvites} {
		t.Run(string(name), func(t *testing.T) {
			policies := policy.NewSet()
			if policies.Enabled(name) {
				t.Fatal("policy should be disabled by default")
			}
			if err := policies.Enable(name); err != nil {
				t.Fatalf("Enable: %v", err)
			}
			if err := policies.Enable(name); err != nil {
				t.Fatalf("second Enable: %v", err)
			}
			if !policies.Enabled(name) {
				t.Fatal("policy should be enabled")
			}
		})
	}
}

func TestSetCopiesShareState(t *testing.T) {
	policies := policy.NewSet()
	copyOfPolicies := policies
	if err := copyOfPolicies.Enable(policy.SendNotices); err != nil {
		t.Fatalf("Enable through copy: %v", err)
	}
	if !policies.Enabled(policy.SendNotices) {
		t.Fatal("original should observe policy enabled through copy")
	}
}

func TestSetRejectsUnknownPolicy(t *testing.T) {
	policies := policy.NewSet()
	if err := policies.Enable(policy.Name("unknown")); err == nil {
		t.Fatal("Enable: expected error")
	}
}

func TestSetZeroValueIsInert(t *testing.T) {
	var policies policy.Set
	if policies.Enabled(policy.SendNotices) {
		t.Fatal("zero-value policies should report disabled")
	}
	if err := policies.Enable(policy.SendNotices); err == nil {
		t.Fatal("zero-value Enable: expected initialization error")
	}
}
