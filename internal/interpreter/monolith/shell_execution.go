package monolith

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

func (e *interpreterExecutor) setShellRunner(runner ShellRunner) {
	e.runShell = runner
}

// The adapter retains the legacy event's result before the command completes.
// Both writes and callback reads happen on the same owned worker.
type userShellLauncher struct {
	executor *interpreterExecutor
	result   *shell.Result
}

func (l *userShellLauncher) Cwd() string { return l.executor.cwd }

func (l *userShellLauncher) Go(program string, complete func(shell.Result)) error {
	e := l.executor
	e.shellWG.Add(1)
	go func() {
		defer e.shellWG.Done()
		result := e.runShell.Run(e.lifetime, e.cwd, program)
		l.result = &result
		complete(result)
	}()
	return nil
}

// completionEvent preserves the shell compatibility event without command routing.
// Called by the command callback after the owned worker has set its result.
func (l *userShellLauncher) completionEvent(outcome submissionOutcome, records []room.Record) Event {
	for _, record := range records {
		if record.Msg == nil {
			continue
		}
		command, ok := record.Msg.Content.(agent.Command)
		if !ok || record.Alias.Name != shellRecordAlias {
			continue
		}
		if l.result == nil {
			return nil
		}
		return eventWithSubmissionSource(ShellCompleted{
			Raw: outcome.raw, Command: command.Command, Cwd: l.executor.cwd,
			Result: *l.result, Output: formatShellResult(*l.result),
		}, outcome.statement)
	}
	// A parent may have reported child shell results while remaining active.
	// Final output without a new shell record must not replay the cached result.
	return nil
}
