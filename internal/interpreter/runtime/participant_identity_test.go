package runtime

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestParticipantUpdates_mergeKeepsSameNameInstancesSeparate(t *testing.T) {
	old, current := newTestAlias("ada"), newTestAlias("ada")
	updates := &participantUpdates{}
	updates.start()
	defer updates.stop()
	updates.collect(session.AgentStopped{Alias: old})
	updates.merge(session.ParticipantSnapshot{Participants: []participant.View{
		{Alias: old, Status: participant.StatusIdle, StartupReady: true},
		{Alias: current, Status: participant.StatusIdle, StartupReady: true},
	}})
	snapshot := updates.readSnapshot()
	if len(snapshot.states) != 2 || !snapshot.states[old].departed || !snapshot.states[current].isReadyForWork() {
		t.Fatalf("merge mixed same-name instances or overwrote a departure: %#v", snapshot.states)
	}
	updates.collect(session.AgentReady{Alias: old})
	updates.collect(session.ParticipantStatusChanged{Alias: current, From: participant.StatusIdle, To: participant.StatusWorking})
	after := updates.readSnapshot()
	if !after.states[old].departed || after.states[current].status != participant.StatusWorking || !snapshot.states[current].isReadyForWork() {
		t.Fatal("updates changed another instance or an earlier snapshot")
	}
	snapshot.states[current] = participantState{departed: true}
	if updates.readSnapshot().states[current].departed {
		t.Fatal("snapshot mutation changed collected state")
	}
}

func TestParticipantSubscription_ignoresEventsFromSameNamePredecessor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event func(participant.Alias) session.Event
	}{
		{"ready", func(alias participant.Alias) session.Event { return session.AgentReady{Alias: alias} }},
		{"status", func(alias participant.Alias) session.Event {
			return session.ParticipantStatusChanged{Alias: alias, From: participant.StatusWorking, To: participant.StatusIdle}
		}},
		{"stopped", func(alias participant.Alias) session.Event { return session.AgentStopped{Alias: alias} }},
		{"crashed", func(alias participant.Alias) session.Event { return session.AgentCrashed{Alias: alias} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, current := newTestAlias("ada"), newTestAlias("ada")
			host := &participantHost{views: []participant.View{{Alias: current, Status: participant.StatusWorking, StartupReady: true}}}
			var requests int
			subscription := &participantSubscription{requestAdvance: func() { requests++ }}
			subscription.initialize(Context{Participants: host, Session: host}, func(alias participant.Alias) bool { return alias == current })
			defer subscription.stop()
			before := subscription.readSnapshot()
			host.callback()(tc.event(old))
			after := subscription.readSnapshot()
			if after.revision != before.revision || len(after.states) != 1 || after.states[current] != before.states[current] || requests != 0 {
				t.Fatal("predecessor event changed or requested replacement advancement")
			}
			host.callback()(session.AgentReady{Alias: current})
			if !subscription.readSnapshot().states[current].isReadyForWork() || requests != 1 {
				t.Fatal("replacement's own readiness was not collected")
			}
		})
	}
}

func TestInvocationLifecycle_replacementReadinessCannotReleaseOriginalWait(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"required", "notice"}[index], func(t *testing.T) {
			lifecycle, host, invocation := newWaitingLifecycle()
			defer lifecycle.Close(errors.New("cleanup"))
			original := host.views[index].Alias
			replacement := newTestAlias(original.Name)
			host.views[1-index].Status = participant.StatusIdle
			var discarded []participant.Alias
			invocation.staging.DiscardInform = func(alias participant.Alias) { discarded = append(discarded, alias) }
			if _, err := lifecycle.Start(func(CommandResult) { t.Error("unexpected failure") }); err != nil {
				t.Fatal(err)
			}
			handle := host.callback()
			before := lifecycle.subscription.readSnapshot()
			handle(session.AgentReady{Alias: replacement})
			handle(session.AgentStopped{Alias: replacement})
			if after := lifecycle.subscription.readSnapshot(); after.revision != before.revision || after.states[original] != before.states[original] || invocation.launches.Load() != 0 {
				t.Fatal("replacement events advanced retained work for the original")
			}
			handle(session.AgentReady{Alias: original})
			lifecycle.Advance()
			waitLifecycleSignal(t, invocation.started)
			if invocation.launches.Load() != 1 || len(discarded) != 0 || host.callback() != nil {
				t.Fatal("original readiness failed to launch once and release the subscription")
			}
		})
	}
}

