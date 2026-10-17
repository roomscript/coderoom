package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

var errNotInPath = errors.New("not found in PATH")

// versionCheckWaitDelay bounds how long a cancelled version check waits for
// its output pipes after the process is killed. npx starts node as a child,
// which may still hold the pipes when npx itself exits.
const versionCheckWaitDelay = time.Second

// ExternalDependencies lists the checks for the command Start would launch.
// With WithAppServerCommand, only that command's presence is checked: an
// arbitrary app-server peer has no known way to report its version.
func (c *Client) ExternalDependencies() []agent.Dependency {
	if len(c.proc.appServerCmd) > 0 {
		return []agent.Dependency{installedDependency(c.proc.appServerCmd[0])}
	}
	pkg := codexPackage()
	return []agent.Dependency{
		installedDependency("npx"),
		{
			Description: "npx " + pkg + " runs",
			Verify: func(ctx context.Context) error {
				return runVersionCommand(ctx, c.proc.cwd, "npx", pkg)
			},
		},
	}
}

func installedDependency(name string) agent.Dependency {
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

// runVersionCommand runs `name args... --version` in dir. On failure the
// error carries the last line the command printed, which is usually the
// reason.
func runVersionCommand(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, append(args, "--version")...)
	cmd.Dir = dir
	cmd.WaitDelay = versionCheckWaitDelay
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
