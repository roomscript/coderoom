package std_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/api"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

type loopScope struct {
	handle  func(runtime.ExecutionEvent)
	command session.Command
	closed  bool
}

func (s *loopScope) Subscribe(handle func(runtime.ExecutionEvent)) func() {
	s.handle = handle
	return func() { s.closed = true }
}
func (s *loopScope) Execute(command session.Command) error { s.command = command; return nil }

type loopHost struct {
	*sendDelivery
	scopes       []*loopScope
	listener     func(session.Event)
	unsubscribed bool
}

func (h *loopHost) CreateExecution() runtime.SessionExecution {
	scope := &loopScope{}
	h.scopes = append(h.scopes, scope)
	return scope
}
func (h *loopHost) SubscribeParticipants(handle func(session.Event)) (session.ParticipantSnapshot, func()) {
	h.listener = handle
	return session.ParticipantSnapshot{Participants: h.views}, func() { h.unsubscribed = true }
}

type loopShell struct {
	callbacks []func(shell.Result)
	programs  []string
}

func (*loopShell) Cwd() string { return "/work" }
func (s *loopShell) Go(program string, complete func(shell.Result)) error {
	s.programs = append(s.programs, program)
	s.callbacks = append(s.callbacks, complete)
	return nil
}

type loopPublication struct {
	records []room.Record
	events  []api.Event
}

func (p *loopPublication) Publish(records []room.Record, events ...api.Event) {
	p.records = append(p.records, records...)
	p.events = append(p.events, events...)
}

type loopCompletionHistory map[participant.Alias]participant.ExecutionID

func (h loopCompletionHistory) LastCompletedExecutionID(alias participant.Alias) participant.ExecutionID {
	return h[alias]
}

type loopRequest struct{ calls int }

func (r *loopRequest) RequestEventProcessing() { r.calls++ }

type loopProof struct {
	invocation  runtime.Invocation
	handler     runtime.InvocationEventHandler
	host        *loopHost
	shell       *loopShell
	publication *loopPublication
	history     loopCompletionHistory
	request     *loopRequest
	primary     participant.Alias
	results     []runtime.CommandResult
}

