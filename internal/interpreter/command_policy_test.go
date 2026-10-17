package interpreter_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

func TestPolicy_submissionCompletesAfterExecution(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			interp, sess, events := newSubmitExample()
			defer interp.Close()
			failure := errors.New("policy rejected")
			if failed {
				sess.executeErr = failure
			}
			name := policy.SendNotices
			if failed {
				name = policy.EchoInvites
			}
			raw := "/policy enable " + string(name)
			if err := interp.Submit(raw); err != nil {
				t.Fatal(err)
			}
			accepted := receiveEvent[interpreter.InputAccepted](t, events)
			if accepted.Raw != raw {
				t.Fatalf("acceptance=%#v", accepted)
			}
			command := receiveCommand(t, sess.executed)
			if command != (session.EnablePolicyCommand{Name: name}) {
				t.Fatalf("command=%#v", command)
			}
			changed := receiveEvent[interpreter.StateChanged](t, events)
			if len(changed.Snapshot.Room.Records) != 1 || changed.Snapshot.Room.Records[0].Text != raw {
				t.Fatalf("records=%#v", changed.Snapshot.Room.Records)
			}
			assertPolicySubmissionOutcome(t, events, raw, sess.executeErr)
			interp.Close()
			assertPolicySettled(t, sess, events)
		})
	}
}

func assertPolicySubmissionOutcome(t *testing.T, events <-chan interpreter.Event, raw string, want error) {
	t.Helper()
	if want != nil {
		result := receiveEvent[interpreter.SubmissionFailed](t, events)
		if result.Raw != raw || result.Operation != "policy" || result.Code != interpreter.ErrorExecutionFailed || !errors.Is(result.Err, want) {
			t.Fatalf("failure=%#v", result)
		}
		return
	}
	if result := receiveEvent[interpreter.SubmissionSucceeded](t, events); result.Raw != raw {
		t.Fatalf("success=%#v", result)
	}
}

func assertPolicySettled(t *testing.T, sess *recordingSession, events <-chan interpreter.Event) {
	t.Helper()
	for len(events) > 0 {
		if event := <-events; event != nil {
			if _, ok := event.(interpreter.StateChanged); !ok {
				t.Fatalf("extra terminal event=%#v", event)
			}
		}
	}
	assertNotExecuted(t, sess.executed)
}
