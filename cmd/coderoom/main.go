// Command coderoom is the CLI entry point for coderoom.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
	"github.com/roomscript/coderoom/internal/agent/echo"
	"github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/ui"
)

func main() {
	os.Exit(run())
}

func run() int {
	agentLog := flag.String("agent-log", "", "write raw agent JSON-RPC traffic to `file`")
	flag.Parse()

	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", flag.Args())
		flag.Usage()
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cleanup, factory, err := agentFactory(cwd, *agentLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent factory: %v\n", err)
		return 1
	}
	if cleanup != nil {
		defer cleanup()
	}
	cfg := config.New(cwd)
	sess := session.New(
		session.WithContext(ctx),
		session.WithConfig(cfg),
		session.WithAgentFactory(factory),
	)

	var opts []ui.Option
	if strings.TrimSpace(os.Getenv("CODEROOM_DEBUG")) == "1" {
		opts = append(opts, ui.WithDebug(true))
	}
	opts = append(opts, ui.WithStartupHelpTip(true))

	observer := ui.NewObserver()
	defer observer.Close()
	interp := interpreter.New(ctx, sess, cwd, interpreter.WithObserver(observer))
	defer interp.Close()
	model := ui.New(interp, observer, cwd, opts...)
	defer model.Close()
	if _, err := tea.NewProgram(model).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func agentFactory(cwd, agentLog string) (cleanup func(), factory session.AgentFactory, err error) {
	if agentLog == "" {
		return nil, func(s *session.Session, cfg config.ParticipantConfig, backend session.AgentBackend) agent.Agent {
			if backend == session.AgentBackendEcho {
				return echo.New()
			}
			return codex.New(
				cwd,
				codex.WithContext(s.CreateAgentContext(cfg.Alias)),
				codex.WithApprovalListener(s.ApprovalListener(cfg.Alias)),
				codex.WithSystemPrompt(cfg.Prompt),
			)
		}, nil
	}

	f, err := os.OpenFile(filepath.Clean(agentLog), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open agent log %q: %w", agentLog, err)
	}
	return func() {
			if err := f.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "agent-log close: %v\n", err)
			}
		}, func(s *session.Session, cfg config.ParticipantConfig, backend session.AgentBackend) agent.Agent {
			if backend == session.AgentBackendEcho {
				return echo.New()
			}
			return codex.New(
				cwd,
				codex.WithContext(s.CreateAgentContext(cfg.Alias)),
				codex.WithObserver(codex.NewLogObserver(f, cfg.Alias)),
				codex.WithApprovalListener(s.ApprovalListener(cfg.Alias)),
				codex.WithSystemPrompt(cfg.Prompt),
			)
		}, nil
}
