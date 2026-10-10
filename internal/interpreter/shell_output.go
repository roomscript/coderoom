package interpreter

import (
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/shell"
)

const shellRecordAlias = "shell"

func formatShellResult(result shell.Result) string {
	return std.FormatShellResult(result)
}
