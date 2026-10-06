package interpreter

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

var errLoopAlreadyActive = errors.New("a loop is already active")

type loopPhase uint8

const (
	loopDispatchingParticipant loopPhase = iota
	loopWaitingForParticipant
	loopEvaluating
)

type loopState struct {
	generation             uint64
	raw                    string
	statement              promptlang.Loop
	body                   promptlang.Shell
	phase                  loopPhase
	turns                  int
	pending                workflowRef
	submissionPending      bool
	dispatchTerminalStatus string
}

type loopWorkflow struct {
	active         *loopState
	nextGeneration uint64
	nextRequestID  uint64
}

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
	body, err := commands.Resolve(promptlang.CommandInvocation{Name: statement.Condition})
	if err != nil {
		sequence = append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "loop condition /" + statement.Condition,
			Code: ErrorExecutionFailed, Err: err,
		}})
		return sequence
	}
	w.nextGeneration++
	w.active = &loopState{
		generation:        w.nextGeneration,
		raw:               raw,
		statement:         statement,
		body:              body,
		phase:             loopDispatchingParticipant,
		submissionPending: true,
	}
	sequence = append(sequence, w.dispatchInstruction(statement.Prompt))
	return sequence
}

func acceptedInputSequence(raw string) instructionSequence {
	return instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindUserInput, Text: raw}},
		publishEventInstruction{event: InputAccepted{Raw: raw}},
	}
}

func (w *loopWorkflow) dispatchInstruction(prompt string) executeSessionInstruction {
	state := w.active
	state.phase = loopDispatchingParticipant
	state.pending = w.nextRef(state.generation)
	return executeSessionInstruction{
		target: state.pending,
		request: planAndExecuteSharedSendRequest{
			alias:         state.statement.Participant,
			directText:    prompt,
			listenersText: fmt.Sprintf("@%s: %s", state.statement.Participant, prompt),
		},
	}
}

func (w *loopWorkflow) nextRef(generation uint64) workflowRef {
	w.nextRequestID++
	return workflowRef{kind: workflowLoop, generation: generation, requestID: w.nextRequestID}
}

func (w *loopWorkflow) handleSessionEvent(event session.Event) instructionSequence {
	if w.active == nil {
		return nil
	}
	if w.active.phase == loopDispatchingParticipant {
		w.retainDispatchTerminalEvent(event)
		return nil
	}
	if w.active.phase != loopWaitingForParticipant {
		return nil
	}
	return w.handleWaitingEvent(event)
}

func (w *loopWorkflow) handleWaitingEvent(event session.Event) instructionSequence {
	alias := w.active.statement.Participant
	switch event := event.(type) {
	case session.ParticipantStatusChanged:
		if event.Alias != alias || event.To != participant.StatusIdle {
			return nil
		}
		w.active.phase = loopEvaluating
		w.active.pending = w.nextRef(w.active.generation)
		return instructionSequence{startShellInstruction{
			target: w.active.pending,
			request: shellRequest{
				command: "/" + w.active.statement.Condition,
				program: w.active.body.Program,
			},
		}}
	case session.AgentStopped:
		if event.Alias == alias {
			return w.finish("[loop] stopped: participant @" + alias + " stopped")
		}
	case session.AgentCrashed:
		if event.Alias == alias {
			return w.finish("[loop] stopped: participant @" + alias + " crashed")
		}
	}
	return nil
}

func (w *loopWorkflow) retainDispatchTerminalEvent(event session.Event) {
	alias := w.active.statement.Participant
	switch event := event.(type) {
	case session.AgentStopped:
		if event.Alias == alias {
			w.active.dispatchTerminalStatus = "[loop] stopped: participant @" + alias + " stopped"
		}
	case session.AgentCrashed:
		if event.Alias == alias {
			w.active.dispatchTerminalStatus = "[loop] stopped: participant @" + alias + " crashed"
		}
	}
}

func (w *loopWorkflow) handleSessionCompletion(completion sessionCompletion) instructionSequence {
	if !w.matches(completion.target, loopDispatchingParticipant) {
		return nil
	}
	state := w.active
	state.pending = workflowRef{}
	started := completion.err == nil || slices.Contains(
		session.DeliveredAliases(completion.err), state.statement.Participant,
	)
	if !started {
		return w.finishDispatch("[loop] stopped: participant turn could not start")
	}
	if state.dispatchTerminalStatus != "" {
		return w.finishDispatch(state.dispatchTerminalStatus)
	}
	state.turns++
	state.phase = loopWaitingForParticipant
	sequence := loopStatusSequence(fmt.Sprintf("[loop] turn %d/%d sent to @%s",
		state.turns, state.statement.MaxTurns, state.statement.Participant))
	w.appendSubmissionSuccess(&sequence)
	return sequence
}

