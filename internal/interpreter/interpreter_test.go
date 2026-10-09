package interpreter_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

type recordingSession struct {
	mu           sync.Mutex
	observer     session.Observer
	participants []participant.View
	executed     chan session.Command
	executeErr   error
	executeHook  func(session.Command, session.Observer)
	active       atomic.Int32
	maxActive    atomic.Int32
	shutdowns    atomic.Int32
}

func newRecordingSession() *recordingSession {
	return &recordingSession{executed: make(chan session.Command, 64)}
}

func (s *recordingSession) Execute(command session.Command) error {
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
	hook := s.executeHook
	err := s.executeErr
	s.mu.Unlock()
	if hook != nil {
		hook(command, observer)
	} else if observer != nil {
		observer.OnEvent(session.ParticipantStatusChanged{Alias: "ada", To: participant.StatusIdle})
	}
	time.Sleep(time.Millisecond)
	if observer != nil {
		emitFakeRoutingOutcome(command, err, observer)
	}
	s.executed <- command
	return err
}

func (s *recordingSession) AddObserver(observer session.Observer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = observer
}

func (*recordingSession) CreateParticipantSendPlan(alias string) session.ParticipantSendPlan {
	planner := session.New()
	defer planner.Shutdown()
	return planner.CreateParticipantSendPlan(alias)
}

func (s *recordingSession) Participants() []participant.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]participant.View(nil), s.participants...)
}

func (s *recordingSession) Participant(alias string) (participant.View, bool) {
	for _, value := range s.Participants() {
		if value.Alias == alias {
			return value, true
		}
	}
	return participant.View{}, false
}

func (s *recordingSession) Shutdown() { s.shutdowns.Add(1) }

func (s *recordingSession) emit(event session.Event) {
	s.mu.Lock()
	observer := s.observer
	s.mu.Unlock()
	observer.OnEvent(event)
}

type eventObserver struct{ events chan interpreter.Event }

func (o eventObserver) OnEvent(event interpreter.Event) {
	if _, transcript := event.(interpreter.TranscriptChanged); !transcript {
		o.events <- event
	}
}

func TestInterpreter_projectsSessionEventBeforePublishingSnapshot(t *testing.T) {
	sess := newRecordingSession()
	sess.participants = []participant.View{{
		Alias:      "ada",
		Role:       "builder",
		Initiative: participant.InitiativeManual,
		Status:     participant.StatusIdle, StartupReady: true,
		Color: "#4ADE80",
	}}
	events := make(chan interpreter.Event, 1)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)

	sess.emit(session.AgentReady{Alias: "ada"})

	changed := receiveEvent[interpreter.StateChanged](t, events)
	if len(changed.Snapshot.Room.Members) != 1 || changed.Snapshot.Room.Members[0] != "ada" {
		t.Fatalf("room members = %v, want [ada]", changed.Snapshot.Room.Members)
	}
	if len(changed.Snapshot.Participants) != 1 || changed.Snapshot.Participants[0].Alias != "ada" {
		t.Fatalf("participants = %#v, want ada", changed.Snapshot.Participants)
	}
}

func TestInterpreter_translatesApprovalState(t *testing.T) {
	sess := newRecordingSession()
	events := make(chan interpreter.Event, 2)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)

	sess.emit(session.ApprovalRequested{
		Alias: "ada",
		ID:    42,
		Req: agent.ApprovalRequest{
			Kind:    agent.ApprovalCommandExecution,
			Ask:     "Run tests?",
			Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline},
		},
	})

	requested := receiveEvent[interpreter.StateChanged](t, events)
	approval := requested.Snapshot.Approval
	if approval == nil || approval.ID != 42 || approval.Alias != "ada" || approval.Prompt != "Run tests?" {
		t.Fatalf("approval = %#v", approval)
	}
	if len(approval.Options) != 2 || approval.Options[0].ID != "accept" {
		t.Fatalf("approval options = %#v", approval.Options)
	}

	sess.emit(session.ApprovalCleared{Alias: "ada", ID: 42})
	cleared := receiveEvent[interpreter.StateChanged](t, events)
	if cleared.Snapshot.Approval != nil {
		t.Fatalf("approval after clear = %#v", cleared.Snapshot.Approval)
	}
}

