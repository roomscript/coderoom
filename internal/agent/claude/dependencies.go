package claude

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/depcheck"
)

// ExternalDependencies lists the checks for the command Start would launch.
// With WithCommand, only that command's presence is checked: an arbitrary
// peer has no known way to report its version.
func (c *Client) ExternalDependencies() []agent.Dependency {
	if len(c.proc.command) > 0 {
		return []agent.Dependency{depcheck.Installed(c.proc.command[0])}
	}
	return []agent.Dependency{
		depcheck.Installed("claude"),
		depcheck.ReportsVersion(c.proc.cwd, "claude"),
	}
}
