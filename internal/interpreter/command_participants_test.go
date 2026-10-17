package interpreter_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
)

func TestParticipantCommands_submissionCompletesAfterExecution(t *testing.T) {
	for _, tt := range []struct {
		operation string
		command   session.Command
	}{
		{"invite", session.InviteCommand{Alias: "ada"}},
		{"remove", session.RemoveCommand{Alias: testAlias("ada")}},
		{"cancel", session.CancelCommand{Alias: testAlias("ada")}},
	} {
		for _, failed := range []bool{false, true} {
			t.Run(tt.operation+map[bool]string{false: "/success", true: "/failure"}[failed], func(t *testing.T) {
				assertParticipantCommandSubmission(t, tt.operation, tt.command, failed)
			})
		}
	}
}

func assertParticipantCommandSubmission(t *testing.T, operation string, want session.Command, failed bool) {
	t.Helper()
	interp, sess, events := newSubmitExample()
	defer interp.Close()
	if failed {
		sess.executeErr = errors.New("participant command rejected")
	}
	raw := "/" + operation + " ada"
	if err := interp.Submit(raw); err != nil {
		t.Fatal(err)
	}
	if accepted := receiveEvent[interpreter.InputAccepted](t, events); accepted.Raw != raw {
		t.Fatalf("acceptance=%#v", accepted)
	}
	if command := receiveCommand(t, sess.executed); command != want {
		t.Fatalf("command=%#v want=%#v", command, want)
	}
	changed := receiveEvent[interpreter.StateChanged](t, events)
	if len(changed.Snapshot.Room.Records) != 1 || changed.Snapshot.Room.Records[0].Text != raw {
		t.Fatalf("records=%#v", changed.Snapshot.Room.Records)
	}
	assertSessionSubmissionOutcome(t, events, raw, operation, sess.executeErr)
	interp.Close()
	assertSessionCommandSettled(t, sess, events)
}