func (w *loopWorkflow) finishDispatch(message string) instructionSequence {
	sequence := loopStatusSequence(message)
	w.appendSubmissionSuccess(&sequence)
	w.active = nil
	return sequence
}

func (w *loopWorkflow) appendSubmissionSuccess(sequence *instructionSequence) {
	if w.active == nil || !w.active.submissionPending {
		return
	}
	raw := w.active.raw
	w.active.submissionPending = false
	*sequence = append(*sequence, publishEventInstruction{event: SubmissionSucceeded{Raw: raw}})
}

func (w *loopWorkflow) handleShellCompletion(completion shellCompletion) instructionSequence {
	sequence := shellObservationSequence(completion)
	if !w.matches(completion.target, loopEvaluating) {
		return sequence
	}
	state := w.active
	state.pending = workflowRef{}
	switch completion.result.Status {
	case shell.StatusSuccess:
		sequence.append(w.finish("[loop] condition /" + state.statement.Condition + " succeeded"))
	case shell.StatusCancelled:
		sequence.append(w.finish("[loop] condition /" + state.statement.Condition + " cancelled"))
	case shell.StatusFailure:
		if state.turns >= state.statement.MaxTurns {
			sequence.append(w.finish(fmt.Sprintf("[loop] reached /max %d; condition /%s still failing",
				state.statement.MaxTurns, state.statement.Condition)))
		} else {
			prompt := formatLoopPrompt(state.statement, completion.result)
			sequence = append(sequence, w.dispatchInstruction(prompt))
		}
	}
	sequence = append(sequence, requestSnapshotInstruction{})
	return sequence
}

func shellObservationSequence(completion shellCompletion) instructionSequence {
	output := formatLoopConditionResult(completion.result)
	return instructionSequence{
		appendRecordInstruction{record: room.NewAgentRecord(shellRecordAlias, agent.Message{
			Mode: agent.ModeSingle,
			Content: agent.Command{
				Command: completion.request.command, Cwd: completion.cwd,
				Output: output, ExitCode: completion.result.ExitCode,
			},
		})},
		publishEventInstruction{event: ShellCompleted{
			Command: completion.request.command, Cwd: completion.cwd,
			Result: completion.result, Output: output,
		}},
	}
}

func (w *loopWorkflow) matches(target workflowRef, phase loopPhase) bool {
	return w.active != nil && w.active.generation == target.generation &&
		w.active.pending == target && w.active.phase == phase
}

func (w *loopWorkflow) finish(message string) instructionSequence {
	w.active = nil
	return loopStatusSequence(message)
}

func loopStatusSequence(message string) instructionSequence {
	return instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindSystem, Text: message}},
		publishEventInstruction{event: LoopStatus{Message: message}},
	}
}

func formatLoopPrompt(statement promptlang.Loop, result shell.Result) string {
	errorText := ""
	if result.Err != nil {
		errorText = result.Err.Error()
	}
	return strings.Join([]string{
		statement.Prompt, "",
		"The completion condition is failing. Continue working on the task using the evidence below.", "",
		"Condition command: /" + statement.Condition,
		"Status: " + string(result.Status),
		"Exit code: " + formatExitCode(result.ExitCode),
		"Stdout:\n" + formatEvidence(result.Stdout),
		"Stderr:\n" + formatEvidence(result.Stderr),
		"Error:\n" + formatEvidence(errorText),
	}, "\n")
}

func formatLoopConditionResult(result shell.Result) string {
	errorText := ""
	if result.Err != nil {
		errorText = result.Err.Error()
	}
	return strings.Join([]string{
		"status: " + string(result.Status),
		"exit code: " + formatExitCode(result.ExitCode),
		"stdout:\n" + formatEvidence(result.Stdout),
		"stderr:\n" + formatEvidence(result.Stderr),
		"error:\n" + formatEvidence(errorText),
	}, "\n")
}

func formatExitCode(exitCode *int) string {
	if exitCode == nil {
		return "(none)"
	}
	return strconv.Itoa(*exitCode)
}

func formatEvidence(text string) string {
	if text == "" {
		return "(none)"
	}
	return text
}
