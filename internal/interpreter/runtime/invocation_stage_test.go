package runtime

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
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
	lifecycle.ProcessEvents() // A stale queued request cannot revive discarded work.
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
	lifecycle.ProcessEvents()
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
	lifecycle.ProcessEvents()
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
	lifecycle.ProcessEvents()
	lifecycle.Close(errors.New("cleanup"))
	if invocation.launches.Load() != 0 || invocation.closes.Load() != 1 || host.callback() != nil {
		t.Fatal("queued failure launched work or repeated cleanup")
	}
}

func TestInvocationLifecycle_completionTracksCurrentTurn(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	defer lifecycle.Close(errors.New("cleanup"))
	alias := invocation.staging.WaitFor[0]
	host.views[0].ActiveExecutionID = 7
	if _, err := lifecycle.Start(func(CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	// A newer turn starts while another participant still blocks the stage.
	host.callback()(session.ParticipantStatusChanged{Alias: alias, From: participant.StatusIdle, To: participant.StatusWorking, ActiveExecutionID: 8})
	for _, view := range host.views {
		host.callback()(session.ParticipantWorkCompleted{Alias: view.Alias, ExecutionID: 8})
		host.callback()(session.ParticipantStatusChanged{Alias: view.Alias, From: participant.StatusWorking, To: participant.StatusIdle})
	}
	lastCompletedExecutionIDs := completionIDs{newTestAlias(alias.Name): 100, alias: 7}
	lifecycle.ctx.CompletedExecutions = lastCompletedExecutionIDs
	lifecycle.ProcessEvents()
	if invocation.launches.Load() != 0 {
		t.Fatal("older turn completion launched current work")
	}
	lastCompletedExecutionIDs[alias] = 8
	lastCompletedExecutionIDs[host.views[1].Alias] = 8
	if lifecycle.DiscardWaiting() {
		t.Fatal("discard defeated completion recorded in room")
	}
	lifecycle.ProcessEvents()
	if invocation.launches.Load() != 1 {
		t.Fatal("current completion recorded in room did not launch work")
	}

}

func TestInvocationLifecycle_requiredAndOptionalSharePropagationReadiness(t *testing.T) {
	for _, test := range []struct {
		name  string
		index int
	}{{"required", 0}, {"optional", 1}} {
		t.Run(test.name, func(t *testing.T) {
			lifecycle, host, invocation := newWaitingLifecycle()
			defer lifecycle.Close(errors.New("cleanup"))
			for index := range host.views {
				host.views[index].Status = participant.StatusIdle
			}
			index := test.index
			alias := host.views[index].Alias
			host.completed = map[participant.Alias]session.ExecutionID{host.views[index].Alias: 7}
			waiting, err := lifecycle.Start(func(CommandResult) {})
			if err != nil || !waiting {
				t.Fatalf("Start=%v,%v", waiting, err)
			}
			lifecycle.ProcessEvents()
			if invocation.launches.Load() != 0 {
				t.Fatal("idle bypassed unpropagated completion")
			}
			lastCompletedExecutionIDs := completionIDs{alias: 7}
			lifecycle.ctx.CompletedExecutions = lastCompletedExecutionIDs
			lifecycle.ProcessEvents()
			if invocation.launches.Load() != 1 {
				t.Fatal("propagated completion did not release wait")
			}
		})
	}
}

type completionIDs map[participant.Alias]participant.ExecutionID

func (ids completionIDs) LastCompletedExecutionID(alias participant.Alias) participant.ExecutionID {
	return ids[alias]
}
