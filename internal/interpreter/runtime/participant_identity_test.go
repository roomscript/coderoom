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
	updates.merge([]participant.View{
		{Alias: old, Status: participant.StatusIdle, StartupReady: true},
		{Alias: current, Status: participant.StatusIdle, StartupReady: true},
	})
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

func TestParticipantMonitor_ignoresEventsFromSameNamePredecessor(t *testing.T) {
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
			host := &monitorHost{views: []participant.View{{Alias: current, Status: participant.StatusWorking, StartupReady: true}}}
			monitor := newParticipantMonitor()
			monitor.initialize(Context{Participants: host, Session: host}, func(alias participant.Alias) bool { return alias == current })
			defer monitor.stop()
			before := monitor.readSnapshot()
			host.callback()(tc.event(old))
			after := monitor.readSnapshot()
			if after.revision != before.revision || len(after.states) != 1 || after.states[current] != before.states[current] || len(monitor.wake) != 0 {
				t.Fatal("predecessor event changed or woke replacement monitoring")
			}
			host.callback()(session.AgentReady{Alias: current})
			if !monitor.readSnapshot().states[current].isReadyForWork() || len(monitor.wake) != 1 {
				t.Fatal("replacement's own readiness was not collected")
			}
		})
	}
}

func TestInvocationMonitor_replacementReadinessCannotReleaseOriginalWait(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"required", "notice"}[index], func(t *testing.T) {
			lifecycle, host, invocation := newWaitingMonitor()
			defer lifecycle.Close(errors.New("cleanup"))
			original := host.views[index].Alias
			replacement := newTestAlias(original.Name)
			host.views[1-index].Status = participant.StatusIdle
			var discarded []participant.Alias
			invocation.staging.DiscardInform = func(alias participant.Alias) { discarded = append(discarded, alias) }
			if err := lifecycle.Start(func(CommandResult) { t.Error("unexpected failure") }); err != nil {
				t.Fatal(err)
			}
			handle := host.callback()
			before := lifecycle.monitor.readSnapshot()
			handle(session.AgentReady{Alias: replacement})
			handle(session.AgentStopped{Alias: replacement})
			if after := lifecycle.monitor.readSnapshot(); after.revision != before.revision || after.states[original] != before.states[original] || invocation.launches.Load() != 0 {
				t.Fatal("replacement events advanced retained work for the original")
			}
			handle(session.AgentReady{Alias: original})
			waitMonitorSignal(t, invocation.started)
			waitMonitorSignal(t, lifecycle.monitor.done)
			if invocation.launches.Load() != 1 || len(discarded) != 0 || host.callback() != nil {
				t.Fatal("original readiness failed to launch once and release the monitor")
			}
		})
	}
}

func TestInvocationMonitor_initialReplacementDoesNotInheritRequirements(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"required", "notice"}[index], func(t *testing.T) {
			lifecycle, host, invocation := newWaitingMonitor()
			defer lifecycle.Close(errors.New("cleanup"))
			original := host.views[index].Alias
			host.views[index].Alias = newTestAlias(original.Name)
			host.views[0].Status, host.views[1].Status = participant.StatusIdle, participant.StatusIdle
			var discarded []participant.Alias
			invocation.staging.DiscardInform = func(alias participant.Alias) { discarded = append(discarded, alias) }
			err := lifecycle.Start(func(CommandResult) { t.Error("initial failure must return from Start") })
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

func TestInvocationMonitor_departureCollectedDuringReadSurvivesReplacementSnapshot(t *testing.T) {
	lifecycle, host, invocation := newWaitingMonitor()
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
		started <- lifecycle.Start(func(CommandResult) { t.Error("initial failure must return from Start") })
	}()
	waitMonitorSignal(t, host.entered)
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
