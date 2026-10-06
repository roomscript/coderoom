package room

import (
	"errors"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/session"
)

type updateListener struct {
	ch chan Update
}

func (l updateListener) OnRoomUpdate(update Update) {
	l.ch <- update
}

func newTestRoom(t *testing.T) (*Room, <-chan Update) {
	t.Helper()
	ch := make(chan Update, 16)
	room := New(WithObserver(updateListener{ch: ch}))
	t.Cleanup(room.Close)
	return room, ch
}

func waitUpdate(t *testing.T, ch <-chan Update) Update {
	t.Helper()
	select {
	case update := <-ch:
		return update
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for room update")
		return Update{}
	}
}

func TestOnEvent_agentLifecycleAppendsSystemRecords(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentStarting{Alias: "ada"})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentStopped{Alias: "ada"})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if len(snapshot.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(snapshot.Records))
	}
	if snapshot.Records[0].Kind != KindSystem || snapshot.Records[0].Text != "[ada starting]" {
		t.Fatalf("unexpected first record: %#v", snapshot.Records[0])
	}
	if snapshot.Records[1].Text != "[ada joined]" {
		t.Fatalf("unexpected second record: %#v", snapshot.Records[1])
	}
	if snapshot.Records[2].Text != "[ada left]" {
		t.Fatalf("unexpected third record: %#v", snapshot.Records[2])
	}
	if !snapshot.Departed["ada"] {
		t.Fatal("expected ada to be marked departed")
	}
}

func TestOnEvent_agentMessageStreamsIntoSingleRecord(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"}}})
	waitUpdate(t, updates)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: " world"}}})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if len(snapshot.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snapshot.Records))
	}
	if len(snapshot.OpenStreams) != 1 {
		t.Fatalf("expected 1 open stream, got %d", len(snapshot.OpenStreams))
	}
	if snapshot.Records[0].Msg == nil {
		t.Fatal("expected message payload")
	}
	output, ok := snapshot.Records[0].Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected Output content, got %T", snapshot.Records[0].Msg.Content)
	}
	if output.Text != "hello world" {
		t.Fatalf("expected accumulated output, got %q", output.Text)
	}
}

func TestOnEvent_outputFlushClosesStreamAndPreservesAccumulatedRecord(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"}}})
	streamUpdate := waitUpdate(t, updates)
	if streamUpdate.Trigger.Kind != UpdateTriggerNone {
		t.Fatalf("stream update trigger = %#v, want none", streamUpdate.Trigger)
	}
	room.OnEvent(session.AgentMessage{
		Alias:         "ada",
		Msg:           agent.Message{StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{}},
		TurnCompleted: true,
		TurnID:        7,
	})
	flushUpdate := waitUpdate(t, updates)
	if flushUpdate.Trigger.Kind != UpdateTriggerAgentTurnCompleted ||
		flushUpdate.Trigger.Alias != "ada" || flushUpdate.Trigger.StreamID != "out1" ||
		flushUpdate.Trigger.TurnID != 7 {
		t.Fatalf("flush update trigger = %#v", flushUpdate.Trigger)
	}

	snapshot := room.Snapshot()
	if len(snapshot.OpenStreams) != 0 {
		t.Fatalf("expected no open streams after flush, got %d", len(snapshot.OpenStreams))
	}
	if len(snapshot.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snapshot.Records))
	}
	output, ok := snapshot.Records[0].Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected Output content, got %T", snapshot.Records[0].Msg.Content)
	}
	if output.Text != "hello" {
		t.Fatalf("expected preserved output, got %q", output.Text)
	}
}

func TestOnEvent_contextHandoffAppendsAuditRecord(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.ContextHandoff{FromAlias: "ada",
		ToAlias: "turing",
		Text:    "ship it",
		Preview: "[handoff ada -> turing]\n  ↦ source: ada latest output\n  > ship it",
	})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if len(snapshot.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snapshot.Records))
	}
	if snapshot.Records[0].Kind != KindSystem {
		t.Fatalf("expected system record, got %#v", snapshot.Records[0])
	}
	if snapshot.Records[0].Text != "[handoff ada -> turing]\n  ↦ source: ada latest output\n  > ship it" {
		t.Fatalf("unexpected handoff record: %#v", snapshot.Records[0])
	}
}

