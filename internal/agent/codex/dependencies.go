package codex

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/depcheck"
)

// ExternalDependencies lists the checks for the command Start would launch.
// With WithAppServerCommand, only that command's presence is checked: an
// arbitrary app-server peer has no known way to report its version.
func (c *Client) ExternalDependencies() []agent.Dependency {
	if len(c.proc.appServerCmd) > 0 {
		return []agent.Dependency{depcheck.Installed(c.proc.appServerCmd[0])}
	}
	pkg := codexPackage()
	return []agent.Dependency{
		depcheck.Installed("npx"),
		depcheck.ReportsVersion(c.proc.cwd, "npx", pkg),
	}
}
