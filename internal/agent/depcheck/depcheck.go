// Package depcheck builds the agent.Dependency checks backends report
// through agent.ExternalDependencies.
package depcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

var errNotInPath = errors.New("not found in PATH")

// Installed checks that name is found in PATH.
func Installed(name string) agent.Dependency {
	return agent.Dependency{
		Description: name + " is installed",
		Verify: func(context.Context) error {
			_, err := exec.LookPath(name)
			if errors.Is(err, exec.ErrNotFound) {
				return errNotInPath
			}
			if err != nil {
				return fmt.Errorf("look up %s: %w", name, err)
			}
			return nil
		},
	}
}

// ReportsVersion checks that `name args... --version` succeeds in dir.
func ReportsVersion(dir, name string, args ...string) agent.Dependency {
	return agent.Dependency{
		Description: strings.Join(append([]string{name}, args...), " ") + " --version runs",
		Verify: func(context.Context) error {
			return runVersionCommand(dir, name, args...)
		},
	}
}

// runVersionCommand runs `name args... --version` in dir. On failure the
// error carries the last line the command printed, which is usually the
// reason.
//
// Cancellation is checked between dependencies; an active command must finish
// so launchers such as npx can leave their package cache consistent.
func runVersionCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, append(args, "--version")...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if last := lastLine(out.String()); last != "" {
		return fmt.Errorf("%w: %s", err, last)
	}
	return fmt.Errorf("%w", err)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}
