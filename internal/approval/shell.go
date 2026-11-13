package approval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"mvdan.cc/sh/v3/syntax"
)

const maxCommandBytes = 128 * 1024

func (p Policy) evaluateCommand(ctx context.Context, op agent.CommandOperation, depth int) Result {
	if depth > 8 || len(op.Command) > maxCommandBytes {
		return review("shell.limit")
	}
	if _, ok := p.resolveProjectPath(op.Cwd, ".", false); !ok {
		return review("shell.cwd_unvalidated")
	}
	if !shellEnvironmentSafe() {
		return review("shell.startup_unvalidated")
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(op.Command), "")
	if err != nil || len(file.Stmts) == 0 {
		return review("shell.syntax_unsupported")
	}
	return p.evaluateStatements(ctx, file.Stmts, op.Cwd, depth)
}

func (p Policy) evaluateStatements(ctx context.Context, statements []*syntax.Stmt, cwd string, depth int) Result {
	result := approve("shell.supported")
	for i, stmt := range statements {
		current, next := p.evaluateStatement(ctx, stmt, cwd, depth)
		if current.Outcome != Approve {
			return current
		}
		if next != cwd && i+1 < len(statements) {
			return review("shell.cwd_ambiguous")
		}
		cwd = next
		result = mergeApproval(result, current)
	}
	return result
}

func mergeApproval(previous, current Result) Result {
	if current.ScannerVersion != "" || current.Reason == "get.scan_clean" {
		return current
	}
	return previous
}

func (p Policy) evaluateStatement(ctx context.Context, stmt *syntax.Stmt, cwd string, depth int) (Result, string) {
	if ctx.Err() != nil {
		return review("policy.canceled"), cwd
	}
	if unsupportedStatement(stmt) {
		return review("shell.syntax_unsupported"), cwd
	}
	switch command := stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		return p.evaluateBinary(ctx, command, cwd, depth)
	case *syntax.CallExpr:
		return p.evaluateLiteralCall(ctx, command, cwd, depth)
	default:
		return review("shell.syntax_unsupported"), cwd
	}
}

func unsupportedStatement(stmt *syntax.Stmt) bool {
	return stmt.Background || stmt.Coprocess || stmt.Negated || len(stmt.Redirs) != 0
}

func (p Policy) evaluateBinary(ctx context.Context, command *syntax.BinaryCmd, cwd string, depth int) (Result, string) {
	if !supportedBinaryOperator(command.Op) {
		return review("shell.syntax_unsupported"), cwd
	}
	left, next := p.evaluateStatement(ctx, command.X, cwd, depth)
	if left.Outcome != Approve {
		return left, cwd
	}
	rightCwd := cwd
	if command.Op == syntax.AndStmt {
		rightCwd = next
	}
	right, end := p.evaluateStatement(ctx, command.Y, rightCwd, depth)
	if right.Outcome != Approve {
		return right, cwd
	}
	if command.Op == syntax.Pipe {
		return mergeApproval(left, right), cwd
	}
	if command.Op == syntax.OrStmt && (next != cwd || end != cwd) {
		return review("shell.cwd_ambiguous"), cwd
	}
	return mergeApproval(left, right), end
}

func supportedBinaryOperator(op syntax.BinCmdOperator) bool {
	return op == syntax.AndStmt || op == syntax.OrStmt || op == syntax.Pipe
}

func (p Policy) evaluateLiteralCall(ctx context.Context, command *syntax.CallExpr, cwd string, depth int) (Result, string) {
	if len(command.Assigns) != 0 || len(command.Args) == 0 {
		return review("shell.syntax_unsupported"), cwd
	}
	args := make([]string, len(command.Args))
	for i, word := range command.Args {
		value, ok := literalWord(word.Parts, false)
		if !ok {
			return p.incompleteCall(ctx, args), cwd
		}
		args[i] = value
	}
	return p.evaluateCall(ctx, args, cwd, depth)
}

func (p Policy) incompleteCall(ctx context.Context, args []string) Result {
	if len(args) > 0 && filepath.Base(args[0]) == "curl" && systemExecutable(args[0]) {
		return p.evaluateHTTP(ctx, agent.HTTPOperation{Method: "GET", Dynamic: true})
	}
	return review("shell.expansion_unsupported")
}

func literalWord(parts []syntax.WordPart, quoted bool) (string, bool) {
	var value strings.Builder
	for _, part := range parts {
		text, ok := literalPart(part, quoted)
		if !ok {
			return "", false
		}
		value.WriteString(text)
	}
	return value.String(), true
}

