package registry

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/claude"
	"github.com/roomscript/coderoom/internal/agent/codex"
	"github.com/roomscript/coderoom/internal/agent/wirelog"
)

// NewDefault returns a registry of the backends users can configure.
func NewDefault(cwd string) (*Registry, error) {
	return New(cwd, Codex, Claude)
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
		opts = append(opts, codex.WithObserver(wirelog.New(o.Log, o.Alias)))
	}
	return codex.New(cwd, opts...)
}

// Claude builds a Claude Code stream-json agent.
func Claude(cwd string, o Options) agent.Agent {
	opts := []claude.Option{
		claude.WithApprovalListener(o.ApprovalListener),
		claude.WithSystemPrompt(o.SystemPrompt),
	}
	if o.Context != nil {
		opts = append(opts, claude.WithContext(o.Context))
	}
	if o.Log != nil {
		opts = append(opts, claude.WithObserver(wirelog.New(o.Log, o.Alias)))
	}
	return claude.New(cwd, opts...)
}
