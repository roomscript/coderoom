package interpreter

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func planStageForTest(t *testing.T, workflow *stageWorkflow, statement promptlang.Statement, targets []string, states []participantState) instructionSequence {
	t.Helper()
	sequence := workflow.start("hello", statement)
	if send, ok := statement.(promptlang.Send); ok {
		sess := session.New()
		t.Cleanup(sess.Shutdown)
		request := sequence[0].(prepareSendInstruction)
		return workflow.handleCompletion(sendPlanResult{target: request.target, plan: sess.CreateParticipantSendPlan(send.Alias.Value), targets: targets, participants: states})
	}
	sequence = acceptSuppliedStagePlan(t, workflow, sequence, targets)
	read := sequence[0].(readParticipantStateInstruction)
	return workflow.handleCompletion(participantStateResult{target: read.target, readinessRequirements: states})
}

func TestStageWorkflow_waitsForTemporaryStates(t *testing.T) {
	for _, status := range []participant.Status{participant.StatusStarting, participant.StatusAttached, participant.StatusPreparing, participant.StatusKeepalive} {
		for _, statement := range []promptlang.Statement{promptlang.Send{Alias: located("ben"), Text: located("hello")}, promptlang.Broadcast{Text: located("hello")}} {
			t.Run(fmt.Sprintf("%s/%T", status, statement), func(t *testing.T) {
				workflow := stageWorkflow{}
				planStageForTest(t, &workflow, statement, []string{"ben"}, []participantState{{alias: "ben", status: status}})
				if stage := workflow.snapshot(); stage == nil || !slices.Equal(stage.NotReadyAliases, []string{"ben"}) {
					t.Fatalf("stage = %#v", stage)
				}
				sequence := workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ben", From: status, To: participant.StatusIdle})
				if status == participant.StatusStarting || status == participant.StatusAttached {
					assertNoStageDispatch(t, sequence)
					sequence = workflow.handleSessionEvent(session.AgentReady{Alias: "ben"})
				}
				dispatch, ok := sequence[0].(executeSessionInstruction)
				if !ok || !slices.Equal(stageDispatchRecipients(t, dispatch), []string{"ben"}) {
					t.Fatalf("dispatch = %#v", sequence)
				}
			})
		}
	}
}

func TestStageWorkflow_distinguishesMissingAndCrashed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		states []participantState
		want   error
	}{
		{name: "missing", want: errStageTargetUnavailable},
		{name: "crashed", states: []participantState{{alias: "ben", status: participant.StatusCrashed}}, want: errStageTargetCrashed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			workflow := stageWorkflow{}
			sequence := planStageForTest(t, &workflow, promptlang.Send{Alias: located("ben"), Text: located("hello")}, []string{"ben"}, tt.states)
			failed := sequence[len(sequence)-1].(publishEventInstruction).event.(SubmissionFailed)
			if !errors.Is(failed.Err, tt.want) || workflow.pending() {
				t.Fatalf("failure = %#v", failed)
			}
		})
	}
}

func TestStageWorkflow_startupDepartureDiscards(t *testing.T) {
	for _, event := range []session.Event{session.AgentCrashed{Alias: "ben"}, session.AgentStopped{Alias: "ben"}} {
		workflow := stageWorkflow{}
		planStageForTest(t, &workflow, promptlang.Send{Alias: located("ben"), Text: located("hello")}, []string{"ben"}, []participantState{{alias: "ben", status: participant.StatusStarting}})
		sequence := workflow.handleSessionEvent(event)
		if workflow.pending() {
			t.Fatal("stage survived departure")
		}
		discarded := sequence[1].(publishEventInstruction).event.(StagedInputDiscarded)
		if discarded.Reason == "" {
			t.Fatal("discard had no explanation")
		}
	}
}

func TestStageWorkflow_interruptSkipsStartupAndMaintenance(t *testing.T) {
	workflow := stageWorkflow{}
	planStageForTest(t, &workflow, promptlang.Broadcast{Text: located("hello")}, []string{"ada", "ben", "cat"}, []participantState{
		{alias: "ada", status: participant.StatusWorking}, {alias: "ben", status: participant.StatusStarting}, {alias: "cat", status: participant.StatusKeepalive},
	})
	if !slices.Equal(workflow.snapshot().Interruptible, []string{"ada"}) {
		t.Fatalf("stage = %#v", workflow.snapshot())
	}
	sequence, ok := workflow.interruptAndDispatch()
	if !ok || len(sequence) != 2 {
		t.Fatalf("interrupt = %#v, %v", sequence, ok)
	}
	cancel := sequence[0].(executeSessionInstruction)
	if cancel.request != (cancelRequest{alias: "ada"}) {
		t.Fatalf("request = %#v", cancel.request)
	}
	workflow.handleCompletion(sessionOutcome{target: cancel.target})
	workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ada", To: participant.StatusIdle})
	workflow.handleSessionEvent(session.AgentReady{Alias: "ben"})
	if !workflow.pending() {
		t.Fatal("dispatched while keepalive still running")
	}
	sequence = workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "cat", To: participant.StatusIdle})
	if _, ok := sequence[0].(executeSessionInstruction); !ok {
		t.Fatalf("dispatch = %#v", sequence)
	}
}

func TestSubmitContract_startingBroadcastKeepsFrozenRecipients(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.readinessRequirements = []participant.Participant{{View: participant.View{Alias: "ben", Status: participant.StatusStarting}}}
	mustSubmit(t, interp.Submit("hello"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	assertNoSubmitExecution(t, sess.executed)
	interp.executor.recordSessionEvent(session.AgentReady{Alias: "later"})
	interp.executor.recordSessionEvent(session.AgentReady{Alias: "ben"})
	command := receiveSubmitCommand(t, sess.executed).(session.BroadcastCommand)
	if !slices.Equal(command.Aliases, []string{"ben"}) {
		t.Fatalf("recipients = %v", command.Aliases)
	}
}

func assertNoStageDispatch(t *testing.T, sequence instructionSequence) {
	t.Helper()
	for _, instruction := range sequence {
		if _, ok := instruction.(executeSessionInstruction); ok {
			t.Fatal("dispatched before AgentReady")
		}
	}
}

func TestStageWorkflow_startupStageCanBeEditedOrDiscarded(t *testing.T) {
	for _, edit := range []bool{true, false} {
		t.Run(fmt.Sprintf("edit=%v", edit), func(t *testing.T) {
			workflow := stageWorkflow{}
			planStageForTest(t, &workflow, promptlang.Send{Alias: located("ben"), Text: located("hello")}, []string{"ben"}, []participantState{{alias: "ben", status: participant.StatusStarting}})
			if edit {
				_, raw, ok := workflow.takeForEdit()
				if !ok || raw != "hello" {
					t.Fatalf("draft = %q, %v", raw, ok)
				}
			} else {
				if _, ok := workflow.discard(); !ok {
					t.Fatal("startup stage could not be discarded")
				}
			}
			if workflow.pending() {
				t.Fatal("stage remained after edit/discard")
			}
		})
	}
}
