package runtime

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

type participantHost struct {
	mu         sync.Mutex
	handle     func(session.Event)
	views      []participant.View
	completed  map[participant.Alias]session.ExecutionID
	entered    chan struct{}
	release    chan struct{}
	beforeRead func()
}

func (h *participantHost) subscribeParticipants(handle func(session.Event)) func() {
	h.mu.Lock()
	h.handle = handle
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.handle = nil
		h.mu.Unlock()
	}
}

func (h *participantHost) Participants() []participant.View {
	if h.beforeRead != nil {
		h.beforeRead()
	}
	if h.entered != nil {
		close(h.entered)
		<-h.release
	}
	return slices.Clone(h.views)
}

func (h *participantHost) SubscribeParticipants(handle func(session.Event)) (session.ParticipantSnapshot, func()) {
	unsubscribe := h.subscribeParticipants(handle)
	views := h.Participants()
	for index := range views {
		views[index].LastCompletedExecutionID = h.completed[views[index].Alias]
	}
	return session.ParticipantSnapshot{Participants: views}, unsubscribe
}

func (h *participantHost) ParticipantByName(name string) (participant.View, bool) {
	for _, view := range h.views {
		if view.Alias.Name == name {
			return view, true
		}
	}
	return participant.View{}, false
}

func newTestAlias(name string) participant.Alias {
	return participant.New(name, "builder", participant.InitiativeManual).Alias
}

func (h *participantHost) callback() func(session.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.handle
}

func (*participantHost) CreateParticipantSendPlan(participant.Alias) session.ParticipantSendPlan {
	return session.ParticipantSendPlan{}
}

func (*participantHost) Execute(session.Command) error {
	return errors.New("participant fake does not deliver")
}

type waitingInvocation struct {
	staging   *Staging
	started   chan struct{}
	launches  atomic.Int32
	closes    atomic.Int32
	launchErr error
}

func (i *waitingInvocation) Staging() *Staging { return i.staging }
func (i *waitingInvocation) Close()            { i.closes.Add(1) }
func (i *waitingInvocation) Go(func(CommandResult)) error {
	i.launches.Add(1)
	i.started <- struct{}{}
	return i.launchErr
}

func newWaitingLifecycle() (*InvocationLifecycle, *participantHost, *waitingInvocation) {
	ada, tim := newTestAlias("ada"), newTestAlias("tim")
	host := &participantHost{views: []participant.View{
		{Alias: ada, Status: participant.StatusWorking, StartupReady: true},
		{Alias: tim, Status: participant.StatusWorking, StartupReady: true},
	}}
	invocation := &waitingInvocation{
		staging: &Staging{WaitFor: []participant.Alias{ada}, ToInform: []participant.Alias{tim}},
		started: make(chan struct{}, 1),
	}
	lifecycle := NewInvocationLifecycle(invocation, Context{Participants: host, Session: host})
	return lifecycle, host, invocation
}

func waitLifecycleSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle did not reach synchronization point")
	}
}

// Missing collection ordering is a host contract violation, not unsafe native
// delivery: the execution guard rejects busy work, but staging fails to retain it.
func TestInvocationLifecycle_InitialBusySnapshotRequiresCollectedBusyFact(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "busy fact pending upstream", true: "busy fact collected before read returns"}[ordered], func(t *testing.T) {
			lifecycle, host, invocation := newWaitingLifecycle()
			defer lifecycle.Close(errors.New("cleanup"))
			ada := invocation.staging.WaitFor[0]
			invocation.staging.ToInform = nil
			busyErr := errors.New("execution rejected busy participant")
			invocation.launchErr = busyErr
			host.beforeRead = func() {
				handle := host.callback()
				handle(session.AgentReady{Alias: ada})
				if ordered {
					handle(session.ParticipantStatusChanged{
						Alias: ada, From: participant.StatusIdle, To: participant.StatusWorking,
					})
				}
			}
			results := make(chan CommandResult, 1)
			_, err := lifecycle.Start(func(result CommandResult) { results <- result })
			if !ordered {
				if !errors.Is(err, busyErr) || invocation.launches.Load() != 1 {
					t.Fatalf("unordered initialization = %v, launches = %d; want execution rejection", err, invocation.launches.Load())
				}
				if len(results) != 0 || invocation.closes.Load() != 1 || host.callback() != nil {
					t.Fatal("failed initial launch retained resources or queued completion")
				}
				return
			}
			assertInitialBusyLifecycleResumes(t, lifecycle, host, invocation, err, results)
		})
	}
}

