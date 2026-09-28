package interpreter

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/shell"
)

type sequenceShellRunner struct {
	mu      sync.Mutex
	results []shell.Result
	calls   int
}

type causalBurstProbeOperation struct{ done chan struct{} }

func (op causalBurstProbeOperation) apply(i *Interpreter) {
	i.workflows.loop.active = &loopState{
		generation: 1,
		statement:  testLoopStatement(1),
		body:       promptlang.Shell{Program: "probe"},
		phase:      loopWaitingForParticipant,
	}
	i.applyEffectBatch(effectBatch{effects: []effect{executeSessionEffect{
		target: workflowRef{kind: workflowLoop, generation: 99, requestID: 99},
		request: planAndExecuteSharedSendRequest{
			alias: "ada", directText: "work", listenersText: "@ada: work",
		},
	}}})
	close(op.done)
}

func (r *sequenceShellRunner) Run(context.Context, string, string) shell.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := r.results[r.calls]
	r.calls++
	return result
}

func TestSubmitContract_loopAlternatesTurnsAndConditionsUntilBound(t *testing.T) {
	exitCode := 1
	runner := &sequenceShellRunner{results: []shell.Result{
		{Status: shell.StatusFailure, ExitCode: &exitCode, Stdout: "first failure", Stderr: "details", Err: errors.New("runner problem")},
		{Status: shell.StatusFailure, ExitCode: &exitCode, Stdout: "second failure"},
	}}
	interp, sess, events := newLoopTestInterpreter(t, runner)
	defineLoopCondition(t, interp, events)

	mustSubmit(t, interp.Submit("/loop @ada make the tests pass /until /tests /max 2"))
	receiveSubmitEvent[InputAccepted](t, events)
	first := receiveSubmitCommand(t, sess.executed).(session.SharedSendCommand)
	if first.TextDirect != "make the tests pass" {
		t.Fatalf("first prompt = %q", first.TextDirect)
	}
	assertLoopStatus(t, events, "[loop] turn 1/2 sent to @ada")
	receiveSubmitEvent[SubmissionSucceeded](t, events)

	completeInterpreterLoopTurn(t, interp, events)
	completed := receiveSubmitEvent[ShellCompleted](t, events)
	if completed.Command != "/tests" || !strings.Contains(completed.Output, "stdout:\nfirst failure") {
		t.Fatalf("first condition = %#v", completed)
	}
	second := receiveSubmitCommand(t, sess.executed).(session.SharedSendCommand)
	assertLoopEvidence(t, second.TextDirect, "first failure")
	assertLoopStatus(t, events, "[loop] turn 2/2 sent to @ada")
	receiveSubmitEvent[StateChanged](t, events)

	completeInterpreterLoopTurn(t, interp, events)
	receiveSubmitEvent[ShellCompleted](t, events)
	assertLoopStatus(t, events, "[loop] reached /max 2; condition /tests still failing")
	receiveSubmitEvent[StateChanged](t, events)
	if interp.workflows.loop.active != nil {
		t.Fatal("bounded loop remained active")
	}
}

func TestEffectExecutor_projectsCompleteCausalBurstBeforeDerivedEffect(t *testing.T) {
	sess := newSubmitContractSession()
	sess.execute = func(_ session.Command, observer session.Observer) {
		observer.OnEvent(session.ParticipantStatusChanged{Alias: "ada", To: participant.StatusIdle})
		observer.OnEvent(session.AgentStarted{Alias: "turing"})
	}
	projected := make(chan bool, 1)
	var interp *Interpreter
	runner := ShellRunnerFunc(func(context.Context, string, string) shell.Result {
		projected <- slices.Contains(interp.room.Snapshot().Members, "turing")
		return shell.Result{Status: shell.StatusCancelled}
	})
	interp = New(t.Context(), sess, "/workspace", WithShellRunner(runner))
	t.Cleanup(interp.Close)

	done := make(chan struct{})
	if !interp.enqueue(causalBurstProbeOperation{done: done}) {
		t.Fatal("enqueue causal burst probe")
	}
	select {
	case visible := <-projected:
		if !visible {
			t.Fatal("event-derived effect ran before later event was projected")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event-derived effect")
	}
	<-done
}

func TestSubmitContract_loopStopsWhenParticipantStopsOrCrashes(t *testing.T) {
	tests := []struct {
		name    string
		event   session.Event
		message string
	}{
		{"stopped", session.AgentStopped{Alias: "ada"}, "[loop] stopped: participant @ada stopped"},
		{"crashed", session.AgentCrashed{Alias: "ada"}, "[loop] stopped: participant @ada crashed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interp, sess, events := newLoopTestInterpreter(t, &sequenceShellRunner{})
			defineLoopCondition(t, interp, events)
			mustSubmit(t, interp.Submit("/loop @ada work /until /tests /max 1"))
			receiveSubmitEvent[InputAccepted](t, events)
			receiveSubmitCommand(t, sess.executed)
			receiveSubmitEvent[LoopStatus](t, events)
			receiveSubmitEvent[SubmissionSucceeded](t, events)

			interp.recordSessionEvent(tt.event)
			assertLoopStatus(t, events, tt.message)
			receiveSubmitEvent[StateChanged](t, events)
			if interp.workflows.loop.active != nil {
				t.Fatal("loop remained active after participant departure")
			}
		})
	}
}

