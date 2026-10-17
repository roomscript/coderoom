package interpreter

import (
	"fmt"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/shell"
)

type resolutionShell struct{}

func (resolutionShell) Cwd() string                             { return "/workspace" }
func (resolutionShell) Go(_ string, _ func(shell.Result)) error { return nil }

func TestCommandResolution_selectedDefinitionsNeedNoRegistry(t *testing.T) {
	interp, events := newApplicationInterpreter(t, ApplicationCommand{Name: "inspect", Run: func() error { t.Error("preparation dispatched application"); return nil }})
	defineFacadeCommand(t, interp, events, "/def tests /shell check-tests")
	result := make(chan error, 1)
	if err := interp.fallback.Schedule(func() {
		for _, raw := range []string{"/tests", "/inspect"} {
			parsed, err := promptlang.Parse(raw)
			if err != nil {
				result <- err
				return
			}
			command := interp.selectCommand(parsed)
			if command == nil {
				result <- fmt.Errorf("no command selected for %s", raw)
				return
			}
			invocation, err := command.Prepare(parsed, runtime.Context{Shell: resolutionShell{}})
			if err != nil {
				result <- err
				return
			}
			invocation.Close()
		}
		result <- nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resolved preparation did not finish")
	}
}
