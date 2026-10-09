package interpreter

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

func TestStageWorkflow_freezesSendPlanAndDispatchesWhenReady(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("@ada hello", promptlang.Send{Alias: "ada", Text: "hello"})
	planRequest := sequence[0].(prepareSendInstruction)

	sequence = workflow.handleCompletion(sendPlanResult{
		target: planRequest.target, targets: []string{"ada", "turing"},
		participants: []participantState{
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
	request, ok := dispatch.request.(executePlannedParticipantSendRequest)
	if !ok || request.message != "hello" || request.notice != "@ada: hello" {
		t.Fatalf("request = %#v", dispatch.request)
	}
	accepted := sequence[0].(publishEventInstruction).event.(InputAccepted)
	if !slices.Equal(accepted.Routing, []string{"ada", "turing"}) {
		t.Fatalf("routing = %v, want frozen plan targets", accepted.Routing)
	}
}

func TestStageWorkflow_copiesSuppliedPlanTargets(t *testing.T) {
	tests := []struct {
		name      string
		statement promptlang.Statement
		targets   []string
	}{
		{name: "send", statement: promptlang.Send{Alias: "ada", Text: "hello"}, targets: []string{"ada"}},
		{name: "broadcast", statement: promptlang.Broadcast{Text: "hello"}, targets: []string{"ada", "turing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflow := stageWorkflow{}
			supplied := slices.Clone(tt.targets)
			planStageForTest(t, &workflow, tt.statement, supplied, []participantState{
				{alias: "ada", status: participant.StatusWorking},
				{alias: "turing", status: participant.StatusIdle},
			})
			// Mutating producer-owned routing after preparation cannot change the stage.
			for index := range supplied {
				supplied[index] = "changed"
			}
			snapshot := workflow.snapshot()
			if snapshot == nil || snapshot.Phase != StagePhasePending || !slices.Equal(snapshot.Routing, tt.targets) {
				t.Fatalf("stage = %#v, want pending routing %v", snapshot, tt.targets)
			}
			sequence := workflow.handleSessionEvent(session.ParticipantStatusChanged{
				Alias: "ada", To: participant.StatusIdle,
			})
			dispatch := sequence[0].(executeSessionInstruction)
			recipients := stageDispatchRecipients(t, dispatch)
			if !slices.Equal(recipients, tt.targets) {
				t.Fatalf("dispatch recipients = %v, want %v", recipients, tt.targets)
			}
			if !slices.Equal(workflow.snapshot().Routing, tt.targets) {
				t.Fatalf("dispatch routing = %v, want %v", workflow.snapshot().Routing, tt.targets)
			}
		})
	}
}

func acceptSuppliedStagePlan(t *testing.T, workflow *stageWorkflow, sequence instructionSequence, targets []string) instructionSequence {
	t.Helper()
	switch request := sequence[0].(type) {
	case planBroadcastInstruction:
		return workflow.handleCompletion(broadcastPlanResult{target: request.target, targets: targets})
	default:
		t.Fatalf("plan instruction = %T", request)
		return nil
	}
}

func stageDispatchRecipients(t *testing.T, dispatch executeSessionInstruction) []string {
	t.Helper()
	switch request := dispatch.request.(type) {
	case executePlannedParticipantSendRequest:
		return request.plan.Targets()
	case broadcastRequest:
		return request.aliases
	default:
		t.Fatalf("dispatch request = %T", request)
		return nil
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
		readinessRequirements: []participantState{
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
		!slices.Equal(snapshot.NotReadyAliases, []string{"ada"}) {
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
		readinessRequirements: []participantState{
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
	if snapshot := workflow.snapshot(); !slices.Equal(snapshot.NotReadyAliases, []string{"ada"}) {
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
		readinessRequirements: []participantState{
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
	plan := sequence[0].(prepareSendInstruction)
	workflow.handleCompletion(sendPlanResult{
		target: plan.target, targets: []string{"ada"},
		participants: []participantState{{alias: "ada", status: participant.StatusWorking}},
	})

	sequence = workflow.handleSessionEvent(session.AgentCrashed{Alias: "ada"})
	record := sequence[0].(appendRecordInstruction).record
	if record.Kind != room.KindSystem || workflow.pending() {
		t.Fatalf("record = %#v, pending = %v", record, workflow.pending())
	}
	discarded := sequence[1].(publishEventInstruction).event.(StagedInputDiscarded)
	if discarded.Raw != "@ada hello" ||
		discarded.Reason != "staged message discarded: no active targets" {
		t.Fatalf("discarded = %#v", discarded)
	}
}

func TestStageWorkflow_namesDepartedSendTargetWhenListenerRemains(t *testing.T) {
	workflow := stageWorkflow{}
	sequence := workflow.start("@ada hello", promptlang.Send{Alias: "ada", Text: "hello"})
	plan := sequence[0].(prepareSendInstruction)
	workflow.handleCompletion(sendPlanResult{
		target: plan.target, targets: []string{"ada", "turing"},
		participants: []participantState{
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
	session.readinessRequirements = []participant.Participant{
		{View: participant.View{Alias: "turing", Status: participant.StatusIdle, StartupReady: true}},
		{View: participant.View{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}},
	}
	executor := interpreterExecutor{session: session}

	if got := executor.planBroadcast(); !slices.Equal(got, []string{"ada", "turing"}) {
		t.Fatalf("routing = %v, want [turing]", got)
	}
}

func TestStageWorkflow_keepsHandoffPendingForSourceResolution(t *testing.T) {
	workflow := stageWorkflow{}
	statement := promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"}
	sequence := workflow.start("/handoff ada turing", statement)
	request := sequence[0].(readParticipantStateInstruction)
	sequence = workflow.handleCompletion(participantStateResult{
		target: request.target,
		readinessRequirements: []participantState{
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
	} else if !slices.Equal(snapshot.NotReadyAliases, []string{"grace"}) {
		t.Fatalf("handoff readinessRequirements = %#v, want busy bystander", snapshot)
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
		readinessRequirements: []participantState{
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
		!slices.Equal(request.requiredReadyAliases, []string{"ada", "turing"}) {
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
		readinessRequirements: []participantState{
			{alias: "ada", status: participant.StatusWorking},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	sequence = workflow.handleSessionEvent(session.AgentStopped{Alias: "turing"})
	record := sequence[0].(appendRecordInstruction).record
	if record.Kind != room.KindSystem || workflow.pending() {
		t.Fatalf("record = %#v, pending = %v", record, workflow.pending())
	}
	discarded := sequence[1].(publishEventInstruction).event.(StagedInputDiscarded)
	if discarded.Raw != "/handoff ada turing" ||
		discarded.Reason != `staged message discarded: "turing" is no longer available` {
		t.Fatalf("discarded = %#v", discarded)
	}
}

func TestStageWorkflow_handoffIgnoresBusyLateJoiner(t *testing.T) {
	workflow := stageWorkflow{}
	statement := promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"}
	sequence := workflow.start("/handoff ada turing", statement)
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{
		target: read.target,
		readinessRequirements: []participantState{
			{alias: "ada", status: participant.StatusWorking},
			{alias: "turing", status: participant.StatusIdle},
		},
	})

	workflow.handleSessionEvent(session.ParticipantStatusChanged{
		Alias: "grace", To: participant.StatusWorking,
	})
	if snapshot := workflow.snapshot(); !slices.Equal(snapshot.NotReadyAliases, []string{"ada"}) {
		t.Fatalf("late join changed handoff readinessRequirements: %#v", snapshot)
	}
}

func TestStageWorkflow_capturesOnlyActiveHandoffCompletion(t *testing.T) {
	tests := []struct {
		name      string
		statement promptlang.Statement
		event     session.HandoffDelivered
		want      bool
	}{
		{
			name:      "matching handoff",
			statement: promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"},
			event:     session.HandoffDelivered{FromAlias: "ada", ToAlias: "turing"},
			want:      true,
		},
		{
			name:      "different handoff",
			statement: promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"},
			event:     session.HandoffDelivered{FromAlias: "grace", ToAlias: "turing"},
		},
		{
			name:      "different staged action",
			statement: promptlang.Broadcast{Text: "hello"},
			event:     session.HandoffDelivered{FromAlias: "ada", ToAlias: "turing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflow := stageWorkflow{}
			workflow.start("input", tt.statement)
			workflow.active.phase = stageDispatching

			workflow.handleSessionEvent(tt.event)

			if got := workflow.active.handoff != nil && workflow.active.handoff.completed != nil; got != tt.want {
				t.Fatalf("completion captured = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInterpreterModel_readsCanonicalHandoffSource(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	model.ApplySessionEvent(session.AgentReady{Alias: "ada"})
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
	plan := sequence[0].(prepareSendInstruction)
	sequence = workflow.handleCompletion(sendPlanResult{
		target: plan.target, targets: []string{"missing"},
	})

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
		target:                read.target,
		readinessRequirements: []participantState{{alias: "turing", status: participant.StatusIdle}},
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
	sess.readinessRequirements = []participant.Participant{{View: participant.View{
		Alias: "ada", Status: participant.StatusWorking, StartupReady: true,
	}}}

	mustSubmit(t, interp.Submit("hello"))
	accepted := receiveSubmitEvent[InputAccepted](t, events)
	if !slices.Equal(accepted.Routing, []string{"ada"}) {
		t.Fatalf("routing = %v, want [ada]", accepted.Routing)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	if changed.Snapshot.Stage == nil ||
		!slices.Equal(changed.Snapshot.Stage.NotReadyAliases, []string{"ada"}) {
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
	sess.readinessRequirements = []participant.Participant{{View: participant.View{
		Alias: "ada", Status: participant.StatusIdle, StartupReady: true,
	}}}

	mustSubmit(t, interp.Submit("hello"))
	receiveSubmitEvent[InputAccepted](t, events)
	command := receiveSubmitCommand(t, sess.executed)
	broadcast, ok := command.(session.BroadcastCommand)
	if !ok || broadcast.Text != "hello" || !slices.Equal(broadcast.Aliases, []string{"ada"}) {
		t.Fatalf("command = %#v", command)
	}
	dispatched := receiveSubmitEvent[StagedInputDispatched](t, events)
	if !slices.Equal(dispatched.Routing, []string{"ada"}) {
		t.Fatalf("routing = %v, want [ada]", dispatched.Routing)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	if changed.Snapshot.Stage != nil {
		t.Fatalf("stage remained after immediate dispatch: %#v", changed.Snapshot.Stage)
	}
}

func TestSubmitContract_lifecycleDispatchesPendingBroadcast(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.readinessRequirements = []participant.Participant{{View: participant.View{
		Alias: "ada", Status: participant.StatusWorking, StartupReady: true,
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
	dispatched := receiveSubmitEvent[StagedInputDispatched](t, events)
	if !slices.Equal(dispatched.Routing, []string{"ada"}) {
		t.Fatalf("routing = %v, want [ada]", dispatched.Routing)
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
	configureSuccessfulHandoff(sess)
	sess.readinessRequirements = []participant.Participant{
		{View: participant.View{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}},
		{View: participant.View{Alias: "turing", Status: participant.StatusIdle, StartupReady: true}},
	}
	seedCanonicalHandoffSource(t, interp, events)

	mustSubmit(t, interp.Submit("/handoff ada turing"))
	receiveSubmitEvent[InputAccepted](t, events)
	assertCanonicalHandoffDispatch(t, sess, events)
}

func configureSuccessfulHandoff(sess *submitContractSession) {
	sess.execute = func(command session.Command, observer session.Observer) {
		handoff, ok := command.(session.HandoffCommand)
		if !ok {
			return
		}
		observer.OnEvent(session.HandoffDelivered{
			FromAlias:         handoff.FromAlias,
			ToAlias:           handoff.ToAlias,
			SourceRecordIndex: handoff.Source.RecordIndex,
			Preview:           "[handoff ada -> turing]",
		})
	}
}

func seedCanonicalHandoffSource(t *testing.T, interp *Interpreter, events chan Event) {
	t.Helper()
	interp.executor.recordSessionEvent(session.AgentReady{Alias: "ada"})
	receiveSubmitEvent[StateChanged](t, events)
	interp.executor.recordSessionEvent(session.AgentReady{Alias: "turing"})
	receiveSubmitEvent[StateChanged](t, events)
	interp.executor.recordSessionEvent(session.AgentMessage{
		Alias:         "ada",
		Msg:           agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "finished"}},
		TurnCompleted: true,
		TurnID:        3,
	})
	receiveSubmitEvent[StateChanged](t, events)
}

func assertCanonicalHandoffDispatch(
	t *testing.T,
	sess *submitContractSession,
	events chan Event,
) {
	t.Helper()
	command := receiveSubmitCommand(t, sess.executed)
	handoff, ok := command.(session.HandoffCommand)
	if !ok || handoff.Source.Text != "finished" || handoff.Source.RecordIndex < 0 {
		t.Fatalf("command = %#v", command)
	}
	if !slices.Equal(handoff.RequiredReadyAliases, []string{"ada", "turing"}) {
		t.Fatalf("readinessRequirements = %v", handoff.RequiredReadyAliases)
	}
	dispatched := receiveSubmitEvent[StagedInputDispatched](t, events)
	if !slices.Equal(dispatched.Routing, []string{"turing"}) {
		t.Fatalf("routing = %v, want [turing]", dispatched.Routing)
	}
	completed := receiveSubmitEvent[HandoffCompleted](t, events)
	if completed.Preview != "[handoff ada -> turing]" {
		t.Fatalf("completed = %#v", completed)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	assertCanonicalHandoffSnapshot(t, changed.Snapshot)
}

func assertCanonicalHandoffSnapshot(t *testing.T, snapshot Snapshot) {
	t.Helper()
	if snapshot.Stage != nil {
		t.Fatalf("stage remained after handoff: %#v", snapshot.Stage)
	}
	records := snapshot.Room.Records
	if len(records) < 2 || records[len(records)-2].Text != "/handoff ada turing" ||
		records[len(records)-1].Text != "[handoff ada -> turing]" {
		t.Fatalf("handoff record order = %#v", records)
	}
}

func TestSubmitContract_failedHandoffDoesNotRecordInput(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.executeErr = errors.New("handoff failed")
	sess.readinessRequirements = []participant.Participant{
		{View: participant.View{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}},
		{View: participant.View{Alias: "turing", Status: participant.StatusIdle, StartupReady: true}},
	}
	interp.executor.recordSessionEvent(session.AgentReady{Alias: "ada"})
	receiveSubmitEvent[StateChanged](t, events)
	interp.executor.recordSessionEvent(session.AgentReady{Alias: "turing"})
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

// Test adapter for the typed completion entry points used by workflowCollection.
func (w *stageWorkflow) handleCompletion(completion any) instructionSequence {
	switch completion := completion.(type) {
	case sendPlanResult:
		return w.prepareSend(completion)
	case broadcastPlanResult:
		return w.handleBroadcastPlan(completion)
	case participantStateResult:
		return w.handleParticipantState(completion)
	case handoffSourceResult:
		return w.handleHandoffSource(completion)
	case sessionOutcome:
		return w.applySessionOutcome(completion)
	default:
		return nil
	}
}
