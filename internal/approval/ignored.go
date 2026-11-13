package approval

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// CheckIgnored protects ignored files without invoking repository code.
func CheckIgnored(root string) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, path string) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/git", "check-ignore", "--no-index", "-q", "--", path)
		cmd.Dir = root
		cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		cmd.WaitDelay = time.Second
		err := cmd.Run()
		if err == nil {
			return true, nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check ignored path: %w", err)
	}
}