func TestInterpreter_snapshotAfterCloseUsesDetachedCache(t *testing.T) {
	sess := newRecordingSession()
	sess.participants = []participant.View{{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}}
	events := make(chan interpreter.Event, 2)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)

	sess.emit(session.AgentReady{Alias: "ada"})
	receiveEvent[interpreter.StateChanged](t, events)
	emitApproval(sess, 42, agent.OptionAccept)
	receiveEvent[interpreter.StateChanged](t, events)
	interp.Close()

	first := interp.Snapshot()
	first.Room.Members[0] = "changed"
	first.Participants[0].Alias = "changed"
	first.Approval.Options[0].ID = "changed"

	second := interp.Snapshot()
	if second.Room.Members[0] != "ada" {
		t.Fatalf("cached room members = %v, want [ada]", second.Room.Members)
	}
	if second.Participants[0].Alias != "ada" {
		t.Fatalf("cached participants = %#v, want ada", second.Participants)
	}
	if second.Approval == nil || second.Approval.Options[0].ID != "accept" {
		t.Fatalf("cached approval = %#v, want detached accept option", second.Approval)
	}
}

func TestInterpreter_resolvesOnlyOfferedChoiceAndClearsApproval(t *testing.T) {
	sess := newRecordingSession()
	events := make(chan interpreter.Event, 4)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)

	emitApproval(sess, 42, agent.OptionDecline, agent.OptionCancel)
	receiveEvent[interpreter.StateChanged](t, events)
	mustResolveApproval(t, interp, 42, "accept")
	receiveEvent[interpreter.OperationFailed](t, events)
	assertNotExecuted(t, sess.executed)

	mustResolveApproval(t, interp, 42, "decline")
	command := receiveCommand(t, sess.executed)
	resolved, ok := command.(session.ResolveApprovalCommand)
	if !ok || resolved.ApprovalID != 42 || resolved.Choice != agent.OptionDecline {
		t.Fatalf("command = %#v", command)
	}
	changed := receiveEvent[interpreter.StateChanged](t, events)
	if changed.Snapshot.Approval != nil || interp.Snapshot().Approval != nil {
		t.Fatalf("approval remained active after successful resolution")
	}
}

func TestInterpreter_rejectsResolutionForInactiveApproval(t *testing.T) {
	sess := newRecordingSession()
	events := make(chan interpreter.Event, 3)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)

	emitApproval(sess, 42, agent.OptionAccept)
	receiveEvent[interpreter.StateChanged](t, events)
	mustResolveApproval(t, interp, 41, "accept")
	receiveEvent[interpreter.OperationFailed](t, events)
	assertNotExecuted(t, sess.executed)
	if approval := interp.Snapshot().Approval; approval == nil || approval.ID != 42 {
		t.Fatalf("active approval = %#v, want ID 42", approval)
	}
}

func TestInterpreter_transitionsToNextApprovalAfterResolution(t *testing.T) {
	sess := newRecordingSession()
	sess.executeHook = func(_ session.Command, observer session.Observer) {
		observer.OnEvent(approvalRequested(43, agent.OptionCancel))
	}
	events := make(chan interpreter.Event, 4)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)

	emitApproval(sess, 42, agent.OptionAccept)
	receiveEvent[interpreter.StateChanged](t, events)
	mustResolveApproval(t, interp, 42, "accept")
	receiveCommand(t, sess.executed)
	cleared := receiveEvent[interpreter.StateChanged](t, events)
	if cleared.Snapshot.Approval != nil {
		t.Fatalf("approval during transition = %#v, want nil", cleared.Snapshot.Approval)
	}
	next := receiveEvent[interpreter.StateChanged](t, events)
	if next.Snapshot.Approval == nil || next.Snapshot.Approval.ID != 43 {
		t.Fatalf("next approval = %#v, want ID 43", next.Snapshot.Approval)
	}
}

func TestInterpreter_keepsApprovalWhenResolutionFails(t *testing.T) {
	sess := newRecordingSession()
	sess.executeErr = errors.New("execute failed")
	events := make(chan interpreter.Event, 3)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)

	emitApproval(sess, 42, agent.OptionAccept)
	receiveEvent[interpreter.StateChanged](t, events)
	mustResolveApproval(t, interp, 42, "accept")
	receiveCommand(t, sess.executed)
	receiveEvent[interpreter.OperationFailed](t, events)
	if approval := interp.Snapshot().Approval; approval == nil || approval.ID != 42 {
		t.Fatalf("active approval = %#v, want ID 42", approval)
	}
}

func TestInterpreter_consumesApprovalOnceUnderConcurrentResolution(t *testing.T) {
	sess := newRecordingSession()
	events := make(chan interpreter.Event, 32)
	interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(eventObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)
	emitApproval(sess, 1, agent.OptionAccept)
	receiveEvent[interpreter.StateChanged](t, events)

	const operations = 20
	var submitted sync.WaitGroup
	submitted.Add(operations)
	for range operations {
		go func() {
			defer submitted.Done()
			_ = interp.ResolveApproval(1, interpreter.ApprovalChoice{OptionID: "accept"})
		}()
	}
	submitted.Wait()
	receiveCommand(t, sess.executed)
	interp.Snapshot()
	assertNotExecuted(t, sess.executed)
}

