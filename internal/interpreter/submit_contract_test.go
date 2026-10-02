package interpreter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/session"
)

type submitContractSession struct {
	mu         sync.Mutex
	observer   session.Observer
	roster     []participant.View
	barrier    []participant.Participant
	routable   []participant.Participant
	executed   chan session.Command
	executeErr error
	execute    func(session.Command, session.Observer)
	active     atomic.Int32
	maxActive  atomic.Int32
	shutdowns  atomic.Int32
}

func newSubmitContractSession() *submitContractSession {
	return &submitContractSession{executed: make(chan session.Command, 32)}
}

func (s *submitContractSession) Execute(command session.Command) error {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maxActive.Load()
		if active <= maximum || s.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	s.mu.Lock()
	observer := s.observer
	execute := s.execute
	err := s.executeErr
	s.mu.Unlock()
	if execute != nil {
		execute(command, observer)
	}
	s.executed <- command
	return err
}

func (s *submitContractSession) AddObserver(observer session.Observer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = observer
}

func (*submitContractSession) PlanSharedSend(string) session.SharedSendPlan {
	return session.SharedSendPlan{}
}

func (s *submitContractSession) Roster() []participant.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]participant.View(nil), s.roster...)
}

func (*submitContractSession) Participant(string) (participant.Participant, bool) {
	return participant.Participant{}, false
}

func (s *submitContractSession) RoutableParticipants() []participant.Participant {
	return append([]participant.Participant(nil), s.routable...)
}
func (s *submitContractSession) BarrierParticipants() []participant.Participant {
	return append([]participant.Participant(nil), s.barrier...)
}
func (s *submitContractSession) Shutdown() { s.shutdowns.Add(1) }

type submitContractObserver struct{ events chan Event }

// Semantic contract tests consume the semantic stream; transcript transport has dedicated coverage.
func (o submitContractObserver) OnEvent(event Event) {
	if _, transcript := event.(TranscriptChanged); !transcript {
		o.events <- event
	}
}

type blockingSubmitContractOperation struct {
	entered chan struct{}
	release chan struct{}
}

func (op blockingSubmitContractOperation) apply(*interpreterExecutor) {
	close(op.entered)
	<-op.release
}

