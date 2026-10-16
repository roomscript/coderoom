package room

import (
	"reflect"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestRoom_delayedDeparturePreservesReplacement(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event func(participant.Alias) session.Event
	}{
		{"stopped", func(alias participant.Alias) session.Event { return session.AgentStopped{Alias: alias} }},
		{"crashed", func(alias participant.Alias) session.Event { return session.AgentCrashed{Alias: alias} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			t.Cleanup(r.Close)
			old, current := newTestAlias("ada"), newTestAlias("ada")
			r.ApplyEvent(session.AgentReady{Alias: old})
			r.ApplyEvent(outputEvent(old, "shared", agent.ModeStream, "old"))
			r.ApplyEvent(session.AgentReady{Alias: current})
			r.ApplyEvent(outputEvent(current, "shared", agent.ModeStream, "current"))
			r.ApplyEvent(outputEvent(old, "shared", agent.ModeStream, " continued"))
			before := r.Snapshot()
			if len(before.OpenStreams) != 2 || before.Records[1].Text != "old continued" || before.Records[3].Text != "current" {
				t.Fatalf("same-name streams were merged: %#v", before)
			}
			r.ApplyEvent(tc.event(old))
			delta, err := r.Delta(before.Version)
			if err != nil {
				t.Fatal(err)
			}
			assertReplacementMeta(t, delta.Meta, old, current)
			assertNoHandoffSource(t, r, current)
			r.ApplyEvent(outputEvent(current, "shared", agent.ModeFlush, ""))
			assertHandoffSourceOwner(t, r, current, "current")
			if record := r.Snapshot().Records[1]; record.Alias != old || record.Text != "old continued" {
				t.Fatalf("historical stream lost original owner: %#v", record)
			}
		})
	}
}

func TestRoom_delayedOutputAndCompletionRetainOriginalOwner(t *testing.T) {
	r, updates := newTestRoom(t)
	old, current := newTestAlias("ada"), newTestAlias("ada")
	apply := func(event session.Event) Update {
		r.ApplyEvent(event)
		return waitUpdate(t, updates)
	}
	apply(session.AgentReady{Alias: old})
	apply(outputEvent(old, "completed", agent.ModeSingle, "old completed output"))
	assertHandoffSourceOwner(t, r, old, "old completed output")
	apply(session.AgentStopped{Alias: old})
	apply(session.AgentReady{Alias: current})
	assertNoHandoffSource(t, r, current)
	apply(outputEvent(current, "shared", agent.ModeStream, "current output"))
	apply(outputEvent(old, "shared", agent.ModeSingle, "delayed old output"))
	completion := outputEvent(old, "shared", agent.ModeFlush, "")
	completion.WorkCompleted, completion.ExecutionID = true, 7
	update := apply(completion)
	if update.Trigger.Alias != old || update.Trigger.ExecutionID != 7 || update.Trigger.Kind != UpdateTriggerAgentWorkCompleted {
		t.Fatalf("delayed completion lost original identity: %#v", update.Trigger)
	}
	snapshot := r.Snapshot()
	assertReplacementMeta(t, DeltaMeta{Members: snapshot.Members, Departed: snapshot.Departed, OpenStreams: snapshot.OpenStreams}, old, current)
	assertNoHandoffSource(t, r, current)
	assertNoHandoffSource(t, r, old)
	if record := snapshot.Records[5]; record.Alias != old || record.Text != "delayed old output" || record.HandoffSource {
		t.Fatalf("delayed output changed ownership or source eligibility: %#v", record)
	}
	completion.Alias, completion.ExecutionID = current, 8
	update = apply(completion)
	if update.Trigger.Alias != current || update.Trigger.ExecutionID != 8 {
		t.Fatalf("replacement completion lost its identity: %#v", update.Trigger)
	}
	assertHandoffSourceOwner(t, r, current, "current output")
}

func TestRoom_delayedReadinessCannotReviveDepartedInstance(t *testing.T) {
	r := New()
	t.Cleanup(r.Close)
	old, current := newTestAlias("ada"), newTestAlias("ada")
	r.ApplyEvent(session.AgentReady{Alias: old})
	r.ApplyEvent(outputEvent(old, "old", agent.ModeSingle, "old output"))
	r.ApplyEvent(session.AgentStopped{Alias: old})
	r.ApplyEvent(session.AgentReady{Alias: current})
	r.ApplyEvent(outputEvent(current, "current", agent.ModeSingle, "current output"))
	before := r.Snapshot()
	r.ApplyEvent(session.AgentReady{Alias: old})
	if after := r.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("delayed readiness changed projection: %#v", after)
	}
	assertNoHandoffSource(t, r, old)
	assertHandoffSourceOwner(t, r, current, "current output")
}

