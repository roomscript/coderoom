package interpreter

import (
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

// Preparation facts return synchronously on the serialized runner.
type preparationResult interface{ preparationResult() }

// Execution outcomes arrive after causal session events or asynchronous shell work.
type executionOutcome interface{ executionOutcome() }

type sessionOutcome struct {
	routing session.RoutingResult
	target  workflowRef
	err     error
}

type shellOutcome struct {
	target  workflowRef
	request shellRequest
	result  shell.Result
	cwd     string
}

type submissionOutcome struct {
	statement promptlang.ParsedStatement
	raw       string
	operation string
	err       error
}

type participantsResult struct {
	statement    promptlang.ParsedStatement
	raw          string
	participants []participant.View
}

type sendPlanResult struct {
	participants []participantState
	target       workflowRef
	plan         session.ParticipantSendPlan
	targets      []string
}

type broadcastPlanResult struct {
	target  workflowRef
	targets []string
}

type participantStateResult struct {
	target                workflowRef
	readinessRequirements []participantState
}

type handoffSourceResult struct {
	target workflowRef
	source session.HandoffSource
	ok     bool
}

func (sessionOutcome) executionOutcome()          {}
func (shellOutcome) executionOutcome()            {}
func (submissionOutcome) executionOutcome()       {}
func (participantsResult) preparationResult()     {}
func (sendPlanResult) preparationResult()         {}
func (broadcastPlanResult) preparationResult()    {}
func (participantStateResult) preparationResult() {}
func (handoffSourceResult) preparationResult()    {}