func TestSubmitContract_rejectsInvalidArgumentsWithoutMutation(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/invite"))
	rejected := receiveSubmitEvent[InputRejected](t, events)
	if rejected.Raw != "/invite" || rejected.Err == nil {
		t.Fatalf("rejected = %#v", rejected)
	}
	if records := interp.Snapshot().Room.Records; len(records) != 0 {
		t.Fatalf("records = %#v, want none", records)
	}
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_reportsUndefinedCommandWithoutMutation(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/not-defined"))
	unknown := receiveSubmitEvent[UnknownCommand](t, events)
	if unknown.Raw != "/not-defined" || unknown.Name != "not-defined" {
		t.Fatalf("unknown = %#v", unknown)
	}
	if records := interp.Snapshot().Room.Records; len(records) != 0 {
		t.Fatalf("records = %#v, want none", records)
	}
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_executesNativeCommandAndAcceptsInputOnce(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	command := session.CancelCommand{Alias: "ada"}

	mustSubmit(t, interp.Submit("/cancel ada"))
	accepted := receiveSubmitEvent[InputAccepted](t, events)
	if accepted.Raw != "/cancel ada" {
		t.Fatalf("accepted = %#v", accepted)
	}
	if got := receiveSubmitCommand(t, sess.executed); got != command {
		t.Fatalf("command = %#v, want %#v", got, command)
	}
	changed := receiveSubmitEvent[StateChanged](t, events)
	if len(changed.Snapshot.Room.Records) != 1 || changed.Snapshot.Room.Records[0].Text != "/cancel ada" {
		t.Fatalf("records = %#v", changed.Snapshot.Room.Records)
	}
	succeeded := receiveSubmitEvent[SubmissionSucceeded](t, events)
	if succeeded.Raw != "/cancel ada" {
		t.Fatalf("success = %#v", succeeded)
	}
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_reportsNativeExecutionFailure(t *testing.T) {
	wantErr := errors.New("execute failed")
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.executeErr = wantErr

	mustSubmit(t, interp.Submit("/cancel ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[StateChanged](t, events)
	failed := receiveSubmitEvent[SubmissionFailed](t, events)
	if !errors.Is(failed.Err, wantErr) {
		t.Fatalf("failure = %v, want wrapped execution error", failed.Err)
	}
	if failed.Raw != "/cancel ada" || failed.Operation != "cancel" {
		t.Fatalf("failure = %#v", failed)
	}
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_terminalOutcomeFollowsCausalEvents(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.execute = func(_ session.Command, observer session.Observer) {
		observer.OnEvent(session.AgentStarted{Alias: "ada"})
	}

	mustSubmit(t, interp.Submit("/cancel ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	causal := receiveSubmitEvent[StateChanged](t, events)
	if len(causal.Snapshot.Room.Members) != 1 || causal.Snapshot.Room.Members[0] != "ada" {
		t.Fatalf("causal members = %v, want [ada]", causal.Snapshot.Room.Members)
	}
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_serializesNativeExecutions(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	sess.execute = func(session.Command, session.Observer) {
		entered <- struct{}{}
		<-release
	}

	mustSubmit(t, interp.Submit("/cancel ada"))
	receiveSignal(t, entered, "first Execute")
	mustSubmit(t, interp.Submit("/cancel bob"))
	assertNoSignal(t, entered, "second Execute entered before first completed")
	release <- struct{}{}
	receiveSignal(t, entered, "second Execute")
	release <- struct{}{}
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitCommand(t, sess.executed)
	if maximum := sess.maxActive.Load(); maximum != 1 {
		t.Fatalf("maximum concurrent Execute calls = %d, want 1", maximum)
	}
}

func TestSubmitContract_submitReturnsBeforeExecutionCompletes(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	sess.execute = func(session.Command, session.Observer) {
		close(entered)
		<-release
	}

	returned := make(chan error, 1)
	go func() {
		returned <- interp.Submit("/cancel ada")
	}()
	mustSubmit(t, receiveSubmitResult(t, returned))
	receiveSignal(t, entered, "native Execute")
	close(release)
	receiveSubmitCommand(t, sess.executed)
}

func TestSubmitContract_serializesConcurrentValidSubmissions(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)

	const submissions = 20
	var submitted sync.WaitGroup
	results := make(chan error, submissions)
	submitted.Add(submissions)
	for range submissions {
		go func() {
			defer submitted.Done()
			results <- interp.Submit("/cancel ada")
		}()
	}
	submitted.Wait()
	close(results)
	for err := range results {
		mustSubmit(t, err)
	}
	for range submissions {
		receiveSubmitCommand(t, sess.executed)
	}
	if maximum := sess.maxActive.Load(); maximum != 1 {
		t.Fatalf("maximum concurrent Execute calls = %d, want 1", maximum)
	}
}

func TestSubmitContract_appliesCausalEventBeforeNextExecution(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	var calls int
	causalStateObserved := make(chan bool, 1)
	sess.execute = func(_ session.Command, observer session.Observer) {
		calls++
		if calls == 1 {
			observer.OnEvent(session.AgentStarted{Alias: "ada"})
			return
		}
		members := interpreterModelOf(interp).Snapshot().room.Members
		causalStateObserved <- len(members) == 1 && members[0] == "ada"
	}

	mustSubmit(t, interp.Submit("/cancel ada"))
	mustSubmit(t, interp.Submit("/cancel bob"))
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitCommand(t, sess.executed)
	select {
	case observed := <-causalStateObserved:
		if !observed {
			t.Fatal("second execution did not observe the first execution's causal event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for causal-state observation")
	}
}

func TestSubmitContract_coalescesSessionEventWakeups(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	interp.executor.enqueue(blockingSubmitContractOperation{entered: entered, release: release})
	receiveSignal(t, entered, "blocking operation")

	sess.mu.Lock()
	observer := sess.observer
	sess.mu.Unlock()
	const eventCount = 1000
	for id := range eventCount {
		observer.OnEvent(session.ApprovalCleared{ID: int64(id + 1)})
	}
	mustSubmit(t, interp.Submit("/cancel ada"))
	close(release)
	receiveSubmitCommand(t, sess.executed)

	interp.executor.inbox.mu.Lock()
	pending := interp.executor.inbox.drainPending
	remaining := len(interp.executor.inbox.events)
	interp.executor.inbox.mu.Unlock()
	if pending || remaining != 0 {
		t.Fatalf("session event inbox: pending=%v remaining=%d", pending, remaining)
	}
}

type setStagePendingOperation struct {
	pending bool
	done    chan struct{}
}

func (op setStagePendingOperation) apply(e *interpreterExecutor) {
	stage := &e.model.(*interpreterModel).workflows.stage
	if op.pending {
		stage.active = &stageState{generation: 1, raw: "pending"}
	} else {
		stage.active = nil
	}
	close(op.done)
}

type readStagePendingOperation struct{ result chan bool }

func (op readStagePendingOperation) apply(e *interpreterExecutor) {
	op.result <- e.model.(*interpreterModel).workflows.stage.pending()
}

func TestSubmitContract_rejectsSubmissionWhileStagePending(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	setDone := make(chan struct{})
	if !interp.executor.enqueue(setStagePendingOperation{pending: true, done: setDone}) {
		t.Fatal("enqueue stage setup")
	}
	receiveSignal(t, setDone, "stage setup")

	mustSubmit(t, interp.Submit("/cancel ada"))
	rejected := receiveSubmitEvent[InputRejected](t, events)
	if !errors.Is(rejected.Err, ErrStagePending) {
		t.Fatalf("rejection = %v, want ErrStagePending", rejected.Err)
	}
	pending := make(chan bool, 1)
	if !interp.executor.enqueue(readStagePendingOperation{result: pending}) {
		t.Fatal("enqueue stage inspection")
	}
	if !<-pending {
		t.Fatal("pending stage was modified")
	}
	if records := interp.Snapshot().Room.Records; len(records) != 0 {
		t.Fatalf("records = %#v, want none", records)
	}
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_ignoresSubmissionAfterShutdown(t *testing.T) {
	sess := newSubmitContractSession()
	events := make(chan Event, 1)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(submitContractObserver{events: events}))
	receiveSubmitEvent[StateChanged](t, events)
	interp.Close()

	if err := interp.Submit("/cancel ada"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit error = %v, want ErrClosed", err)
	}
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}

func newSubmitContractInterpreter(t *testing.T) (*Interpreter, *submitContractSession, chan Event) {
	t.Helper()
	sess := newSubmitContractSession()
	events := make(chan Event, 64)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(submitContractObserver{events: events}))
	receiveSubmitEvent[StateChanged](t, events)
	t.Cleanup(interp.Close)
	return interp, sess, events
}

func mustSubmit(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func receiveSubmitResult(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for submission result")
		return nil
	}
}

func receiveSubmitEvent[T Event](t *testing.T, events <-chan Event) T {
	t.Helper()
	select {
	case event := <-events:
		if _, transcript := event.(TranscriptChanged); transcript {
			return receiveSubmitEvent[T](t, events)
		}
		value, ok := event.(T)
		if !ok {
			t.Fatalf("event type = %T, want %T", event, *new(T))
		}
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %T", *new(T))
		var zero T
		return zero
	}
}

func receiveSubmitCommand(t *testing.T, commands <-chan session.Command) session.Command {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session command")
		return nil
	}
}

func assertNoSubmitExecution(t *testing.T, commands <-chan session.Command) {
	t.Helper()
	select {
	case command := <-commands:
		t.Fatalf("unexpected session command: %#v", command)
	case <-time.After(20 * time.Millisecond):
	}
}

func assertNoSubmitEvent(t *testing.T, events <-chan Event) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected event: %#v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func receiveSignal(t *testing.T, signals <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertNoSignal(t *testing.T, signals <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signals:
		t.Fatal(description)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestSubmitContract_shutdownWaitsForAcceptedExecution(t *testing.T) {
	events := make(chan Event, 3)
	interp, sess := newSubmitContractInterpreterWithoutCleanup(t, WithObserver(submitContractObserver{events: events}))
	receiveSubmitEvent[StateChanged](t, events)
	entered := make(chan struct{})
	release := make(chan struct{})
	sess.execute = func(session.Command, session.Observer) {
		close(entered)
		<-release
	}

	mustSubmit(t, interp.Submit("/cancel ada"))
	receiveSignal(t, entered, "accepted execution")
	closed := make(chan struct{})
	go func() {
		interp.Close()
		close(closed)
	}()
	assertNoSignal(t, closed, "Close returned while accepted execution was active")
	close(release)
	receiveSignal(t, closed, "Close")

	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitEvent(t, events)
}