func TestRoom_delayedOutputWithoutFlushDoesNotReopenDepartedStream(t *testing.T) {
	r := New()
	t.Cleanup(r.Close)
	old, current := newTestAlias("ada"), newTestAlias("ada")
	r.ApplyEvent(session.AgentReady{Alias: old})
	r.ApplyEvent(outputEvent(old, "shared", agent.ModeStream, "old output"))
	r.ApplyEvent(session.AgentStopped{Alias: old})
	r.ApplyEvent(session.AgentReady{Alias: current})
	before := r.Snapshot()
	r.ApplyEvent(outputEvent(old, "shared", agent.ModeStream, "delayed old output"))
	snapshot := r.Snapshot()
	delta, err := r.Delta(before.Version)
	if err != nil {
		t.Fatal(err)
	}
	last := len(snapshot.Records) - 1
	if record := snapshot.Records[last]; record.Alias != old || record.Text != "delayed old output" || record.HandoffSource {
		t.Fatalf("delayed historical output was lost or changed ownership: %#v", record)
	}
	if len(delta.RecordUpdates) != 1 || delta.RecordUpdates[0].Index != last || delta.RecordUpdates[0].Record.Alias != old {
		t.Fatalf("delta lost delayed historical output: %#v", delta.RecordUpdates)
	}
	assertNoHandoffSource(t, r, old)
	assertNoHandoffSource(t, r, current)
	if len(snapshot.OpenStreams) != 0 {
		t.Errorf("snapshot retained departed participant's orphan stream: %#v", snapshot.OpenStreams)
	}
	if len(delta.Meta.OpenStreams) != 0 {
		t.Errorf("delta retained departed participant's orphan stream: %#v", delta.Meta.OpenStreams)
	}
}

func outputEvent(alias participant.Alias, streamID agent.StreamID, mode agent.Mode, text string) session.AgentMessage {
	return session.AgentMessage{Alias: alias, Msg: agent.Message{StreamID: streamID, Mode: mode, Content: agent.Output{Text: text}}}
}

func assertReplacementMeta(t *testing.T, meta DeltaMeta, old, current participant.Alias) {
	t.Helper()
	if !slices.Equal(meta.Members, []participant.Alias{current}) || !meta.Departed[old] || meta.Departed[current] {
		t.Fatalf("departure changed replacement membership: %#v", meta)
	}
	if len(meta.OpenStreams) != 1 || meta.OpenStreams[0].Alias != current || meta.OpenStreams[0].StreamID != "shared" {
		t.Fatalf("old event changed replacement stream: %#v", meta.OpenStreams)
	}
}

func assertNoHandoffSource(t *testing.T, r *Room, alias participant.Alias) {
	t.Helper()
	if source, ok := r.LatestHandoffSource(alias); ok {
		t.Fatalf("unexpected handoff source for %+v: %#v", alias, source)
	}
	if text, ok := r.LatestCompletedOutput(alias); ok {
		t.Fatalf("unexpected completed output for %+v: %q", alias, text)
	}
}

func assertHandoffSourceOwner(t *testing.T, r *Room, alias participant.Alias, text string) {
	t.Helper()
	source, ok := r.LatestHandoffSource(alias)
	if !ok || source.Text != text {
		t.Fatalf("handoff source = %#v, %v; want %q", source, ok, text)
	}
	record := r.Snapshot().Records[source.RecordIndex]
	if record.Alias != alias || !record.HandoffSource {
		t.Fatalf("handoff source lost instance ownership: %#v", record)
	}
}

func TestRoom_completedExecutionHistory(t *testing.T) {
	r := New()
	t.Cleanup(r.Close)
	alias, replacement := newTestAlias("ada"), newTestAlias("ada")
	r.InitializeCompletionHistory([]participant.View{{Alias: alias, LastCompletedExecutionID: 7}})
	if r.LastCompletedExecutionID(alias) != 7 || len(r.Snapshot().Records) != 0 {
		t.Fatal("attachment history did not establish completion without replay")
	}
	for _, test := range []struct {
		name      string
		completed bool
		id        participant.ExecutionID
		want      participant.ExecutionID
	}{
		{"ordinary output", false, 8, 7},
		{"completion", true, 8, 8},
		{"older completion", true, 6, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := outputEvent(alias, "stream", agent.ModeSingle, "output")
			message.WorkCompleted, message.ExecutionID = test.completed, test.id
			r.ApplyEvent(message)
			if got := r.LastCompletedExecutionID(alias); got != test.want {
				t.Fatalf("completion=%d, want %d", got, test.want)
			}
			if r.LastCompletedExecutionID(replacement) != 0 {
				t.Fatal("replacement inherited completion history")
			}
		})
	}
}
