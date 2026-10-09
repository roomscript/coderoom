package interpreter

import (
	"strings"

	"github.com/roomscript/coderoom/internal/shell"
)

const shellRecordAlias = "shell"

func formatShellResult(result shell.Result) string {
	sections := []string{"status: " + string(result.Status)}
	if result.Stdout != "" {
		sections = append(sections, "stdout:\n"+result.Stdout)
	}
	if result.Stderr != "" {
		sections = append(sections, "stderr:\n"+result.Stderr)
	}
	if result.Err != nil {
		sections = append(sections, "error:\n"+result.Err.Error())
	}
	return strings.Join(sections, "\n")
}
