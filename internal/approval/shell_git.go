package approval

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (p Policy) evaluateGit(ctx context.Context, args []string, cwd string) Result {
	if len(args) == 0 || !supportedGitCommand(args[0]) {
		return review("shell.command_unsupported")
	}
	if result := p.validateGitArgs(args, cwd); result.Outcome != Approve {
		return result
	}
	if !gitConfigurationSafe(ctx, cwd) {
		return review("shell.git_config_unvalidated")
	}
	return approve("shell.supported")
}

func supportedGitCommand(name string) bool {
	return name == "diff" || name == "show" || name == "status"
}

func (p Policy) validateGitArgs(args []string, cwd string) Result {
	paths := false
	for _, arg := range args[1:] {
		if arg == "--" {
			paths = true
			continue
		}
		if paths {
			if _, ok := p.resolveProjectPath(cwd, arg, false); !ok {
				return review("shell.path_unvalidated")
			}
			continue
		}
		if !p.gitArgumentSupported(args[0], arg, cwd) {
			return review("shell.arguments_unsupported")
		}
	}
	return approve("shell.supported")
}

func (p Policy) gitArgumentSupported(command, arg, cwd string) bool {
	if command == "status" {
		return arg == "--short" || arg == "--porcelain"
	}
	switch arg {
	case "--stat", "--cached", "--staged", "--name-only", "--name-status", "--no-ext-diff", "--no-textconv":
		return true
	}
	if command != "show" {
		return false
	}
	if arg == "HEAD" {
		return true
	}
	if !strings.HasPrefix(arg, "HEAD:") {
		return false
	}
	_, ok := p.resolveProjectPath(cwd, strings.TrimPrefix(arg, "HEAD:"), false)
	return ok
}

func gitConfigurationSafe(ctx context.Context, cwd string) bool {
	if !gitEnvironmentSafe() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/git", "config", "--null", "--list")
	cmd.Dir = cwd
	cmd.WaitDelay = time.Second
	output := &limitedOutput{}
	cmd.Stdout = output
	if cmd.Run() != nil || output.truncated {
		return false
	}
	for _, entry := range strings.Split(string(output.data), "\x00") {
		key, _, _ := strings.Cut(entry, "\n")
		if !gitConfigKeySafe(strings.ToLower(key)) {
			return false
		}
	}
	return true
}

func gitEnvironmentSafe() bool {
	for _, env := range os.Environ() {
		key, value, _ := strings.Cut(env, "=")
		if (key == "GIT_PAGER" || key == "PAGER") && value == "cat" && systemExecutable("cat") {
			continue
		}
		if strings.HasPrefix(key, "GIT_") || key == "PAGER" {
			return false
		}
	}
	return true
}

func gitConfigKeySafe(key string) bool {
	switch key {
	case "core.fsmonitor", "core.pager", "core.sshcommand":
		return false
	}
	for _, prefix := range []string{"pager.", "diff.", "filter.", "alias."} {
		if strings.HasPrefix(key, prefix) {
			return false
		}
	}
	return true
}
