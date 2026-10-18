package registry

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
)

// NewDefault returns a registry of the backends users can configure.
func NewDefault(cwd string) (*Registry, error) {
	return New(cwd, Codex)
}

// Codex builds a Codex app-server agent.
func Codex(cwd string, o Options) agent.Agent {
	opts := []codex.Option{
		codex.WithApprovalListener(o.ApprovalListener),
		codex.WithSystemPrompt(o.SystemPrompt),
	}
	if o.Context != nil {
		opts = append(opts, codex.WithContext(o.Context))
	}
	if o.Log != nil {
		opts = append(opts, codex.WithObserver(codex.NewLogObserver(o.Log, o.Alias)))
	}
	return codex.New(cwd, opts...)
}