func TestLatestCompletedOutput(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "partial"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "reason1", Mode: agent.ModeSingle, Content: agent.Reasoning{Text: "skip"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out2", Mode: agent.ModeSingle, Content: agent.Output{Text: "done"}}})
	waitUpdate(t, updates)

	got, ok := room.LatestCompletedOutput("ada")
	if !ok {
		t.Fatal("expected latest completed output")
	}
	if got != "done" {
		t.Fatalf("LatestCompletedOutput = %q, want %q", got, "done")
	}
	source, ok := room.LatestHandoffSource("ada")
	if !ok || source.RecordIndex != 3 {
		t.Fatalf("LatestHandoffSource = %#v, %v; want record 3", source, ok)
	}
	snapshot := room.Snapshot()
	if !snapshot.Records[3].HandoffSource {
		t.Fatalf("expected latest completed output record to be marked as handoff source: %#v", snapshot.Records[3])
	}
}

func TestLatestCompletedOutput_returnsFlushedStreamOutput(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "done"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{}}})
	waitUpdate(t, updates)

	got, ok := room.LatestCompletedOutput("ada")
	if !ok {
		t.Fatal("expected latest completed flushed stream output")
	}
	if got != "done" {
		t.Fatalf("LatestCompletedOutput = %q, want %q", got, "done")
	}
	source, ok := room.LatestHandoffSource("ada")
	if !ok || source.RecordIndex != 1 {
		t.Fatalf("LatestHandoffSource = %#v, %v; want record 1", source, ok)
	}
	snapshot := room.Snapshot()
	if !snapshot.Records[1].HandoffSource {
		t.Fatalf("expected flushed output record to be marked as handoff source: %#v", snapshot.Records[1])
	}
}

func TestLatestHandoffSource_movesToNewestCompletedOutput(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeSingle, Content: agent.Output{Text: "first"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out2", Mode: agent.ModeSingle, Content: agent.Output{Text: "second"}}})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if snapshot.Records[1].HandoffSource {
		t.Fatalf("expected older output to lose handoff marker: %#v", snapshot.Records[1])
	}
	if !snapshot.Records[2].HandoffSource {
		t.Fatalf("expected newest output to gain handoff marker: %#v", snapshot.Records[2])
	}
}

func TestLatestHandoffSource_clearsOnDeparture(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeSingle, Content: agent.Output{Text: "done"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentStopped{Alias: "ada"})
	waitUpdate(t, updates)

	if _, ok := room.LatestHandoffSource("ada"); ok {
		t.Fatal("expected no handoff source after departure")
	}
	snapshot := room.Snapshot()
	if snapshot.Records[1].HandoffSource {
		t.Fatalf("expected handoff marker cleared on departure: %#v", snapshot.Records[1])
	}
}

func TestLatestHandoffSource_restoredOnRejoin(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeSingle, Content: agent.Output{Text: "done"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentStopped{Alias: "ada"})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)

	source, ok := room.LatestHandoffSource("ada")
	if !ok || source.RecordIndex != 1 {
		t.Fatalf("LatestHandoffSource after rejoin = %#v, %v; want record 1", source, ok)
	}
	snapshot := room.Snapshot()
	if !snapshot.Records[1].HandoffSource {
		t.Fatalf("expected handoff marker restored on rejoin: %#v", snapshot.Records[1])
	}
}

func TestOnEvent_reasoningFlushClearsOnlyMatchingReasoningStream(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "think"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "say"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "reason1", Mode: agent.ModeFlush, Content: agent.Reasoning{}}})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if len(snapshot.OpenStreams) != 1 {
		t.Fatalf("expected 1 remaining open stream, got %d", len(snapshot.OpenStreams))
	}
	if snapshot.OpenStreams[0].StreamID != "out1" {
		t.Fatalf("expected output stream to remain open, got %#v", snapshot.OpenStreams[0])
	}
}

func TestOnEvent_commandFlushPreservesExitCodeAndClosesStream(t *testing.T) {
	room, updates := newTestRoom(t)
	exitCode := 0

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "cmd1", Mode: agent.ModeStream, Content: agent.Command{Command: "true", Cwd: "/tmp"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "cmd1", Mode: agent.ModeStream, Content: agent.Command{Output: "ok\n", ExitCode: &exitCode}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "cmd1", Mode: agent.ModeFlush, Content: agent.Command{}}})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if len(snapshot.OpenStreams) != 0 {
		t.Fatalf("expected command stream to be closed, got %d open streams", len(snapshot.OpenStreams))
	}
	cmd, ok := snapshot.Records[0].Msg.Content.(agent.Command)
	if !ok {
		t.Fatalf("expected Command content, got %T", snapshot.Records[0].Msg.Content)
	}
	if cmd.ExitCode == nil || *cmd.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %v", cmd.ExitCode)
	}
	if cmd.Output != "ok\n" {
		t.Fatalf("expected accumulated command output, got %q", cmd.Output)
	}
}