func assertInitialBusyLifecycleResumes(t *testing.T, lifecycle *InvocationLifecycle, host *participantHost, invocation *waitingInvocation, err error, results <-chan CommandResult) {
	t.Helper()
	if err != nil || invocation.launches.Load() != 0 || host.callback() == nil {
		t.Fatalf("ordered initialization did not retain waiting work: %v", err)
	}
	invocation.launchErr = nil
	host.callback()(session.ParticipantStatusChanged{
		Alias: invocation.staging.WaitFor[0], From: participant.StatusWorking, To: participant.StatusIdle,
	})
	lifecycle.ProcessEvents()
	waitLifecycleSignal(t, invocation.started)
	if invocation.launches.Load() != 1 || host.callback() != nil || len(results) != 0 {
		t.Fatal("readiness did not launch exactly once and release subscription")
	}
}

func TestInvocationLifecycle_InitialBusySnapshotPreservesLaterReadyFact(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	defer lifecycle.Close(errors.New("cleanup"))
	ada := invocation.staging.WaitFor[0]
	invocation.staging.ToInform = nil
	host.beforeRead = func() {
		handle := host.callback()
		// The busy snapshot was captured before the participant became idle.
		handle(session.ParticipantStatusChanged{
			Alias: ada, From: participant.StatusIdle, To: participant.StatusWorking,
		})
		handle(session.ParticipantStatusChanged{
			Alias: ada, From: participant.StatusWorking, To: participant.StatusIdle,
		})
	}
	if _, err := lifecycle.Start(func(CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if invocation.launches.Load() != 1 || host.callback() != nil {
		t.Fatal("older busy snapshot hid the collected readiness or retained subscription")
	}
}

func TestInvocationLifecycle_CloseWhileFactsArrive(t *testing.T) {
	for range 32 {
		lifecycle, host, invocation := newWaitingLifecycle()
		ada, tim := invocation.staging.WaitFor[0], invocation.staging.ToInform[0]
		if _, err := lifecycle.Start(func(CommandResult) {}); err != nil {
			t.Fatal(err)
		}
		handle := host.callback()
		var calls sync.WaitGroup
		calls.Add(2)
		go func() {
			defer calls.Done()
			handle(session.AgentReady{Alias: ada})
			handle(session.AgentReady{Alias: tim})
		}()
		go func() {
			defer calls.Done()
			lifecycle.Close(errors.New("discard"))
		}()
		calls.Wait()
		lifecycle.ProcessEvents() // A queued request after close must do nothing.
		if invocation.launches.Load() != 0 || invocation.closes.Load() != 1 || host.callback() != nil {
			t.Fatal("readiness/discard race duplicated launch or leaked resources")
		}
		count := invocation.launches.Load()
		handle(session.AgentReady{Alias: ada}) // Captured callback after unsubscribe.
		if invocation.launches.Load() != count {
			t.Fatal("late callback restored closed work")
		}
	}
}

func TestInvocationLifecycle_ProcessesCollectedDeparturesBeforeLaunch(t *testing.T) {
	for _, name := range []string{"ada", "tim"} {
		t.Run(name, func(t *testing.T) {
			lifecycle, host, invocation := newWaitingLifecycle()
			view, _ := host.ParticipantByName(name)
			alias, ada := view.Alias, invocation.staging.WaitFor[0]
			defer lifecycle.Close(errors.New("cleanup"))
			var discards atomic.Int32
			invocation.staging.DiscardInform = func(participant.Alias) { discards.Add(1) }
			results := make(chan CommandResult, 2)
			if _, err := lifecycle.Start(func(result CommandResult) { results <- result }); err != nil {
				t.Fatal(err)
			}
			handle := host.callback()
			// Collect all facts before the owner advances the lifecycle.
			handle(session.ParticipantStatusChanged{Alias: alias, From: participant.StatusWorking, To: participant.StatusIdle})
			handle(session.AgentStopped{Alias: alias})
			handle(session.AgentReady{Alias: alias})
			handle(session.AgentReady{Alias: ada})
			lifecycle.ProcessEvents()
			if alias.Name == "tim" {
				waitLifecycleSignal(t, invocation.started)
				if discards.Load() != 1 {
					t.Fatal("replacement optional recipient was not discarded once")
				}
				return
			}
			select {
			case result := <-results:
				accepted, ok := lifecycle.Complete(result)
				if !ok || invocation.launches.Load() != 0 {
					t.Fatal("collected required departure was bypassed")
				}
				assertRequiredParticipantUnavailable(t, accepted.Err, alias)
			case <-time.After(3 * time.Second):
				t.Fatal("required departure did not settle")
			}
		})
	}
}

func TestInvocationLifecycle_DepartureDuringDiscardPreventsLaunch(t *testing.T) {
	for _, kind := range []string{"stopped", "crashed"} {
		t.Run(kind, func(t *testing.T) {
			lifecycle, host, invocation := newWaitingLifecycle()
			defer lifecycle.Close(errors.New("cleanup"))
			ada := invocation.staging.WaitFor[0]
			var event session.Event = session.AgentStopped{Alias: ada}
			if kind == "crashed" {
				event = session.AgentCrashed{Alias: ada}
			}
			host.views = []participant.View{{Alias: ada, Status: participant.StatusIdle, StartupReady: true}}
			var discards atomic.Int32
			invocation.staging.DiscardInform = func(participant.Alias) {
				discards.Add(1)
				host.callback()(event)
			}
			_, err := lifecycle.Start(func(CommandResult) { t.Error("initial failure should return from Start") })
			if err == nil || invocation.launches.Load() != 0 {
				t.Fatal("collected primary departure was lost during optional discard")
			}
			if discards.Load() != 1 || invocation.closes.Load() != 1 || host.callback() != nil {
				t.Fatal("failure did not discard once and release resources")
			}
		})
	}
}

func TestInvocationLifecycle_OptionalDepartureDuringDiscardIsReevaluated(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	ada, tim, ben := invocation.staging.WaitFor[0], invocation.staging.ToInform[0], newTestAlias("ben")
	defer lifecycle.Close(errors.New("cleanup"))
	host.views = []participant.View{
		{Alias: ada, Status: participant.StatusIdle, StartupReady: true},
		{Alias: ben, Status: participant.StatusIdle, StartupReady: true},
	}
	invocation.staging.ToInform = []participant.Alias{tim, ben}
	var discarded []participant.Alias
	invocation.staging.DiscardInform = func(alias participant.Alias) {
		discarded = append(discarded, alias)
		if alias.Name == "tim" {
			host.callback()(session.AgentStopped{Alias: ben})
		}
	}
	if _, err := lifecycle.Start(func(CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(discarded, []participant.Alias{tim, ben}) || invocation.launches.Load() != 1 {
		t.Fatalf("discarded = %v, launches = %d", discarded, invocation.launches.Load())
	}
}

func TestInvocationLifecycle_BusyUpdateDuringDiscardRetainsWork(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	ada := invocation.staging.WaitFor[0]
	defer lifecycle.Close(errors.New("cleanup"))
	host.views = []participant.View{{Alias: ada, Status: participant.StatusIdle, StartupReady: true}}
	var discards atomic.Int32
	invocation.staging.DiscardInform = func(participant.Alias) {
		discards.Add(1)
		host.callback()(session.ParticipantStatusChanged{
			Alias: ada, From: participant.StatusIdle, To: participant.StatusWorking,
		})
	}
	if _, err := lifecycle.Start(func(CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if invocation.launches.Load() != 0 {
		t.Fatal("busy update was lost before launch")
	}
	host.callback()(session.AgentReady{Alias: ada})
	lifecycle.ProcessEvents()
	waitLifecycleSignal(t, invocation.started)
	if discards.Load() != 1 || invocation.launches.Load() != 1 {
		t.Fatal("reevaluation repeated discard or launch")
	}
}

func assertRequiredParticipantUnavailable(t *testing.T, err error, alias participant.Alias) {
	t.Helper()
	var unavailable *RequiredParticipantUnavailableError
	if !errors.As(err, &unavailable) || unavailable.Alias != alias {
		t.Fatalf("required participant outcome = %v; want alias %#v", err, alias)
	}
}

func (*participantHost) Subscribe(func(ExecutionEvent)) func() { return func() {} }

func (h *participantHost) CreateExecution() SessionExecution { return h }
