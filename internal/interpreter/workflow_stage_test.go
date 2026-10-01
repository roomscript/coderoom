package interpreter

import (
	"errors"
	"slices"
	"testing"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/room"
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

	if len(sequence) != 2 {
		t.Fatalf("instructions = %d, want accepted input and dispatch", len(sequence))
	}
	dispatch, ok := sequence[1].(executeSessionInstruction)
	if !ok {
		t.Fatalf("instruction 1 = %T, want executeSessionInstruction", sequence[1])
	}
	request, ok := dispatch.request.(executePlannedSharedSendRequest)
	if !ok || request.directText != "hello" || request.listenersText != "@ada: hello" {
		t.Fatalf("request = %#v", dispatch.request)
	}
	accepted := sequence[0].(publishEventInstruction).event.(InputAccepted)
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

	if len(sequence) != 3 {
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

func TestStageWorkflow_lifecycleDispatchUsesFrozenBroadcastTargets(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("hello", promptlang.Broadcast{Text: "hello"})
	plan := sequence[0].(planBroadcastInstruction)
	sequence = workflow.handleCompletion(broadcastPlanResult{
		target: plan.target, targets: []string{"ada", "turing"},
	})
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target: read.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusWorking},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	sequence = workflow.handleSessionEvent(session.AgentMessage{
		Alias: "ada", TurnCompleted: true, TurnID: 6,
	})
	if len(sequence) != 0 {
		t.Fatalf("stale source completion produced instructions: %#v", sequence)
	}
	sequence = workflow.handleSessionEvent(session.ParticipantStatusChanged{
		Alias: "grace", To: participant.StatusWorking,
	})
	if snapshot := workflow.snapshot(); !slices.Equal(snapshot.Blocking, []string{"ada"}) {
		t.Fatalf("late join changed blocking aliases: %#v", snapshot)
	}
	if _, dispatched := sequence[0].(executeSessionInstruction); dispatched {
		t.Fatal("late join dispatched blocked submission")
	}

	sequence = workflow.handleSessionEvent(session.ParticipantStatusChanged{
		Alias: "ada", To: participant.StatusIdle,
	})
	dispatch, ok := sequence[0].(executeSessionInstruction)
	if !ok {
		t.Fatalf("instruction = %T, want executeSessionInstruction", sequence[0])
	}
	request := dispatch.request.(broadcastRequest)
	if !slices.Equal(request.aliases, []string{"ada", "turing"}) {
		t.Fatalf("dispatch aliases = %v, want frozen targets", request.aliases)
	}
}

func TestStageWorkflow_departedBroadcastTargetDoesNotBlockRemainingTargets(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("hello", promptlang.Broadcast{Text: "hello"})
	plan := sequence[0].(planBroadcastInstruction)
	sequence = workflow.handleCompletion(broadcastPlanResult{
		target: plan.target, targets: []string{"ada", "turing"},
	})
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target: read.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusWorking},
			{alias: "turing", status: participant.StatusWorking},
		},
	})

	workflow.handleSessionEvent(session.AgentStopped{Alias: "ada"})
	sequence = workflow.handleSessionEvent(session.ParticipantStatusChanged{
		Alias: "turing", To: participant.StatusIdle,
	})
	dispatch := sequence[0].(executeSessionInstruction)
	request := dispatch.request.(broadcastRequest)
	if !slices.Equal(request.aliases, []string{"turing"}) {
		t.Fatalf("dispatch aliases = %v, want [turing]", request.aliases)
	}
	if !slices.Equal(workflow.snapshot().Unavailable, []string{"ada"}) {
		t.Fatalf("stage = %#v", workflow.snapshot())
	}
}

func TestStageWorkflow_discardsSendWhenAddressedTargetDeparts(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("@ada hello", promptlang.Send{Alias: "ada", Text: "hello"})
	plan := sequence[0].(planSharedSendInstruction)
	sequence = workflow.handleCompletion(sharedSendPlanResult{
		target: plan.target, targets: []string{"ada"},
	})
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target:  read.target,
		barrier: []participantState{{alias: "ada", status: participant.StatusWorking}},
	})

	sequence = workflow.handleSessionEvent(session.AgentCrashed{Alias: "ada"})
	record := sequence[0].(appendRecordInstruction).record
	if record.Kind != room.KindSystem || workflow.pending() {
		t.Fatalf("record = %#v, pending = %v", record, workflow.pending())
	}
}

func TestStageWorkflow_namesDepartedSendTargetWhenListenerRemains(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("@ada hello", promptlang.Send{Alias: "ada", Text: "hello"})
	plan := sequence[0].(planSharedSendInstruction)
	sequence = workflow.handleCompletion(sharedSendPlanResult{
		target: plan.target, targets: []string{"ada", "turing"},
	})
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target: read.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusWorking},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	sequence = workflow.handleSessionEvent(session.AgentStopped{Alias: "ada"})
	record := sequence[0].(appendRecordInstruction).record
	discarded := sequence[1].(publishEventInstruction).event.(StagedInputDiscarded)
	if record.Text != `staged submission discarded: "ada" is no longer available` {
		t.Fatalf("record = %#v", record)
	}
	if discarded.Reason != `staged message discarded: "ada" is no longer available` {
		t.Fatalf("discard event = %#v", discarded)
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
	sequence = workflow.handleSessionEvent(session.AgentStopped{Alias: "grace"})
	if _, ok := sequence[0].(readHandoffSourceInstruction); !ok {
		t.Fatalf("instruction = %T, want source read after bystander departure", sequence[0])
	}
}