func TestOnEvent_fileChangeFlushPreservesChangesAndClosesStream(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "fc1", Mode: agent.ModeStream, Content: agent.FileChangeSet{
		Changes: []agent.FileChange{{Path: "a.txt", Diff: "+one\n", ChangeKind: "update"}},
	}},
	})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "fc1", Mode: agent.ModeStream, Content: agent.FileChangeSet{
		Status:  agent.ToolStatusCompleted,
		Changes: []agent.FileChange{{Path: "b.txt", Diff: "+two\n", ChangeKind: "add"}},
	}},
	})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "fc1", Mode: agent.ModeFlush, Content: agent.FileChangeSet{}}})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if len(snapshot.OpenStreams) != 0 {
		t.Fatalf("expected file-change stream to be closed, got %d open streams", len(snapshot.OpenStreams))
	}
	got, ok := snapshot.Records[0].Msg.Content.(agent.FileChangeSet)
	if !ok {
		t.Fatalf("expected FileChangeSet content, got %T", snapshot.Records[0].Msg.Content)
	}
	if got.Status != agent.ToolStatusCompleted {
		t.Fatalf("expected completed status, got %q", got.Status)
	}
	if len(got.Changes) != 2 {
		t.Fatalf("expected 2 accumulated changes, got %d", len(got.Changes))
	}
}

func TestOnEvent_agentStoppedClearsOnlyStoppedAliasStreams(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "ada-out", Mode: agent.ModeStream, Content: agent.Output{Text: "a"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "bob", Msg: agent.Message{StreamID: "bob-out", Mode: agent.ModeStream, Content: agent.Output{Text: "b"}}})
	waitUpdate(t, updates)
	room.OnEvent(session.AgentStopped{Alias: "ada"})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	if len(snapshot.OpenStreams) != 1 {
		t.Fatalf("expected 1 remaining open stream, got %d", len(snapshot.OpenStreams))
	}
	if snapshot.OpenStreams[0].Alias != "bob" {
		t.Fatalf("expected bob stream to remain, got %#v", snapshot.OpenStreams[0])
	}
}

func TestAppendSystemRecordNotifiesObserverAndAdvancesVersion(t *testing.T) {
	room, updates := newTestRoom(t)

	room.AppendSystemRecord("[system]")
	update := waitUpdate(t, updates)
	if update.Version != 1 {
		t.Fatalf("unexpected update version: %#v", update)
	}

	snapshot := room.Snapshot()
	if len(snapshot.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snapshot.Records))
	}
	if snapshot.Records[0].Kind != KindSystem || snapshot.Records[0].Text != "[system]" {
		t.Fatalf("unexpected system record: %#v", snapshot.Records[0])
	}
}

func TestAppendUserInputRecordNotifiesObserverAndAdvancesVersion(t *testing.T) {
	room, updates := newTestRoom(t)

	room.AppendUserInputRecord("hello", []string{"ada"})
	update := waitUpdate(t, updates)
	if update.Version != 1 {
		t.Fatalf("unexpected update version: %#v", update)
	}

	snapshot := room.Snapshot()
	if len(snapshot.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snapshot.Records))
	}
	r := snapshot.Records[0]
	if r.Kind != KindUserInput || len(r.Routing) != 1 || r.Routing[0] != "ada" {
		t.Fatalf("unexpected user input record: %#v", r)
	}
}

func TestAppendLogRecordNotifiesObserverAndAdvancesVersion(t *testing.T) {
	room, updates := newTestRoom(t)

	room.AppendLogRecord("ada", "warn")
	update := waitUpdate(t, updates)
	if update.Version != 1 {
		t.Fatalf("unexpected update version: %#v", update)
	}

	snapshot := room.Snapshot()
	if len(snapshot.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snapshot.Records))
	}
	r := snapshot.Records[0]
	if r.Kind != KindLog || r.Alias != "ada" || r.Text != "warn" {
		t.Fatalf("unexpected log record: %#v", r)
	}
}

