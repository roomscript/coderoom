//go:build integration

package codex_test

import (
	"os"
	"os/exec"
	"testing"
)

// TestRecordModelScenarios exercises the standard recorder with conversation inputs.
func TestRecordModelScenarios(t *testing.T) {
	if os.Getenv("CODEX_MODEL_RECORD") != "1" {
		t.Skip("set CODEX_MODEL_RECORD=1 to record live model scenarios")
	}
	for _, name := range []string{"model-success", "model-failure", "model-interrupted"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("go", "run", "./cmd/codex-record", "0.163.0", name)
			cmd.Dir = mustRepoRoot(t)
			output, err := cmd.CombinedOutput()
			t.Logf("%s", output)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