func TestStageWorkflow_handoffWaitsForSourceProjectionAndIdle(t *testing.T) {
	workflow := stageWorkflow{}
	statement := promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"}
	sequence := workflow.start("/handoff ada turing", statement)
	readParticipants := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target: readParticipants.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusWorking, turnID: 7},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	sequence = workflow.handleSessionEvent(session.ParticipantStatusChanged{
		Alias: "ada", From: participant.StatusWorking, To: participant.StatusIdle,
	})
	if _, read := sequence[0].(readHandoffSourceInstruction); read {
		t.Fatal("handoff read source before completed output projection")
	}
	sequence = workflow.handleSessionEvent(session.AgentMessage{
		Alias: "ada", TurnCompleted: true, TurnID: 7,
	})
	readSource, ok := sequence[0].(readHandoffSourceInstruction)
	if !ok || readSource.alias != "ada" {
		t.Fatalf("instruction = %#v, want source read for ada", sequence[0])
	}

	source := session.HandoffSource{Text: "finished", RecordIndex: 4}
	sequence = workflow.handleCompletion(handoffSourceResult{
		target: readSource.target, source: source, ok: true,
	})
	dispatch := sequence[0].(executeSessionInstruction)
	request := dispatch.request.(handoffRequest)
	if request.source != source ||
		!slices.Equal(request.idleAliases, []string{"ada", "turing"}) {
		t.Fatalf("request = %#v", request)
	}
}

func TestStageWorkflow_handoffDiscardsDepartedTarget(t *testing.T) {
	workflow := stageWorkflow{}
	statement := promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"}
	sequence := workflow.start("/handoff ada turing", statement)
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target: read.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusWorking},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	sequence = workflow.handleSessionEvent(session.AgentStopped{Alias: "turing"})
	record := sequence[0].(appendRecordInstruction).record
	if record.Kind != room.KindSystem || workflow.pending() {
		t.Fatalf("record = %#v, pending = %v", record, workflow.pending())
	}
}

