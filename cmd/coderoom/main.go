// Command coderoom is the CLI entry point for coderoom.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/registry"
	"github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/ui"
)

const (
	// backendEnvVar sets the user's default backend.
	backendEnvVar = "CODEROOM_BACKEND"
	// builtinBackend is the backend used when neither a participant file nor
	// CODEROOM_BACKEND names one.
	builtinBackend = "codex"
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

	backends, err := registry.NewDefault(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backends: %v\n", err)
		return 1
	}
	cfg, err := loadConfig(cwd, configDefaults(), backends)
	if err != nil {
		reportConfigErrors(err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log, closeLog, err := openAgentLog(*agentLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer closeLog()
	sess := session.New(
		session.WithContext(ctx),
		session.WithConfig(cfg),
		session.WithAgentFactory(agentFactory(backends, log)),
	)

	defer sess.Shutdown()
	return runUI(ctx, sess, cwd, func() {
		sess.StartDependencyChecks(backends, backendsInUse(cfg))
	})
}

func configDefaults() config.Defaults {
	if backend := strings.TrimSpace(os.Getenv(backendEnvVar)); backend != "" {
		return config.Defaults{Backend: config.BackendRequest{Name: backend, Source: backendEnvVar}}
	}
	return config.Defaults{Backend: config.BackendRequest{Name: builtinBackend, Source: "built-in default"}}
}

// loadConfig loads the participant configuration and validates the backends
// it names. It reports file problems first, then unknown backends, so one
// startup lists every problem.
func loadConfig(cwd string, defaults config.Defaults, backends *registry.Registry) (*config.Config, error) {
	cfg, loadErr := config.Load(cwd, defaults)
	if cfg == nil {
		return nil, fmt.Errorf("load participant config: %w", loadErr)
	}
	if err := errors.Join(loadErr, backends.ValidateConfig(cfg)); err != nil {
		return nil, err
	}
	return cfg, nil
}

// reportConfigErrors prints each configuration problem on its own line.
func reportConfigErrors(err error) {
	fmt.Fprintln(os.Stderr, "coderoom: configuration errors:")
	for _, line := range strings.Split(err.Error(), "\n") {
		fmt.Fprintf(os.Stderr, "  %s\n", line)
	}
}

// runUI runs the interactive room. onAttached runs once the room receives
// session events.
func runUI(ctx context.Context, sess *session.Session, cwd string, onAttached func()) int {
	var opts []ui.Option
	if strings.TrimSpace(os.Getenv("CODEROOM_DEBUG")) == "1" {
		opts = append(opts, ui.WithDebug(true))
	}
	opts = append(opts, ui.WithStartupHelpTip(true))

	observer := ui.NewObserver()
	defer observer.Close()
	interp, err := interpreter.NewWithApplicationCommands(ctx, sess, cwd, observer.ApplicationCommands(), interpreter.WithObserver(observer))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer interp.Close()
	onAttached()
	model := ui.New(interp, observer, cwd, opts...)
	defer model.Close()
	if _, err := tea.NewProgram(model).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// openAgentLog opens the --agent-log file. With no file it returns a nil
// writer and a no-op close.
func openAgentLog(path string) (io.Writer, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open agent log %q: %w", path, err)
	}
	return f, func() {
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "agent-log close: %v\n", err)
		}
	}, nil
}

// agentFactory builds each participant's configured backend with the
// session's facilities.
func agentFactory(backends *registry.Registry, log io.Writer) session.AgentFactory {
	return func(s *session.Session, cfg config.ParticipantConfig) (agent.Agent, error) {
		// Session registers the invited participant before invoking this factory.
		view, _ := s.ParticipantByName(cfg.Alias)
		return backends.Build(cfg.Backend.Name, registry.Options{
			Context:          s.CreateAgentContext(view.Alias),
			ApprovalListener: s.ApprovalListener(view.Alias),
			SystemPrompt:     cfg.Prompt,
			Alias:            cfg.Alias,
			Log:              log,
		})
	}
}

// backendsInUse lists each backend named by the configuration once: every
// participant file's backend and the default.
func backendsInUse(cfg *config.Config) []string {
	return slices.Compact(slices.Sorted(maps.Values(cfg.ConfiguredBackends())))
}
