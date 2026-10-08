package interpreter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func startReadinessHandoff(workflow *stageWorkflow, sourceStatus participant.Status, bystanderStatus participant.Status) {
	sequence := workflow.start("/handoff ada turing", promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"})
	read := sequence[0].(readParticipantStateInstruction)
	workflow.handleCompletion(participantStateResult{target: read.target, readinessRequirements: []participantState{
		{alias: "ada", status: sourceStatus}, {alias: "turing", status: participant.StatusIdle}, {alias: "ben", status: bystanderStatus},
	}})
}

func TestStageWorkflow_handoffTemporarySourceReadsAfterReadiness(t *testing.T) {
	for _, status := range []participant.Status{participant.StatusStarting, participant.StatusAttached, participant.StatusKeepalive} {
		for _, hasOutput := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/output=%v", status, hasOutput), func(t *testing.T) {
				workflow := stageWorkflow{}
				startReadinessHandoff(&workflow, status, participant.StatusIdle)
				if workflow.active.handoff.sourceNeedsCompletion {
					t.Fatal("temporary state requires a user output event")
				}
				sequence := workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ada", From: status, To: participant.StatusIdle})
				if status != participant.StatusKeepalive {
					assertNoHandoffSourceRead(t, sequence)
					sequence = workflow.handleSessionEvent(session.AgentReady{Alias: "ada"})
				}
				read, ok := sequence[0].(readHandoffSourceInstruction)
				if !ok || read.alias != "ada" {
					t.Fatalf("source read = %#v", sequence)
				}
				assertReadinessHandoffSourceResult(t, &workflow, read, hasOutput)
			})
		}
	}
}

func assertNoHandoffSourceRead(t *testing.T, sequence instructionSequence) {
	t.Helper()
	for _, instruction := range sequence {
		if _, ok := instruction.(readHandoffSourceInstruction); ok {
			t.Fatal("source read before startup readiness")
		}
	}
}

func assertReadinessHandoffSourceResult(t *testing.T, workflow *stageWorkflow, read readHandoffSourceInstruction, hasOutput bool) {
	t.Helper()
	source := session.HandoffSource{Text: "previous output", RecordIndex: 4}
	sequence := workflow.handleCompletion(handoffSourceResult{target: read.target, source: source, ok: hasOutput})
	if !hasOutput {
		failed := sequence[len(sequence)-1].(publishEventInstruction).event.(OperationFailed)
		if !errors.Is(failed.Err, errNoHandoffSource) || workflow.pending() {
			t.Fatalf("failure = %#v, pending = %v", failed, workflow.pending())
		}
		return
	}
	dispatch := sequence[0].(executeSessionInstruction)
	request := dispatch.request.(handoffRequest)
	if request.source != source {
		t.Fatalf("handoff source = %#v", request.source)
	}
}

func TestStageWorkflow_handoffSourceEnteringKeepaliveDoesNotRequireOutput(t *testing.T) {
	workflow := stageWorkflow{}
	startReadinessHandoff(&workflow, participant.StatusIdle, participant.StatusWorking)
	workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ada", From: participant.StatusIdle, To: participant.StatusKeepalive})
	workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ben", From: participant.StatusWorking, To: participant.StatusIdle})
	if workflow.active.handoff.sourceNeedsCompletion {
		t.Fatal("keepalive requires a user output event")
	}
	sequence := workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ada", From: participant.StatusKeepalive, To: participant.StatusIdle})
	if _, ok := sequence[0].(readHandoffSourceInstruction); !ok {
		t.Fatalf("source read = %#v", sequence)
	}
}

func TestStageWorkflow_handoffPreparingSourceStillWaitsForOutput(t *testing.T) {
	workflow := stageWorkflow{}
	startReadinessHandoff(&workflow, participant.StatusPreparing, participant.StatusIdle)
	workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ada", From: participant.StatusPreparing, To: participant.StatusWorking})
	sequence := workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: "ada", From: participant.StatusWorking, To: participant.StatusIdle})
	assertNoHandoffSourceRead(t, sequence)
	sequence = workflow.handleSessionEvent(session.AgentMessage{Alias: "ada", TurnCompleted: true})
	if _, ok := sequence[0].(readHandoffSourceInstruction); !ok {
		t.Fatalf("source read = %#v", sequence)
	}
}