func newLoopProof(t *testing.T, maxTurns string) *loopProof {
	t.Helper()
	delivery := &sendDelivery{}
	initializeSendPlanner(t, delivery)
	host := &loopHost{sendDelivery: delivery}
	proof := &loopProof{host: host, shell: &loopShell{}, publication: &loopPublication{}, history: loopCompletionHistory{}, request: &loopRequest{}, primary: delivery.views[0].Alias}
	registry := promptlang.NewRegistry()
	definition := parseSendStatement(t, "/def tests /shell check-tests").Value.(promptlang.UserDefinition)
	if err := registry.Define(definition); err != nil {
		t.Fatal(err)
	}
	commands := &countingDefinitions{Registry: registry}
	ctx := runtime.Context{Session: host, Shell: proof.shell, NamedCommands: commands, EventProcessing: proof.request, Publication: proof.publication, CompletedExecutions: proof.history}
	inv, err := (std.LoopCommand{}).Prepare(parseSendStatement(t, "/loop @ada work /until /tests /max "+maxTurns), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if commands.lookups != 1 {
		t.Fatalf("loop condition lookups=%d", commands.lookups)
	}
	proof.invocation, proof.handler = inv, inv.(runtime.InvocationEventHandler)
	t.Cleanup(inv.Close)
	if inv.Staging() != nil {
		t.Fatal("loop unexpectedly stages")
	}
	if err := inv.Go(func(result runtime.CommandResult) { proof.results = append(proof.results, result) }); err != nil {
		t.Fatal(err)
	}
	return proof
}

func (p *loopProof) deliver(id session.ExecutionID, status session.DeliveryStatus) {
	scope := p.host.scopes[len(p.host.scopes)-1]
	scope.handle(runtime.ExecutionCompleted{ExecutionID: id, Result: session.RoutingResult{Kind: session.RoutingParticipantSend, Recipients: []session.RecipientResult{{Alias: p.primary, Role: session.RecipientPrimary, Status: status}}}})
	p.handler.HandlePendingEvents()
}
func (p *loopProof) completeWork(id session.ExecutionID, recorded bool) {
	p.host.listener(session.ParticipantWorkCompleted{Alias: p.primary, ExecutionID: id})
	if recorded {
		p.history[p.primary] = id
	}
	p.handler.HandlePendingEvents()
}

func TestLoop_waitsForOwnRecordedCompletion(t *testing.T) {
	p := newLoopProof(t, "2")
	// Completion can arrive before the delivery outcome and before room history.
	p.completeWork(41, false)
	p.deliver(41, session.DeliveryDelivered)
	if !p.invocation.(runtime.SubmissionAcknowledgement).ReadyToAcknowledge() {
		t.Fatal("first delivery did not reach acknowledgement")
	}
	p.host.listener(session.ParticipantStatusChanged{Alias: p.primary, To: participant.StatusIdle})
	p.completeWork(42, false)
	if len(p.shell.programs) != 0 {
		t.Fatal("unrelated completion or unrecorded output advanced loop")
	}
	p.history[p.primary] = 41
	p.handler.HandlePendingEvents()
	if len(p.shell.programs) != 1 {
		t.Fatal("recorded own completion did not start condition")
	}
	p.shell.callbacks[0](shell.Result{Status: shell.StatusFailure, Stdout: "evidence"})
	if len(p.host.scopes) != 1 {
		t.Fatal("callback advanced workflow outside interpreter")
	}
	p.handler.HandlePendingEvents()
	assertLoopRetry(t, p)
	p.deliver(43, session.DeliveryDelivered)
	p.completeWork(43, true)
	p.shell.callbacks[0](shell.Result{Status: shell.StatusSuccess})
	p.handler.HandlePendingEvents()
	if len(p.results) != 0 {
		t.Fatal("stale condition completed a later iteration")
	}
	p.shell.callbacks[1](shell.Result{Status: shell.StatusFailure})
	p.handler.HandlePendingEvents()
	if len(p.results) != 1 || len(p.host.scopes) != 2 {
		t.Fatal("loop did not stop at bound")
	}
}

func TestLoop_completionAndDeparture(t *testing.T) {
	for _, status := range []shell.Status{shell.StatusSuccess, shell.StatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			p := newLoopProof(t, "2")
			p.deliver(41, session.DeliveryDelivered)
			p.completeWork(41, true)
			p.shell.callbacks[0](shell.Result{Status: status})
			p.handler.HandlePendingEvents()
			if len(p.results) != 1 || p.results[0].Err != nil || len(p.host.scopes) != 1 {
				t.Fatal("condition did not finish loop")
			}
		})
	}
	t.Run("departure ignores late condition", func(t *testing.T) {
		p := newLoopProof(t, "2")
		p.deliver(41, session.DeliveryDelivered)
		p.completeWork(41, true)
		p.host.listener(session.AgentStopped{Alias: p.primary})
		p.handler.HandlePendingEvents()
		if len(p.results) != 1 {
			t.Fatal("departure did not stop loop")
		}
		p.invocation.Close()
		count := len(p.publication.records)
		p.shell.callbacks[0](shell.Result{Status: shell.StatusFailure})
		p.handler.HandlePendingEvents()
		if len(p.publication.records) != count || len(p.host.scopes) != 1 || !p.host.unsubscribed {
			t.Fatal("late condition revived or published stopped work")
		}
	})
}

func TestLoop_abortedWorkAndReplacement(t *testing.T) {
	p := newLoopProof(t, "1")
	p.deliver(41, session.DeliveryDelivered)
	replacement := participant.Alias{Name: p.primary.Name}
	p.host.listener(session.ParticipantWorkCompleted{Alias: replacement, ExecutionID: 41})
	p.handler.HandlePendingEvents()
	if len(p.shell.programs) != 0 {
		t.Fatal("replacement completed original work")
	}
	p.host.listener(session.ParticipantStatusChanged{Alias: p.primary, To: participant.StatusWorking, ActiveExecutionID: 41})
	p.host.listener(session.ParticipantStatusChanged{Alias: p.primary, From: participant.StatusWorking, To: participant.StatusIdle})
	p.handler.HandlePendingEvents()
	if len(p.shell.programs) != 1 {
		t.Fatal("rollback of own work did not evaluate condition")
	}
}

