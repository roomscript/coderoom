package interpreter

import (
	"slices"
	"testing"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

func TestStageWorkflow_freezesSendPlanAndDispatchesWhenReady(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("@ada hello", promptlang.Send{Alias: "ada", Text: "hello"})
	planRequest := sequence[0].(planSharedSendInstruction)

	sequence = workflow.handleCompletion(sharedSendPlanResult{
		target: planRequest.target, targets: []string{"ada", "turing"},
	})
	stateRequest := sequence[0].(readParticipantStateInstruction)
	sequence = workflow.handleCompletion(participantStateResult{
		target: stateRequest.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusIdle},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	if len(sequence) != 3 {
		t.Fatalf("instructions = %d, want accepted input and dispatch", len(sequence))
	}
	dispatch, ok := sequence[2].(executeSessionInstruction)
	if !ok {
		t.Fatalf("instruction 2 = %T, want executeSessionInstruction", sequence[2])
	}
	request, ok := dispatch.request.(executePlannedSharedSendRequest)
	if !ok || request.directText != "hello" || request.listenersText != "@ada: hello" {
		t.Fatalf("request = %#v", dispatch.request)
	}
	accepted := sequence[1].(publishEventInstruction).event.(InputAccepted)
	if !slices.Equal(accepted.Routing, []string{"ada", "turing"}) {
		t.Fatalf("routing = %v, want frozen plan targets", accepted.Routing)
	}
}

func TestStageWorkflow_stagesBusyBroadcastWithDetachedSnapshot(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("hello", promptlang.Broadcast{Text: "hello"})
	planRequest := sequence[0].(planBroadcastInstruction)
	sequence = workflow.handleCompletion(broadcastPlanResult{
		target: planRequest.target, targets: []string{"ada", "turing"},
	})
	request := sequence[0].(readParticipantStateInstruction)
	sequence = workflow.handleCompletion(participantStateResult{
		target: request.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusWorking, turnID: 7},
			{alias: "turing", status: participant.StatusIdle},
			{alias: "grace", status: participant.StatusWorking, turnID: 8},
		},
	})

	if len(sequence) != 4 {
		t.Fatalf("instructions = %d, want accepted input, snapshot, and success", len(sequence))
	}
	snapshot := workflow.snapshot()
	if snapshot.Raw != "hello" || snapshot.Phase != StagePhasePending {
		t.Fatalf("stage = %#v", snapshot)
	}
	if !slices.Equal(snapshot.Routing, []string{"ada", "turing"}) ||
		!slices.Equal(snapshot.Blocking, []string{"ada"}) {
		t.Fatalf("stage routing/blocking = %#v", snapshot)
	}
	snapshot.Routing[0] = "changed"
	if workflow.snapshot().Routing[0] != "ada" {
		t.Fatal("stage snapshot was not detached")
	}
}

func TestInterpreterExecutor_planBroadcastSortsDetachedAliases(t *testing.T) {
	session := newSubmitContractSession()
	session.barrier = []participant.Participant{
		{View: participant.View{Alias: "turing"}},
		{View: participant.View{Alias: "ada"}},
	}
	executor := interpreterExecutor{session: session}

	if got := executor.planBroadcast(); !slices.Equal(got, []string{"ada", "turing"}) {
		t.Fatalf("routing = %v, want [ada turing]", got)
	}
}

func TestStageWorkflow_keepsHandoffPendingForSourceResolution(t *testing.T) {
	workflow := stageWorkflow{}
	statement := promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"}
	sequence := workflow.start("/handoff ada turing", statement)
	request := sequence[0].(readParticipantStateInstruction)
	sequence = workflow.handleCompletion(participantStateResult{
		target: request.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusIdle},
			{alias: "turing", status: participant.StatusIdle},
			{alias: "grace", status: participant.StatusWorking},
		},
	})

	if _, dispatched := sequence[len(sequence)-1].(executeSessionInstruction); dispatched {
		t.Fatal("handoff dispatched before canonical source resolution")
	}
	if snapshot := workflow.snapshot(); snapshot == nil ||
		!slices.Equal(snapshot.Routing, []string{"ada", "turing"}) {
		t.Fatalf("stage = %#v", snapshot)
	} else if !slices.Equal(snapshot.Blocking, []string{"grace"}) {
		t.Fatalf("handoff barrier = %#v, want busy bystander", snapshot)
	}
}

func TestStageWorkflow_failsBroadcastWithoutTargets(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("hello", promptlang.Broadcast{Text: "hello"})
	planRequest := sequence[0].(planBroadcastInstruction)
	sequence = workflow.handleCompletion(broadcastPlanResult{target: planRequest.target})
	request := sequence[0].(readParticipantStateInstruction)
	sequence = workflow.handleCompletion(participantStateResult{target: request.target})

	failed := sequence[len(sequence)-1].(publishEventInstruction).event.(SubmissionFailed)
	if failed.Err != errNoStageTargets || workflow.pending() {
		t.Fatalf("failure = %#v, pending = %v", failed, workflow.pending())
	}
}

func TestSubmitContract_stageOwnsPendingSnapshotAndPreParseGate(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.barrier = []participant.Participant{{View: participant.View{
		Alias: "ada", Status: participant.StatusWorking,
	}}}
	sess.routable = append([]participant.Participant(nil), sess.barrier...)

	mustSubmit(t, interp.Submit("hello"))
	accepted := receiveSubmitEvent[InputAccepted](t, events)
	if !slices.Equal(accepted.Routing, []string{"ada"}) {
		t.Fatalf("routing = %v, want [ada]", accepted.Routing)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	if changed.Snapshot.Stage == nil ||
		!slices.Equal(changed.Snapshot.Stage.Blocking, []string{"ada"}) {
		t.Fatalf("stage = %#v", changed.Snapshot.Stage)
	}
	assertNoSubmitExecution(t, sess.executed)

	mustSubmit(t, interp.Submit("/definitely-invalid argument"))
	rejected := receiveSubmitEvent[InputRejected](t, events)
	if rejected.Code != ErrorStagePending || rejected.Err != ErrStagePending {
		t.Fatalf("rejection = %#v", rejected)
	}
	assertNoSubmitExecution(t, sess.executed)
}

func TestSubmitContract_immediatelyDispatchesReadyBroadcast(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.barrier = []participant.Participant{{View: participant.View{
		Alias: "ada", Status: participant.StatusIdle,
	}}}
	sess.routable = append([]participant.Participant(nil), sess.barrier...)

	mustSubmit(t, interp.Submit("hello"))
	receiveSubmitEvent[InputAccepted](t, events)
	command := receiveSubmitCommand(t, sess.executed)
	broadcast, ok := command.(session.BroadcastCommand)
	if !ok || broadcast.Text != "hello" || !slices.Equal(broadcast.Aliases, []string{"ada"}) {
		t.Fatalf("command = %#v", command)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	if changed.Snapshot.Stage != nil {
		t.Fatalf("stage remained after immediate dispatch: %#v", changed.Snapshot.Stage)
	}
}
