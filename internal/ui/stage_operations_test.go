package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
)

type recordingStageOperator struct {
	takeRaw        string
	takeOK         bool
	discardOK      bool
	interruptOK    bool
	takeCalls      int
	discardCalls   int
	interruptCalls int
}

func (o *recordingStageOperator) TakeStageForEdit() (string, bool) {
	o.takeCalls++
	return o.takeRaw, o.takeOK
}

func (o *recordingStageOperator) DiscardStage() bool {
	o.discardCalls++
	return o.discardOK
}

func (o *recordingStageOperator) InterruptAndDispatchStage() bool {
	o.interruptCalls++
	return o.interruptOK
}

func TestStageOperationCommandsInvokeInterpreterAdapter(t *testing.T) {
	operator := &recordingStageOperator{
		takeRaw: "@ada hello", takeOK: true, discardOK: true, interruptOK: true,
	}

	taken := takeStageForEdit(operator)().(stageTakenForEditMsg)
	discarded := discardStage(operator)().(stageDiscardedMsg)
	interrupted := interruptAndDispatchStage(operator)().(stageInterruptRequestedMsg)

	if taken.raw != "@ada hello" || !taken.ok || !discarded.ok || !interrupted.ok {
		t.Fatalf("results = %#v, %#v, %#v", taken, discarded, interrupted)
	}
	if operator.takeCalls != 1 || operator.discardCalls != 1 || operator.interruptCalls != 1 {
		t.Fatalf("calls = take %d, discard %d, interrupt %d", operator.takeCalls, operator.discardCalls, operator.interruptCalls)
	}
}

func TestStageTakenForEditRestoresDraftAcrossResultOrdering(t *testing.T) {
	tests := []struct {
		name             string
		snapshotFirst    bool
		typeBeforeResult bool
	}{
		{name: "result before snapshot"},
		{name: "snapshot before result", snapshotFirst: true},
		{name: "typing before result", snapshotFirst: true, typeBeforeResult: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := makeReadyModel(t)
			m.room = m.room.SetComposerStaged("@ada hello", "waiting")
			snapshot := interpreterEventMsg{event: interpreter.StateChanged{}}
			result := stageTakenForEditMsg{raw: "@ada hello", ok: true}

			if tt.snapshotFirst {
				next, _ := m.Update(snapshot)
				m = next.(Model)
			}
			if tt.typeBeforeResult {
				// Step 7e.2 will clear staged presentation from this snapshot. Model
				// that state here to prove a delayed result cannot erase new input.
				m.room = m.room.ClearComposerStaged().SetComposeValue("@ada hello!")
			}

			next, _ := m.Update(result)
			m = next.(Model)
			if !tt.typeBeforeResult {
				next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '!', Text: "!"}))
				m = next.(Model)
			}
			if !tt.snapshotFirst {
				// The result unlocks the composer before this delayed snapshot.
				// Input entered in between must survive snapshot presentation.
				next, _ = m.Update(snapshot)
				m = next.(Model)
			}

			if got := m.room.ComposeValue(); got != "@ada hello!" {
				t.Fatalf("composer = %q, want %q", got, "@ada hello!")
			}
			if m.room.IsComposerStaged() {
				t.Fatal("composer remained staged")
			}
		})
	}
}

func TestStageOperationFailureMessagesLeaveComposerUnchanged(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.SetComposerStaged("hello", "waiting")

	for _, msg := range []tea.Msg{
		stageTakenForEditMsg{raw: "hello"},
		stageDiscardedMsg{},
		stageInterruptRequestedMsg{},
	} {
		next, _ := m.Update(msg)
		m = next.(Model)
	}

	if got := m.room.ComposeValue(); got != "hello" || !m.room.IsComposerStaged() {
		t.Fatalf("composer = %q, staged = %v", got, m.room.IsComposerStaged())
	}
}
