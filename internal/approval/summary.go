package approval

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// CommandSummary derives a display label only; it never evaluates policy or runs commands.
func CommandSummary(command string) string { return summarizeCommand(command, 0) }

func summarizeCommand(command string, depth int) string {
	if depth > 8 || len(command) > maxCommandBytes {
		return "shell command"
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 {
		return "shell command"
	}
	args, ok := summaryArgs(file.Stmts[0])
	if !ok {
		return "shell command"
	}
	return summarizeArgs(args, depth)
}

func summaryArgs(stmt *syntax.Stmt) ([]string, bool) {
	if unsupportedStatement(stmt) || stmt.Semicolon.IsValid() {
		return nil, false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) == 0 {
		return nil, false
	}
	args := make([]string, len(call.Args))
	for i, word := range call.Args {
		value, literal := summaryLiteral(word.Parts)
		if !literal {
			return nil, false
		}
		args[i] = value
	}
	return args, true
}

func summarizeArgs(args []string, depth int) string {
	name := filepath.Base(args[0])
	if name == "." || name == "/" || args[0] == "" {
		return "shell command"
	}
	switch name {
	case "sh", "bash", "zsh":
		body, recognized := summaryShellBody(name, args[1:])
		if !recognized {
			return "shell command"
		}
		return summarizeCommand(body, depth+1)
	case "git", "make":
		return summarizeAction(name, args[1:])
	default:
		return name
	}
}

func summaryLiteral(parts []syntax.WordPart) (string, bool) {
	var value strings.Builder
	for _, part := range parts {
		if lit, ok := part.(*syntax.Lit); ok {
			if strings.ContainsAny(lit.Value, "*?[{~\\") {
				return "", false
			}
			value.WriteString(lit.Value)
			continue
		}
		text, ok := literalPart(part, false)
		if !ok {
			return "", false
		}
		value.WriteString(text)
	}
	return value.String(), true
}

func summaryShellBody(name string, args []string) (string, bool) {
	if body, ok := wrapperBody(name, args); ok {
		return body, true
	}
	if len(args) == 2 && (args[0] == "-c" || args[0] == "-lc") {
		return args[1], true
	}
	return "", false
}

func summarizeAction(name string, args []string) string {
	action := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return summaryRemaining(name, action, args[i+1:])
		}
		if strings.HasPrefix(arg, "-") {
			operand, known := summaryOption(name, arg)
			if !known {
				return "shell command"
			}
			if operand {
				i++
				if i == len(args) {
					return "shell command"
				}
			}
			continue
		}
		if name == "git" {
			return name + " " + arg
		}
		if strings.Contains(arg, "=") {
			continue
		}
		if action != "" {
			return name
		}
		action = arg
	}
	return summaryRemaining(name, action, nil)
}

func summaryRemaining(name, action string, args []string) string {
	if name == "git" && len(args) > 0 {
		return name + " " + args[0]
	}
	if action != "" {
		args = append([]string{action}, args...)
	}
	if len(args) == 1 {
		return name + " " + args[0]
	}
	return name
}

// Recognize only options whose operand consumption is unambiguous.
func summaryOption(name, arg string) (operand, known bool) {
	var values, flags []string
	if name == "git" {
		values = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env"}
		flags = []string{"--no-pager", "--paginate", "-p", "-P", "--bare", "--no-optional-locks", "--literal-pathspecs", "--no-replace-objects", "--version", "--help"}
	} else {
		values = []string{"-C", "--directory", "-f", "--file", "--makefile", "-I", "--include-dir", "-o", "--old-file", "-W", "--what-if", "--eval"}
		flags = []string{"-n", "--just-print", "--dry-run", "-s", "--silent", "-k", "--keep-going", "-B", "--always-make", "-e", "-i", "-r", "-R", "--no-print-directory", "--version", "--help"}
	}
	for _, option := range values {
		if operand, ok := summaryValueOption(option, arg); ok {
			return operand, true
		}
	}
	for _, option := range flags {
		if arg == option {
			return false, true
		}
	}
	return false, name == "make" && summaryNumericOption(arg)
}

func summaryValueOption(option, arg string) (bool, bool) {
	if arg == option {
		return true, true
	}
	if strings.HasPrefix(option, "--") {
		return false, strings.HasPrefix(arg, option+"=")
	}
	return false, strings.HasPrefix(arg, option) && len(arg) > len(option)
}

// Optional numeric operands must be attached, avoiding target ambiguity.
func summaryNumericOption(arg string) bool {
	for _, prefix := range []string{"-j", "-l", "--jobs=", "--load-average="} {
		if !strings.HasPrefix(arg, prefix) {
			continue
		}
		value := strings.TrimPrefix(arg, prefix)
		if value == "" {
			return false
		}
		for _, digit := range value {
			if digit != '.' && (digit < '0' || digit > '9') {
				return false
			}
		}
		return true
	}
	return false
}
