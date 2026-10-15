package runtime

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/session"
)

func TestInvocationLifecycle_discardWaitingStopsLateReadiness(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	defer lifecycle.Close(errors.New("cleanup"))
	if _, err := lifecycle.Start(func(CommandResult) { t.Error("discarded invocation completed") }); err != nil {
		t.Fatal(err)
	}
	callback := host.callback()
	if !lifecycle.DiscardWaiting() {
		t.Fatal("blocked invocation was not discarded")
	}
	for _, view := range host.views {
		callback(session.AgentReady{Alias: view.Alias})
	}
	lifecycle.Advance() // A stale queued request cannot revive discarded work.
	if lifecycle.DiscardWaiting() || invocation.launches.Load() != 0 || invocation.closes.Load() != 1 || host.callback() != nil {
		t.Fatal("discard left live work or subscriptions")
	}
}

func TestInvocationLifecycle_collectedReadinessWinsOverDiscard(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	defer lifecycle.Close(errors.New("cleanup"))
	if _, err := lifecycle.Start(func(CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	// Collect all ready facts before the owner advances.
	for _, view := range host.views {
		host.callback()(session.AgentReady{Alias: view.Alias})
	}
	if lifecycle.DiscardWaiting() {
		t.Fatal("discard won over collected readiness")
	}
	lifecycle.Advance()
	waitLifecycleSignal(t, invocation.started)
	if invocation.launches.Load() != 1 {
		t.Fatal("readiness did not launch exactly once")
	}
}

func TestInvocationLifecycle_collectedDepartureWinsOverDiscard(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	results := make(chan CommandResult, 1)
	if _, err := lifecycle.Start(func(result CommandResult) { results <- result }); err != nil {
		t.Fatal(err)
	}
	alias := invocation.staging.WaitFor[0]
	host.callback()(session.AgentStopped{Alias: alias})
	if lifecycle.DiscardWaiting() || len(results) != 0 {
		t.Fatal("departure was discarded or settled outside owner advancement")
	}
	lifecycle.Advance()
	if len(results) != 1 {
		t.Fatal("advancement did not queue the departure outcome")
	}
	result := <-results
	assertRequiredParticipantUnavailable(t, result.Err, alias)
	// Shutdown can settle the queued failure before its completion work is applied.
	closed, ok := lifecycle.Close(errors.New("shutdown"))
	if !ok {
		t.Fatal("queued failure did not settle on shutdown")
	}
	assertRequiredParticipantUnavailable(t, closed.Err, alias)
	if _, accepted := lifecycle.Complete(result); accepted {
		t.Fatal("late completion settled twice")
	}
	lifecycle.Advance()
	lifecycle.Close(errors.New("cleanup"))
	if invocation.launches.Load() != 0 || invocation.closes.Load() != 1 || host.callback() != nil {
		t.Fatal("queued failure launched work or repeated cleanup")
	}
}