func TestSubmitContract_loopRetainsTerminalEventDuringDispatch(t *testing.T) {
	interp, sess, events := newLoopTestInterpreter(t, &sequenceShellRunner{})
	defineLoopCondition(t, interp, events)
	sess.execute = func(_ session.Command, observer session.Observer) {
		observer.OnEvent(session.AgentCrashed{Alias: "ada"})
	}

	mustSubmit(t, interp.Submit("/loop @ada work /until /tests /max 1"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	assertLoopStatus(t, events, "[loop] stopped: participant @ada crashed")
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	if interp.workflows.loop.active != nil {
		t.Fatal("loop remained active after dispatch-time crash")
	}
}

func TestSubmitContract_loopChecksActiveBeforeResolvingCondition(t *testing.T) {
	interp, sess, events := newLoopTestInterpreter(t, &sequenceShellRunner{})
	defineLoopCondition(t, interp, events)
	mustSubmit(t, interp.Submit("/loop @ada work /until /tests /max 1"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[LoopStatus](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)

	mustSubmit(t, interp.Submit("/loop @ada other /until /undefined /max 1"))
	receiveSubmitEvent[InputAccepted](t, events)
	failed := receiveSubmitEvent[SubmissionFailed](t, events)
	if !errors.Is(failed.Err, errLoopAlreadyActive) {
		t.Fatalf("failure = %v, want errLoopAlreadyActive", failed.Err)
	}
}

func TestSubmitContract_failedLoopDispatchIgnoresSynchronousIdle(t *testing.T) {
	interp, sess, events := newLoopTestInterpreter(t, &sequenceShellRunner{})
	defineLoopCondition(t, interp, events)
	sess.executeErr = errors.New("send failed")
	sess.execute = func(_ session.Command, observer session.Observer) {
		observer.OnEvent(session.ParticipantStatusChanged{Alias: "ada", To: participant.StatusIdle})
	}

	mustSubmit(t, interp.Submit("/loop @ada work /until /tests /max 1"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	assertLoopStatus(t, events, "[loop] stopped: participant turn could not start")
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	if interp.workflows.loop.active != nil {
		t.Fatal("loop remained active after failed dispatch")
	}
}

func TestSubmitContract_loopFinishesForSuccessfulOrCancelledCondition(t *testing.T) {
	tests := []struct {
		name    string
		status  shell.Status
		message string
	}{
		{"success", shell.StatusSuccess, "[loop] condition /tests succeeded"},
		{"cancelled", shell.StatusCancelled, "[loop] condition /tests cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &sequenceShellRunner{results: []shell.Result{{Status: tt.status}}}
			interp, sess, events := newLoopTestInterpreter(t, runner)
			defineLoopCondition(t, interp, events)
			mustSubmit(t, interp.Submit("/loop @ada work /until /tests /max 2"))
			receiveSubmitEvent[InputAccepted](t, events)
			receiveSubmitCommand(t, sess.executed)
			receiveSubmitEvent[LoopStatus](t, events)
			receiveSubmitEvent[SubmissionSucceeded](t, events)

			completeInterpreterLoopTurn(t, interp, events)
			receiveSubmitEvent[ShellCompleted](t, events)
			assertLoopStatus(t, events, tt.message)
			receiveSubmitEvent[StateChanged](t, events)
			if interp.workflows.loop.active != nil {
				t.Fatal("completed loop remained active")
			}
		})
	}
}

func TestFormatLoopPrompt_includesConditionEvidence(t *testing.T) {
	got := formatLoopPrompt(testLoopStatement(1), shell.Result{Status: shell.StatusFailure})
	for _, want := range []string{
		"make the tests pass", "Condition command: /tests", "Status: failure",
		"Exit code: (none)", "Stdout:\n(none)", "Stderr:\n(none)", "Error:\n(none)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted prompt missing %q:\n%s", want, got)
		}
	}
}

func TestLoopWorkflow_consumesDispatchCompletionOnce(t *testing.T) {
	target := workflowRef{kind: workflowLoop, generation: 1, requestID: 1}
	workflow := loopWorkflow{active: &loopState{
		generation: 1,
		statement:  testLoopStatement(2),
		phase:      loopDispatchingParticipant,
		pending:    target,
	}}

	first := workflow.handleSessionCompletion(sessionCompletion{target: target})
	if len(first.effects) == 0 || workflow.active.phase != loopWaitingForParticipant {
		t.Fatalf("first completion did not advance dispatch: %#v", first)
	}
	second := workflow.handleSessionCompletion(sessionCompletion{target: target})
	if len(second.effects) != 0 || workflow.active.turns != 1 {
		t.Fatalf("duplicate completion advanced workflow: %#v", second)
	}
}

func TestLoopWorkflow_staleShellCompletionDoesNotAdvanceCurrentGeneration(t *testing.T) {
	current := workflowRef{kind: workflowLoop, generation: 2, requestID: 2}
	workflow := loopWorkflow{active: &loopState{
		generation: 2,
		statement:  testLoopStatement(2),
		phase:      loopEvaluating,
		pending:    current,
	}}
	stale := shellCompletion{
		target:  workflowRef{kind: workflowLoop, generation: 1, requestID: 1},
		request: shellRequest{command: "/tests"},
		result:  shell.Result{Status: shell.StatusSuccess},
	}

	batch := workflow.handleShellCompletion(stale)
	if len(batch.effects) != 2 {
		t.Fatalf("stale completion observation effects = %d, want 2", len(batch.effects))
	}
	if workflow.active == nil || workflow.active.pending != current {
		t.Fatal("stale completion advanced current generation")
	}
}

func newLoopTestInterpreter(t *testing.T, runner ShellRunner) (*Interpreter, *submitContractSession, chan Event) {
	t.Helper()
	sess := newSubmitContractSession()
	interp := New(t.Context(), sess, "/workspace", WithShellRunner(runner))
	events := make(chan Event, 32)
	interp.AddObserver(submitContractObserver{events: events})
	t.Cleanup(interp.Close)
	return interp, sess, events
}

func defineLoopCondition(t *testing.T, interp *Interpreter, events <-chan Event) {
	t.Helper()
	mustSubmit(t, interp.Submit("/def tests /shell go test ./..."))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
}

func completeInterpreterLoopTurn(t *testing.T, interp *Interpreter, events <-chan Event) {
	t.Helper()
	interp.recordSessionEvent(session.ParticipantStatusChanged{Alias: "ada", To: participant.StatusIdle})
	receiveSubmitEvent[StateChanged](t, events)
}

func assertLoopStatus(t *testing.T, events <-chan Event, want string) {
	t.Helper()
	status := receiveSubmitEvent[LoopStatus](t, events)
	if status.Message != want {
		t.Fatalf("loop status = %q, want %q", status.Message, want)
	}
}

func assertLoopEvidence(t *testing.T, prompt, stdout string) {
	t.Helper()
	for _, want := range []string{
		"make the tests pass", "Condition command: /tests", "Status: failure",
		"Exit code: 1", "Stdout:\n" + stdout, "Stderr:\ndetails", "Error:\nrunner problem",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("participant prompt missing %q:\n%s", want, prompt)
		}
	}
}

func testLoopStatement(maxTurns int) promptlang.Loop {
	return promptlang.Loop{Participant: "ada", Prompt: "make the tests pass", Condition: "tests", MaxTurns: maxTurns}
}