func TestInterpreter_serializesConcurrentSessionExecuteCalls(t *testing.T) {
	sess := newRecordingSession()
	sess.executeErr = errors.New("execute failed")
	interp := interpreter.New(context.Background(), sess, t.TempDir())
	t.Cleanup(interp.Close)
	emitApproval(sess, 1, agent.OptionAccept)
	if approval := interp.Snapshot().Approval; approval == nil || approval.ID != 1 {
		t.Fatalf("active approval = %#v, want ID 1", approval)
	}

	const operations = 20
	for range operations {
		go func() {
			_ = interp.ResolveApproval(1, interpreter.ApprovalChoice{OptionID: "accept"})
		}()
	}
	for range operations {
		receiveCommand(t, sess.executed)
	}
	if maximum := sess.maxActive.Load(); maximum != 1 {
		t.Fatalf("maximum concurrent Execute calls = %d, want 1", maximum)
	}
}

func TestInterpreter_closeIsIdempotentAndRejectsOperations(t *testing.T) {
	sess := newRecordingSession()
	interp := interpreter.New(context.Background(), sess, t.TempDir())

	interp.Close()
	interp.Close()
	if err := interp.ResolveApproval(1, interpreter.ApprovalChoice{OptionID: "accept"}); !errors.Is(err, interpreter.ErrClosed) {
		t.Fatalf("ResolveApproval after Close = %v, want ErrClosed", err)
	}

	select {
	case <-sess.executed:
		t.Fatal("Execute called after Close")
	case <-time.After(20 * time.Millisecond):
	}
	if shutdowns := sess.shutdowns.Load(); shutdowns != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", shutdowns)
	}
}

func TestInterpreter_parentCancellationShutsDown(t *testing.T) {
	sess := newRecordingSession()
	ctx, cancel := context.WithCancel(context.Background())
	interp := interpreter.New(ctx, sess, t.TempDir())

	cancel()
	done := make(chan struct{})
	go func() {
		interp.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after parent cancellation")
	}
	if shutdowns := sess.shutdowns.Load(); shutdowns != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", shutdowns)
	}
}

func receiveEvent[T interpreter.Event](t *testing.T, events <-chan interpreter.Event) T {
	t.Helper()
	select {
	case event := <-events:
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

func emitApproval(sess *recordingSession, id int64, options ...agent.ApprovalOption) {
	sess.emit(approvalRequested(id, options...))
}

func mustResolveApproval(t *testing.T, interp *interpreter.Interpreter, id int64, optionID string) {
	t.Helper()
	if err := interp.ResolveApproval(id, interpreter.ApprovalChoice{OptionID: optionID}); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
}

func approvalRequested(id int64, options ...agent.ApprovalOption) session.ApprovalRequested {
	return session.ApprovalRequested{
		Alias: "ada",
		ID:    id,
		Req: agent.ApprovalRequest{
			Kind:    agent.ApprovalCommandExecution,
			Ask:     "Proceed?",
			Options: options,
		},
	}
}

func receiveCommand(t *testing.T, commands <-chan session.Command) session.Command {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Execute")
		return nil
	}
}

func assertNotExecuted(t *testing.T, commands <-chan session.Command) {
	t.Helper()
	select {
	case command := <-commands:
		t.Fatalf("unexpected Execute command: %#v", command)
	case <-time.After(20 * time.Millisecond):
	}
}

func emitFakeRoutingOutcome(command session.Command, err error, observer session.Observer) {
	result := session.RoutingResult{Err: err}
	var aliases []string
	role := session.RecipientPrimary
	switch command := command.(type) {
	case session.BroadcastCommand:
		result.Kind, aliases, role = session.RoutingBroadcast, command.Aliases, session.RecipientBroadcast
	case session.SendToParticipantCommand:
		result.Kind, aliases = session.RoutingParticipantSend, command.Plan.Targets()
	case session.HandoffCommand:
		result.Kind, aliases, role = session.RoutingHandoff, []string{command.ToAlias}, session.RecipientHandoff
	default:
		return
	}
	for _, alias := range aliases {
		status := session.DeliveryDelivered
		if err != nil {
			status = session.DeliveryFailed
		}
		result.Recipients = append(result.Recipients, session.RecipientResult{Alias: alias, Role: role, Status: status, Err: err})
	}
	observer.OnEvent(session.RoutingCompleted{Result: result})
}