func TestLoop_failedDeliveryAndProtocol(t *testing.T) {
	t.Run("session rejection retains cause", func(t *testing.T) {
		p := newLoopProof(t, "1")
		failure := participant.ErrWorkAlreadyStarted
		p.host.scopes[0].handle(runtime.ExecutionCompleted{
			ExecutionID: 41, Err: failure,
			Result: session.RoutingResult{Kind: session.RoutingParticipantSend, Err: failure,
				Recipients: []session.RecipientResult{{Alias: p.primary, Role: session.RecipientPrimary, Status: session.DeliveryFailed, Err: failure}}},
		})
		p.handler.HandlePendingEvents()
		if len(p.results) != 1 || !errors.Is(p.results[0].Err, failure) || len(p.shell.programs) != 0 {
			t.Fatalf("loop lost rejection or ran condition: %+v", p.results)
		}
	})
	t.Run("primary failure", func(t *testing.T) {
		p := newLoopProof(t, "1")
		p.deliver(41, session.DeliveryFailed)
		if len(p.results) != 1 || p.results[0].Err != nil || len(p.shell.programs) != 0 {
			t.Fatal("failed primary delivery did not stop loop")
		}
	})
	t.Run("protocol failure", func(t *testing.T) {
		p := newLoopProof(t, "1")
		failure := errors.New("execution failed")
		p.host.scopes[0].handle(runtime.ExecutionFailed{Err: failure})
		p.handler.HandlePendingEvents()
		if len(p.results) != 1 || !errors.Is(p.results[0].Err, failure) {
			t.Fatal("protocol failure did not terminate loop")
		}
	})
}

func TestLoop_departureDuringDispatch(t *testing.T) {
	p := newLoopProof(t, "1")
	p.host.listener(session.ParticipantStatusChanged{Alias: p.primary, To: participant.StatusCrashed})
	p.handler.HandlePendingEvents()
	if len(p.results) != 0 {
		t.Fatal("departure bypassed delivery outcome")
	}
	p.deliver(41, session.DeliveryDelivered)
	if len(p.results) != 1 || len(p.shell.programs) != 0 {
		t.Fatal("dispatch departure did not stop loop")
	}
}

func TestLoop_noticeFailureStillWaitsForPrimary(t *testing.T) {
	p := newLoopProof(t, "1")
	failure := errors.New("notice failed")
	p.host.scopes[0].handle(runtime.ExecutionCompleted{ExecutionID: 41, Result: session.RoutingResult{
		Kind: session.RoutingParticipantSend, Err: failure, Recipients: []session.RecipientResult{
			{Alias: p.primary, Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
			{Alias: p.host.views[1].Alias, Role: session.RecipientNotice, Status: session.DeliveryFailed, Err: failure},
		},
	}})
	p.handler.HandlePendingEvents()
	if len(p.results) != 0 {
		t.Fatal("notice failure stopped delivered primary work")
	}
	p.completeWork(41, true)
	if len(p.shell.programs) != 1 {
		t.Fatal("delivered primary did not advance")
	}
}

func assertLoopRetry(t *testing.T, p *loopProof) {
	t.Helper()
	if len(p.host.scopes) != 2 || !p.host.scopes[0].closed {
		t.Fatal("iteration did not use a fresh scope")
	}
	command := p.host.scopes[1].command.(session.SendToParticipantCommand)
	if !strings.Contains(command.Message, "evidence") {
		t.Fatal("retry prompt lost condition evidence")
	}
}

func TestLoop_crashStopsEachPhase(t *testing.T) {
	for _, phase := range []string{"dispatch", "waiting", "condition"} {
		t.Run(phase, func(t *testing.T) {
			p := newLoopProof(t, "2")
			if phase != "dispatch" {
				p.deliver(41, session.DeliveryDelivered)
			}
			if phase == "condition" {
				p.completeWork(41, true)
			}
			p.host.listener(session.ParticipantStatusChanged{Alias: p.primary, To: participant.StatusCrashed})
			p.handler.HandlePendingEvents()
			if phase == "dispatch" {
				if len(p.results) != 0 {
					t.Fatal("crash bypassed delivery settlement")
				}
				p.deliver(41, session.DeliveryDelivered)
			}
			if len(p.results) != 1 || p.results[0].Err != nil {
				t.Fatal("crash did not stop loop")
			}
			last := p.publication.records[len(p.publication.records)-1]
			if last.Text != "[loop] stopped: participant @ada crashed" {
				t.Fatalf("last record=%+v", last)
			}
			p.invocation.Close()
			assertLoopCrashStaysStopped(t, p)
		})
	}
}

func assertLoopCrashStaysStopped(t *testing.T, p *loopProof) {
	t.Helper()
	count := len(p.publication.records)
	for _, callback := range p.shell.callbacks {
		callback(shell.Result{Status: shell.StatusFailure})
	}
	p.handler.HandlePendingEvents()
	if len(p.results) != 1 || len(p.publication.records) != count || len(p.host.scopes) != 1 || !p.host.unsubscribed {
		t.Fatal("crashed loop retained resources or accepted late output")
	}
}
