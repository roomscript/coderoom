package monolith

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

var errLoopAlreadyActive = errors.New("a loop is already active")

// Loop algorithm: prepare the condition, send a turn, wait for the participant,
// evaluate the condition asynchronously, then repeat or finish within the bound.
func (w *loopWorkflow) start(
	raw string,
	statement promptlang.Loop,
	commands *promptlang.Registry,
) instructionSequence {
	sequence := acceptedInputSequence(raw)
	if w.active != nil {
		sequence = append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "loop", Code: ErrorExecutionFailed, Err: errLoopAlreadyActive,
		}})
		return sequence
	}
	definition, err := commands.Resolve(promptlang.UserCommand{Name: statement.Condition})
	if err != nil {
		sequence = append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "loop condition /" + statement.Condition.Value,
			Code: ErrorExecutionFailed, Err: err,
		}})
		return sequence
	}
	body, err := promptlang.RequireShell(definition)
	if err != nil {
		return append(sequence, publishEventInstruction{event: SubmissionFailed{Raw: raw, Operation: "loop condition /" + statement.Condition.Value, Code: ErrorExecutionFailed, Err: err}})
	}
	w.nextGeneration++
	w.active = &loopState{
		generation:        w.nextGeneration,
		raw:               raw,
		statement:         statement,
		body:              body.Value,
		phase:             loopPlanningParticipant,
		submissionPending: true,
	}
	w.active.pending = w.nextRef(w.active.generation)
	sequence = append(sequence, resolveParticipantInstruction{target: w.active.pending, name: statement.Participant.Value})
	return sequence
}

func (w *loopWorkflow) startParticipantTurn(prompt string) executeSessionInstruction {
	state := w.active
	state.phase = loopDispatchingParticipant
	state.pending = w.nextRef(state.generation)
	return executeSessionInstruction{
		target:  state.pending,
		request: state.participantRequest(prompt),
	}
}

func (w *loopWorkflow) handleSessionEvent(event session.Event) instructionSequence {
	if w.active == nil {
		return nil
	}
	if w.active.phase == loopDispatchingParticipant {
		w.active.retainDispatchTerminalEvent(event)
		return nil
	}
	switch event := event.(type) {
	case session.AgentStopped:
		if event.Alias == w.active.primary {
			return w.finish("[loop] stopped: participant @" + event.Alias.Name + " stopped")
		}
	case session.AgentCrashed:
		if event.Alias == w.active.primary {
			return w.finish("[loop] stopped: participant @" + event.Alias.Name + " crashed")
		}
	}
	if w.active.phase != loopWaitingForParticipant {
		return nil
	}
	return w.resumeOnParticipantEvent(event)
}

func (w *loopWorkflow) resumeOnParticipantEvent(event session.Event) instructionSequence {
	status, ok := event.(session.ParticipantStatusChanged)
	if !ok || status.Alias != w.active.primary || status.To != participant.StatusIdle {
		return nil
	}
	return instructionSequence{w.startConditionEvaluation()}
}

// finishParticipantDelivery consumes the synchronous command result after its
// causal events have been applied. A successful delivery starts the actual wait.
func (w *loopWorkflow) finishParticipantDelivery(outcome sessionOutcome) instructionSequence {
	if !w.matches(outcome.target, loopDispatchingParticipant) {
		return nil
	}
	state := w.active
	state.pending = workflowRef{}
	if !state.participantTurnStarted(outcome.routing) {
		return w.finishDispatch("[loop] stopped: participant turn could not start")
	}
	if state.dispatchTerminalStatus != "" {
		return w.finishDispatch(state.dispatchTerminalStatus)
	}
	return w.waitForParticipant()
}

// waitForParticipant suspends after delivery. Later participant lifecycle events
// resume the loop; delivery itself does not mean the turn is complete.
func (w *loopWorkflow) waitForParticipant() instructionSequence {
	state := w.active
	state.turns++
	state.phase = loopWaitingForParticipant
	sequence := loopStatusSequence(fmt.Sprintf("[loop] turn %d/%d sent to @%s",
		state.turns, state.statement.MaxTurns.Value, state.statement.Participant.Value))
	state.appendSubmissionSuccess(&sequence)
	return sequence
}

// startConditionEvaluation suspends while the asynchronous shell command runs.
func (w *loopWorkflow) startConditionEvaluation() startShellInstruction {
	state := w.active
	state.phase = loopEvaluating
	state.pending = w.nextRef(state.generation)
	return startShellInstruction{target: state.pending, request: state.conditionRequest()}
}

func (w *loopWorkflow) finishDispatch(message string) instructionSequence {
	sequence := loopStatusSequence(message)
	w.active.appendSubmissionSuccess(&sequence)
	w.active = nil
	return sequence
}

// resumeOnConditionResult resumes after shell execution. Stale results are still
// observed, but cannot advance the current loop.
func (w *loopWorkflow) resumeOnConditionResult(outcome shellOutcome) instructionSequence {
	sequence := shellObservationSequence(outcome)
	if !w.matches(outcome.target, loopEvaluating) {
		return sequence
	}
	state := w.active
	state.pending = workflowRef{}
	sequence.append(w.advanceAfterCondition(outcome.result))
	sequence = append(sequence, requestSnapshotInstruction{})
	return sequence
}

func (w *loopWorkflow) finish(message string) instructionSequence {
	w.active = nil
	return loopStatusSequence(message)
}

func (w *loopWorkflow) advanceAfterCondition(result shell.Result) instructionSequence {
	state := w.active
	switch result.Status {
	case shell.StatusSuccess:
		return w.finish("[loop] condition /" + state.statement.Condition.Value + " succeeded")
	case shell.StatusCancelled:
		return w.finish("[loop] condition /" + state.statement.Condition.Value + " cancelled")
	case shell.StatusFailure:
		if state.turns >= state.statement.MaxTurns.Value {
			return w.finish(fmt.Sprintf("[loop] reached /max %d; condition /%s still failing",
				state.statement.MaxTurns.Value, state.statement.Condition.Value))
		}
		return instructionSequence{w.startParticipantTurn(formatLoopPrompt(state.statement, result))}
	}
	return nil
}

func (w *loopWorkflow) prepareParticipant(result participantAliasResult) instructionSequence {
	if !w.matches(result.target, loopPlanningParticipant) {
		return nil
	}
	w.active.primary = result.alias
	return instructionSequence{w.startParticipantTurn(w.active.statement.Prompt.Value)}
}
