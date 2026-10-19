//go:build integration

package claude_test

import (
	"context"
	"testing"

	"github.com/roomscript/coderoom/internal/agent/claude"
)

// TestIntegExternalDependencies runs every dependency check against the
// installed tools. It needs no Claude login: the checks stop at --version.
func TestIntegExternalDependencies(t *testing.T) {
	c := claude.New(t.TempDir())
	deps := c.ExternalDependencies()
	if len(deps) == 0 {
		t.Fatal("expected at least one dependency")
	}
	for _, dep := range deps {
		if err := dep.Verify(context.Background()); err != nil {
			t.Fatalf("%s: %v", dep.Description, err)
		}
		t.Logf("%s: ok", dep.Description)
	}
}
