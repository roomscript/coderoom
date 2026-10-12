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

type monitorHost struct {
	mu      sync.Mutex
	handle  func(session.Event)
	views   []participant.View
	entered chan struct{}
	release chan struct{}
}

func (h *monitorHost) Subscribe(handle func(session.Event)) func() {
	h.mu.Lock()
	h.handle = handle
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.handle = nil
		h.mu.Unlock()
	}
}

func (h *monitorHost) Participants() []participant.View {
	if h.entered != nil {
		close(h.entered)
		<-h.release
	}
	return slices.Clone(h.views)
}

func (h *monitorHost) ParticipantByName(name string) (participant.View, bool) {
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

func (h *monitorHost) callback() func(session.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.handle
}

func (*monitorHost) CreateParticipantSendPlan(participant.Alias) session.ParticipantSendPlan {
	return session.ParticipantSendPlan{}
}

func (*monitorHost) Execute(session.Command) error {
	return errors.New("monitor fake does not deliver")
}

type monitorInvocation struct {
	staging  *Staging
	started  chan struct{}
	launches atomic.Int32
	closes   atomic.Int32
}

func (i *monitorInvocation) Staging() *Staging { return i.staging }
func (i *monitorInvocation) Close()            { i.closes.Add(1) }
func (i *monitorInvocation) Go(func(CommandResult)) error {
	i.launches.Add(1)
	i.started <- struct{}{}
	return nil
}

func newWaitingMonitor() (*InvocationLifecycle, *monitorHost, *monitorInvocation) {
	ada, tim := newTestAlias("ada"), newTestAlias("tim")
	host := &monitorHost{views: []participant.View{
		{Alias: ada, Status: participant.StatusWorking, StartupReady: true},
		{Alias: tim, Status: participant.StatusWorking, StartupReady: true},
	}}
	invocation := &monitorInvocation{
		staging: &Staging{WaitFor: []participant.Alias{ada}, ToInform: []participant.Alias{tim}},
		started: make(chan struct{}, 1),
	}
	lifecycle := NewInvocationLifecycle(invocation, Context{Participants: host, Session: host})
	return lifecycle, host, invocation
}

func waitMonitorSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not reach synchronization point")
	}
}

func TestInvocationMonitor_CloseDuringInitialRead(t *testing.T) {
	lifecycle, host, invocation := newWaitingMonitor()
	host.views[0].Status, host.views[1].Status = participant.StatusIdle, participant.StatusIdle
	host.entered, host.release = make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(host.release) })
	started := make(chan error, 1)
	go func() { started <- lifecycle.Start(func(CommandResult) {}) }()
	waitMonitorSignal(t, host.entered)
	closed := make(chan struct{})
	go func() {
		lifecycle.Close(errors.New("shutdown"))
		close(closed)
	}()
	waitMonitorSignal(t, lifecycle.monitor.cancelled)
	select {
	case <-closed:
		t.Fatal("Close returned while initialization still owned the invocation")
	default:
	}
	releaseOnce.Do(func() { close(host.release) })
	waitMonitorSignal(t, closed)
	if err := <-started; !errors.Is(err, ErrInvocationNotPrepared) {
		t.Fatalf("cancelled initialization = %v", err)
	}
	if invocation.launches.Load() != 0 || invocation.closes.Load() != 1 || host.callback() != nil {
		t.Fatal("cancelled initialization launched or retained resources")
	}
}

func TestInvocationMonitor_CloseRacesWithReady(t *testing.T) {
	for range 32 {
		lifecycle, host, invocation := newWaitingMonitor()
		ada, tim := invocation.staging.WaitFor[0], invocation.staging.ToInform[0]
		if err := lifecycle.Start(func(CommandResult) {}); err != nil {
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
		waitMonitorSignal(t, lifecycle.monitor.done)
		if invocation.launches.Load() > 1 || invocation.closes.Load() != 1 || host.callback() != nil {
			t.Fatal("readiness/discard race duplicated launch or leaked resources")
		}
		count := invocation.launches.Load()
		handle(session.AgentReady{Alias: ada}) // Captured callback after unsubscribe.
		if invocation.launches.Load() != count {
			t.Fatal("late callback restored closed work")
		}
	}
}

func TestInvocationMonitor_ProcessesCollectedDeparturesBeforeLaunch(t *testing.T) {
	for _, name := range []string{"ada", "tim"} {
		t.Run(name, func(t *testing.T) {
			lifecycle, host, invocation := newWaitingMonitor()
			view, _ := host.ParticipantByName(name)
			alias, ada := view.Alias, invocation.staging.WaitFor[0]
			defer lifecycle.Close(errors.New("cleanup"))
			var discards atomic.Int32
			invocation.staging.DiscardInform = func(participant.Alias) { discards.Add(1) }
			results := make(chan CommandResult, 2)
			if err := lifecycle.Start(func(result CommandResult) { results <- result }); err != nil {
				t.Fatal(err)
			}
			handle := host.callback()
			// Hold evaluation while the producer delivers these pending facts.
			lifecycle.mu.Lock()
			handle(session.ParticipantStatusChanged{Alias: alias, From: participant.StatusWorking, To: participant.StatusIdle})
			handle(session.AgentStopped{Alias: alias})
			handle(session.AgentReady{Alias: alias})
			handle(session.AgentReady{Alias: ada})
			lifecycle.mu.Unlock()
			if alias.Name == "tim" {
				waitMonitorSignal(t, invocation.started)
				if discards.Load() != 1 {
					t.Fatal("replacement optional recipient was not discarded once")
				}
				return
			}
			select {
			case result := <-results:
				accepted, ok := lifecycle.Complete(result)
				if !ok || accepted.Err == nil || invocation.launches.Load() != 0 {
					t.Fatal("collected required departure was bypassed")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("required departure did not settle")
			}
		})
	}
}

func TestInvocationMonitor_DepartureDuringDiscardPreventsLaunch(t *testing.T) {
	for _, kind := range []string{"stopped", "crashed"} {
		t.Run(kind, func(t *testing.T) {
			lifecycle, host, invocation := newWaitingMonitor()
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
			err := lifecycle.Start(func(CommandResult) { t.Error("initial failure should return from Start") })
			if err == nil || invocation.launches.Load() != 0 {
				t.Fatal("collected primary departure was lost during optional discard")
			}
			if discards.Load() != 1 || invocation.closes.Load() != 1 || host.callback() != nil {
				t.Fatal("failure did not discard once and release resources")
			}
		})
	}
}

func TestInvocationMonitor_OptionalDepartureDuringDiscardIsReevaluated(t *testing.T) {
	lifecycle, host, invocation := newWaitingMonitor()
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
	if err := lifecycle.Start(func(CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(discarded, []participant.Alias{tim, ben}) || invocation.launches.Load() != 1 {
		t.Fatalf("discarded = %v, launches = %d", discarded, invocation.launches.Load())
	}
}

func TestInvocationMonitor_BusyUpdateDuringDiscardRetainsWork(t *testing.T) {
	lifecycle, host, invocation := newWaitingMonitor()
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
	if err := lifecycle.Start(func(CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if invocation.launches.Load() != 0 {
		t.Fatal("busy update was lost before launch")
	}
	host.callback()(session.AgentReady{Alias: ada})
	waitMonitorSignal(t, invocation.started)
	if discards.Load() != 1 || invocation.launches.Load() != 1 {
		t.Fatal("reevaluation repeated discard or launch")
	}
}