func literalPart(part syntax.WordPart, quoted bool) (string, bool) {
	switch item := part.(type) {
	case *syntax.Lit:
		return item.Value, quoted || !strings.ContainsAny(item.Value, "*?[{~\\=")
	case *syntax.SglQuoted:
		return item.Value, !item.Dollar
	case *syntax.DblQuoted:
		if item.Dollar {
			return "", false
		}
		return literalWord(item.Parts, true)
	default:
		return "", false
	}
}

func (p Policy) evaluateCall(ctx context.Context, args []string, cwd string, depth int) (Result, string) {
	name := filepath.Base(args[0])
	switch name {
	case "sh", "bash", "zsh":
		return p.evaluateWrapper(ctx, args, cwd, depth), cwd
	case "cd":
		return p.evaluateCD(args, cwd)
	case "echo":
		return evaluateEcho(args), cwd
	}
	if !systemExecutable(args[0]) {
		return review("shell.executable_unvalidated"), cwd
	}
	switch name {
	case "curl":
		return p.evaluateCurl(ctx, args[1:]), cwd
	case "git":
		return p.evaluateGit(ctx, args[1:], cwd), cwd
	default:
		return p.evaluateRead(args, cwd), cwd
	}
}

func (p Policy) evaluateWrapper(ctx context.Context, args []string, cwd string, depth int) Result {
	if !systemExecutable(args[0]) {
		return review("shell.executable_unvalidated")
	}
	name := filepath.Base(args[0])
	// Zsh's -f still reads the system zshenv. Only accept the common
	// literal grammar when those startup files contain no executable code.
	if name == "zsh" && !zshStartupSafe() {
		return review("shell.startup_unvalidated")
	}
	body, ok := wrapperBody(name, args[1:])
	if !ok {
		return review("shell.wrapper_unsupported")
	}
	return p.evaluateCommand(ctx, agent.CommandOperation{Command: body, Cwd: cwd}, depth+1)
}

func wrapperBody(name string, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	flags, body := args[:len(args)-1], args[len(args)-1]
	if name == "zsh" {
		return body, slices.Equal(flags, []string{"-f", "-c"})
	}
	if slices.Equal(flags, []string{"-c"}) {
		return body, true
	}
	return body, name == "bash" && slices.Equal(flags, []string{"--noprofile", "--norc", "-c"})
}

func (p Policy) evaluateCD(args []string, cwd string) (Result, string) {
	if args[0] != "cd" || len(args) != 2 || strings.HasPrefix(args[1], "-") {
		return review("shell.arguments_unsupported"), cwd
	}
	path, ok := p.resolveProjectPath(cwd, args[1], false)
	info, err := os.Stat(path)
	if !ok || err != nil || !info.IsDir() {
		return review("shell.path_unvalidated"), cwd
	}
	return approve("shell.supported"), path
}

func evaluateEcho(args []string) Result {
	if args[0] != "echo" || len(args) > 1 && strings.HasPrefix(args[1], "-") {
		return review("shell.arguments_unsupported")
	}
	for _, arg := range args[1:] {
		if strings.ContainsRune(arg, '\\') {
			return review("shell.arguments_unsupported")
		}
	}
	return approve("shell.supported")
}

func shellEnvironmentSafe() bool {
	if !shellStartupVariablesSafe() {
		return false
	}
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "BASH_FUNC_") {
			return false
		}
	}
	return true
}

// Resolve only installed system utilities, never a project executable with a
// familiar basename. This inspects metadata; no candidate is run. Some systems
// distribute coreutils as symlinks into /usr/lib/cargo/bin/coreutils.
func systemExecutable(name string) bool {
	path := name
	if !filepath.IsAbs(path) {
		if filepath.Base(path) != path {
			return false
		}
		var err error
		path, err = exec.LookPath(path)
		if err != nil {
			return false
		}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !systemExecutableDirectory(filepath.Dir(resolved)) {
		return false
	}
	info, err := os.Stat(resolved)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0022 == 0
}

func systemExecutableDirectory(dir string) bool {
	return dir == "/usr/bin" || dir == "/bin" || dir == "/usr/lib/cargo/bin/coreutils"
}

func zshStartupSafe() bool {
	for _, path := range []string{"/etc/zshenv", "/etc/zsh/zshenv"} {
		data, err := os.ReadFile(path) //nolint:gosec // fixed system zshenv paths, never candidate-provided
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || len(data) > 4096 {
			return false
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				return false
			}
		}
	}
	return true
}

func shellStartupVariablesSafe() bool {
	for _, name := range []string{"BASH_ENV", "ENV", "CDPATH", "SHELLOPTS", "BASHOPTS", "BASH_XTRACEFD"} {
		if os.Getenv(name) != "" {
			return false
		}
	}
	return true
}