func TestInvocationLifecycle_initialReplacementDoesNotInheritRequirements(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"required", "notice"}[index], func(t *testing.T) {
			lifecycle, host, invocation := newWaitingLifecycle()
			defer lifecycle.Close(errors.New("cleanup"))
			original := host.views[index].Alias
			host.views[index].Alias = newTestAlias(original.Name)
			host.views[0].Status, host.views[1].Status = participant.StatusIdle, participant.StatusIdle
			var discarded []participant.Alias
			invocation.staging.DiscardInform = func(alias participant.Alias) { discarded = append(discarded, alias) }
			_, err := lifecycle.Start(func(CommandResult) { t.Error("initial failure must return from Start") })
			if index == 0 {
				if err == nil || invocation.launches.Load() != 0 || invocation.closes.Load() != 1 {
					t.Fatal("replacement satisfied the missing original primary")
				}
			} else if err != nil || invocation.launches.Load() != 1 || !slices.Equal(discarded, []participant.Alias{original}) {
				t.Fatalf("optional original was not excluded: %v, discarded = %+v", err, discarded)
			}
			if host.callback() != nil {
				t.Fatal("initial decision retained the subscription")
			}
		})
	}
}

func TestInvocationLifecycle_departureCollectedDuringReadSurvivesReplacementSnapshot(t *testing.T) {
	lifecycle, host, invocation := newWaitingLifecycle()
	defer lifecycle.Close(errors.New("cleanup"))
	original := invocation.staging.WaitFor[0]
	replacement := newTestAlias(original.Name)
	host.views = []participant.View{{Alias: replacement, Status: participant.StatusIdle, StartupReady: true}}
	host.entered, host.release = make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(host.release) }) }
	defer release()
	started := make(chan error, 1)
	go func() {
		_, err := lifecycle.Start(func(CommandResult) { t.Error("initial failure must return from Start") })
		started <- err
	}()
	waitLifecycleSignal(t, host.entered)
	handle := host.callback()
	handle(session.AgentStopped{Alias: original})
	handle(session.AgentReady{Alias: replacement})
	release()
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("replacement snapshot overwrote collected original departure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial departure did not settle")
	}
	if invocation.launches.Load() != 0 || invocation.closes.Load() != 1 || host.callback() != nil {
		t.Fatal("initial departure launched replacement work or leaked resources")
	}
}

func TestParticipantUpdates_mergePreservesIndependentCompletionFacts(t *testing.T) {
	alias := newTestAlias("ada")
	for _, test := range []struct {
		name      string
		events    []session.Event
		completed session.ExecutionID
	}{
		{"status before return", []session.Event{session.ParticipantStatusChanged{Alias: alias, From: participant.StatusIdle, To: participant.StatusWorking}}, 7},
		{"completion before return", []session.Event{session.ParticipantWorkCompleted{Alias: alias, ExecutionID: 8}, session.ParticipantStatusChanged{Alias: alias, From: participant.StatusWorking, To: participant.StatusIdle}}, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			updates := &participantUpdates{}
			updates.start()
			defer updates.stop()
			for _, event := range test.events {
				updates.collect(event)
			}
			updates.merge(session.ParticipantSnapshot{Participants: []participant.View{{Alias: alias, Status: participant.StatusWorking, StartupReady: true, LastCompletedExecutionID: 7}}})
			state := updates.readSnapshot().states[alias]
			if state.lastCompletedExecutionID != test.completed {
				t.Fatalf("completed=%d, want %d", state.lastCompletedExecutionID, test.completed)
			}
		})
	}
}
