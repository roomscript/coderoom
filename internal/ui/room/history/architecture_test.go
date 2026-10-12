package history

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestArchitecture_historyHasNoDirectRuntimeImports(t *testing.T) {
	command := exec.CommandContext(t.Context(), "go", "list", "-json=Imports", ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list history imports: %v\n%s", err, output)
	}
	var pkg struct{ Imports []string }
	if err := json.Unmarshal(output, &pkg); err != nil {
		t.Fatalf("decode history imports: %v", err)
	}
	for _, path := range pkg.Imports {
		for _, root := range []string{
			"github.com/roomscript/coderoom/internal/agent",
			"github.com/roomscript/coderoom/internal/session",
		} {
			if path == root || strings.HasPrefix(path, root+"/") {
				t.Errorf("history directly imports forbidden runtime package %s", path)
			}
		}
	}
}