func TestDelta_fromZeroRequiresResync(t *testing.T) {
	room, updates := newTestRoom(t)

	room.AppendSystemRecord("[system]")
	waitUpdate(t, updates)
	room.AppendUserInputRecord("hello", []string{"ada"})
	waitUpdate(t, updates)

	_, err := room.Delta(0)
	if !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("expected ErrResyncRequired, got %v", err)
	}
}

func TestDelta_incrementalCoalescesRepeatedRecordUpdates(t *testing.T) {
	room, updates := newTestRoom(t)

	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"}}})
	update1 := waitUpdate(t, updates)
	room.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: " world"}}})
	update2 := waitUpdate(t, updates)

	delta, err := room.Delta(update1.Version)
	if err != nil {
		t.Fatalf("Delta() error = %v", err)
	}
	if delta.Version != update2.Version {
		t.Fatalf("expected version %d, got %d", update2.Version, delta.Version)
	}
	if len(delta.RecordUpdates) != 1 {
		t.Fatalf("expected 1 coalesced record update, got %d", len(delta.RecordUpdates))
	}
	if delta.RecordUpdates[0].Index != 0 {
		t.Fatalf("expected record index 0, got %d", delta.RecordUpdates[0].Index)
	}
	output, ok := delta.RecordUpdates[0].Record.Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected Output content, got %T", delta.RecordUpdates[0].Record.Msg.Content)
	}
	if output.Text != "hello world" {
		t.Fatalf("expected accumulated output, got %q", output.Text)
	}
}

func TestDelta_currentVersionReturnsEmptyDelta(t *testing.T) {
	room, updates := newTestRoom(t)

	room.AppendSystemRecord("[system]")
	update := waitUpdate(t, updates)

	delta, err := room.Delta(update.Version)
	if err != nil {
		t.Fatalf("Delta() error = %v", err)
	}
	if len(delta.RecordUpdates) != 0 {
		t.Fatalf("expected no incremental records, got %#v", delta.RecordUpdates)
	}
}

func TestDelta_futureVersionRequiresResync(t *testing.T) {
	room, updates := newTestRoom(t)

	room.AppendSystemRecord("[system]")
	waitUpdate(t, updates)

	_, err := room.Delta(99)
	if !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("expected ErrResyncRequired, got %v", err)
	}
}

func TestDelta_prunedVersionRequiresResync(t *testing.T) {
	room, updates := newTestRoom(t)

	for i := 0; i < deltaHistoryLimit+2; i++ {
		room.AppendSystemRecord("[system]")
		waitUpdate(t, updates)
	}

	_, err := room.Delta(1)
	if !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("expected ErrResyncRequired after pruning, got %v", err)
	}
}

func TestSnapshotClonesRecordsAndState(t *testing.T) {
	room, updates := newTestRoom(t)
	exitCode := 7

	room.OnEvent(session.AgentStarted{Alias: "ada"})
	waitUpdate(t, updates)
	room.AppendUserInputRecord("hello", []string{"ada"})
	waitUpdate(t, updates)
	room.AppendRecord(Record{
		Kind:  KindCommand,
		Alias: "ada",
		Text:  "out",
		Msg: &agent.Message{
			StreamID: "cmd1",
			Mode:     agent.ModeSingle,
			Content:  agent.Command{Command: "pwd", Cwd: "/tmp", Output: "out", ExitCode: &exitCode},
		},
	})
	waitUpdate(t, updates)

	snapshot := room.Snapshot()
	snapshot.Members[0] = "mutated"
	snapshot.Departed["ada"] = true
	snapshot.Records[0].Text = "changed"
	snapshot.Records[1].Routing[0] = "mutated"
	cmd := snapshot.Records[2].Msg.Content.(agent.Command)
	cmd.Output = "mutated"
	snapshot.Records[2].Msg.Content = cmd

	next := room.Snapshot()
	if next.Members[0] != "ada" {
		t.Fatalf("expected members to be cloned, got %v", next.Members)
	}
	if next.Departed["ada"] {
		t.Fatalf("expected departed map to be cloned, got %v", next.Departed)
	}
	if next.Records[0].Text != "[ada joined]" {
		t.Fatalf("expected record text to be preserved, got %#v", next.Records[0])
	}
	if next.Records[1].Routing[0] != "ada" {
		t.Fatalf("expected routing to be cloned, got %#v", next.Records[1])
	}
	nextCmd := next.Records[2].Msg.Content.(agent.Command)
	if nextCmd.Output != "out" {
		t.Fatalf("expected message content to be cloned, got %#v", nextCmd)
	}
}