func TestStageWorkflow_handoffIgnoresBusyLateJoiner(t *testing.T) {
	workflow := stageWorkflow{}
	statement := promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"}
	sequence := workflow.start("/handoff ada turing", statement)
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target: read.target,
		barrier: []participantState{
			{alias: "ada", status: participant.StatusWorking},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	workflow.handleSessionEvent(session.ParticipantStatusChanged{
		Alias: "grace", To: participant.StatusWorking,
	})
	if snapshot := workflow.snapshot(); !slices.Equal(snapshot.Blocking, []string{"ada"}) {
		t.Fatalf("late join changed handoff barrier: %#v", snapshot)
	}
}

func TestInterpreterModel_readsCanonicalHandoffSource(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	model.ApplySessionEvent(session.AgentStarted{Alias: "ada"})
	model.ApplySessionEvent(session.AgentMessage{
		Alias:         "ada",
		Msg:           agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "finished"}},
		TurnCompleted: true,
		TurnID:        3,
	})

	source, ok := model.ReadHandoffSource("ada")
	if !ok || source.Text != "finished" || source.RecordIndex != 1 {
		t.Fatalf("source = %#v, %v", source, ok)
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

func TestStageWorkflow_rejectsInitiallyUnavailableSendTarget(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("@missing hello", promptlang.Send{Alias: "missing", Text: "hello"})
	plan := sequence[0].(planSharedSendInstruction)
	sequence = workflow.handleCompletion(sharedSendPlanResult{
		target: plan.target, targets: []string{"missing"},
	})
	read := sequence[0].(readParticipantStateInstruction)
	sequence = workflow.handleCompletion(participantStateResult{target: read.target})

	failed := sequence[len(sequence)-1].(publishEventInstruction).event.(SubmissionFailed)
	if !errors.Is(failed.Err, errStageTargetUnavailable) || workflow.pending() {
		t.Fatalf("failure = %#v, pending = %v", failed, workflow.pending())
	}
}

func TestStageWorkflow_dispatchesRemainingBroadcastTargetAfterPlanningDeparture(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("hello", promptlang.Broadcast{Text: "hello"})
	plan := sequence[0].(planBroadcastInstruction)
	sequence = workflow.handleCompletion(broadcastPlanResult{
		target: plan.target, targets: []string{"ada", "turing"},
	})
	read := sequence[0].(readParticipantStateInstruction)
	sequence = workflow.handleCompletion(participantStateResult{
		target:  read.target,
		barrier: []participantState{{alias: "turing", status: participant.StatusIdle}},
	})

	dispatch, ok := sequence[1].(executeSessionInstruction)
	if !ok {
		t.Fatalf("instruction = %T, want executeSessionInstruction", sequence[1])
	}
	request := dispatch.request.(broadcastRequest)
	if !slices.Equal(request.aliases, []string{"turing"}) {
		t.Fatalf("dispatch aliases = %v, want [turing]", request.aliases)
	}
	if !slices.Equal(workflow.snapshot().Unavailable, []string{"ada"}) {
		t.Fatalf("stage = %#v", workflow.snapshot())
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

func TestSubmitContract_lifecycleDispatchesPendingBroadcast(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.barrier = []participant.Participant{{View: participant.View{
		Alias: "ada", Status: participant.StatusWorking,
	}}}

	mustSubmit(t, interp.Submit("hello"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	assertNoSubmitExecution(t, sess.executed)

	interp.executor.recordSessionEvent(session.ParticipantStatusChanged{
		Alias: "ada", From: participant.StatusWorking, To: participant.StatusIdle,
	})
	command := receiveSubmitCommand(t, sess.executed)
	broadcast, ok := command.(session.BroadcastCommand)
	if !ok || !slices.Equal(broadcast.Aliases, []string{"ada"}) {
		t.Fatalf("command = %#v", command)
	}
	changed := receiveSubmitEvent[StateChanged](t, events)
	if changed.Snapshot.Stage != nil {
		t.Fatalf("stage remained after dispatch: %#v", changed.Snapshot.Stage)
	}
	records := changed.Snapshot.Room.Records
	if len(records) != 1 || records[0].Text != "hello" ||
		!slices.Equal(records[0].Routing, []string{"ada"}) {
		t.Fatalf("records = %#v", records)
	}
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_handoffUsesCanonicalRoomSource(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.execute = func(command session.Command, observer session.Observer) {
		handoff, ok := command.(session.HandoffCommand)
		if !ok {
			return
		}
		observer.OnEvent(session.ContextHandoff{
			FromAlias: handoff.FromAlias,
			ToAlias:   handoff.ToAlias,
			Preview:   "[handoff ada -> turing]",
		})
	}
	sess.barrier = []participant.Participant{
		{View: participant.View{Alias: "ada", Status: participant.StatusIdle}},
		{View: participant.View{Alias: "turing", Status: participant.StatusIdle}},
	}
	interp.executor.recordSessionEvent(session.AgentStarted{Alias: "ada"})
	receiveSubmitEvent[StateChanged](t, events)
	interp.executor.recordSessionEvent(session.AgentStarted{Alias: "turing"})
	receiveSubmitEvent[StateChanged](t, events)
	interp.executor.recordSessionEvent(session.AgentMessage{
		Alias:         "ada",
		Msg:           agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "finished"}},
		TurnCompleted: true,
		TurnID:        3,
	})
	receiveSubmitEvent[StateChanged](t, events)

	mustSubmit(t, interp.Submit("/handoff ada turing"))
	receiveSubmitEvent[InputAccepted](t, events)
	command := receiveSubmitCommand(t, sess.executed)
	handoff, ok := command.(session.HandoffCommand)
	if !ok || handoff.Source.Text != "finished" || handoff.Source.RecordIndex < 0 {
		t.Fatalf("command = %#v", command)
	}
	if !slices.Equal(handoff.IdleAliases, []string{"ada", "turing"}) {
		t.Fatalf("barrier = %v", handoff.IdleAliases)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	if changed.Snapshot.Stage != nil {
		t.Fatalf("stage remained after handoff: %#v", changed.Snapshot.Stage)
	}
	records := changed.Snapshot.Room.Records
	if len(records) < 2 || records[len(records)-2].Text != "/handoff ada turing" ||
		records[len(records)-1].Text != "[handoff ada -> turing]" {
		t.Fatalf("handoff record order = %#v", records)
	}
}

func TestSubmitContract_failedHandoffDoesNotRecordInput(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.executeErr = errors.New("handoff failed")
	sess.barrier = []participant.Participant{
		{View: participant.View{Alias: "ada", Status: participant.StatusIdle}},
		{View: participant.View{Alias: "turing", Status: participant.StatusIdle}},
	}
	interp.executor.recordSessionEvent(session.AgentStarted{Alias: "ada"})
	receiveSubmitEvent[StateChanged](t, events)
	interp.executor.recordSessionEvent(session.AgentStarted{Alias: "turing"})
	receiveSubmitEvent[StateChanged](t, events)
	interp.executor.recordSessionEvent(session.AgentMessage{
		Alias: "ada",
		Msg: agent.Message{
			Mode: agent.ModeSingle, Content: agent.Output{Text: "finished"},
		},
		TurnCompleted: true,
		TurnID:        3,
	})
	receiveSubmitEvent[StateChanged](t, events)

	mustSubmit(t, interp.Submit("/handoff ada turing"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[SubmissionFailed](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	for _, record := range changed.Snapshot.Room.Records {
		if record.Text == "/handoff ada turing" {
			t.Fatalf("failed handoff recorded input: %#v", changed.Snapshot.Room.Records)
		}
	}
}
