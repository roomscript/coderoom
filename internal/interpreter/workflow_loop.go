package interpreter

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/shell"
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
) effectBatch {
	batch := acceptedInputBatch(raw)
	if w.active != nil {
		batch.effects = append(batch.effects, publishEventEffect{event: SubmissionFailed{
			Raw: raw, Operation: "loop", Code: ErrorExecutionFailed, Err: errLoopAlreadyActive,
		}})
		return batch
	}
	body, err := commands.Resolve(promptlang.CommandInvocation{Name: statement.Condition})
	if err != nil {
		batch.effects = append(batch.effects, publishEventEffect{event: SubmissionFailed{
			Raw: raw, Operation: "loop condition /" + statement.Condition,
			Code: ErrorExecutionFailed, Err: err,
		}})
		return batch
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
	batch.effects = append(batch.effects, w.dispatchEffect(statement.Prompt))
	return batch
}

func acceptedInputBatch(raw string) effectBatch {
	return effectBatch{effects: []effect{
		appendRecordEffect{record: room.Record{Kind: room.KindUserInput, Text: raw}},
		publishEventEffect{event: InputAccepted{Raw: raw}},
	}}
}

func (w *loopWorkflow) dispatchEffect(prompt string) executeSessionEffect {
	state := w.active
	state.phase = loopDispatchingParticipant
	state.pending = w.nextRef(state.generation)
	return executeSessionEffect{
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

func (w *loopWorkflow) handleSessionEvent(event session.Event) effectBatch {
	if w.active == nil {
		return effectBatch{}
	}
	if w.active.phase == loopDispatchingParticipant {
		w.retainDispatchTerminalEvent(event)
		return effectBatch{}
	}
	if w.active.phase != loopWaitingForParticipant {
		return effectBatch{}
	}
	return w.handleWaitingEvent(event)
}

func (w *loopWorkflow) handleWaitingEvent(event session.Event) effectBatch {
	alias := w.active.statement.Participant
	switch event := event.(type) {
	case session.ParticipantStatusChanged:
		if event.Alias != alias || event.To != participant.StatusIdle {
			return effectBatch{}
		}
		w.active.phase = loopEvaluating
		w.active.pending = w.nextRef(w.active.generation)
		return effectBatch{effects: []effect{startShellEffect{
			target: w.active.pending,
			request: shellRequest{
				command: "/" + w.active.statement.Condition,
				program: w.active.body.Program,
			},
		}}}
	case session.AgentStopped:
		if event.Alias == alias {
			return w.finish("[loop] stopped: participant @" + alias + " stopped")
		}
	case session.AgentCrashed:
		if event.Alias == alias {
			return w.finish("[loop] stopped: participant @" + alias + " crashed")
		}
	}
	return effectBatch{}
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

func (w *loopWorkflow) handleSessionCompletion(completion sessionCompletion) effectBatch {
	if !w.matches(completion.target, loopDispatchingParticipant) {
		return effectBatch{}
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
	batch := loopStatusBatch(fmt.Sprintf("[loop] turn %d/%d sent to @%s",
		state.turns, state.statement.MaxTurns, state.statement.Participant))
	w.appendSubmissionSuccess(&batch)
	return batch
}

func (w *loopWorkflow) finishDispatch(message string) effectBatch {
	batch := loopStatusBatch(message)
	w.appendSubmissionSuccess(&batch)
	w.active = nil
	return batch
}

func (w *loopWorkflow) appendSubmissionSuccess(batch *effectBatch) {
	if w.active == nil || !w.active.submissionPending {
		return
	}
	raw := w.active.raw
	w.active.submissionPending = false
	batch.effects = append(batch.effects, publishEventEffect{event: SubmissionSucceeded{Raw: raw}})
}

func (w *loopWorkflow) handleShellCompletion(completion shellCompletion) effectBatch {
	batch := shellObservationBatch(completion)
	if !w.matches(completion.target, loopEvaluating) {
		return batch
	}
	state := w.active
	state.pending = workflowRef{}
	switch completion.result.Status {
	case shell.StatusSuccess:
		batch.append(w.finish("[loop] condition /" + state.statement.Condition + " succeeded"))
	case shell.StatusCancelled:
		batch.append(w.finish("[loop] condition /" + state.statement.Condition + " cancelled"))
	case shell.StatusFailure:
		if state.turns >= state.statement.MaxTurns {
			batch.append(w.finish(fmt.Sprintf("[loop] reached /max %d; condition /%s still failing",
				state.statement.MaxTurns, state.statement.Condition)))
		} else {
			prompt := formatLoopPrompt(state.statement, completion.result)
			batch.effects = append(batch.effects, w.dispatchEffect(prompt))
		}
	}
	batch.publishSnapshot = true
	return batch
}

func shellObservationBatch(completion shellCompletion) effectBatch {
	output := formatLoopConditionResult(completion.result)
	return effectBatch{effects: []effect{
		appendRecordEffect{record: room.NewAgentRecord(shellRecordAlias, agent.Message{
			Mode: agent.ModeSingle,
			Content: agent.Command{
				Command: completion.request.command, Cwd: completion.cwd,
				Output: output, ExitCode: completion.result.ExitCode,
			},
		})},
		publishEventEffect{event: ShellCompleted{
			Command: completion.request.command, Cwd: completion.cwd,
			Result: completion.result, Output: output,
		}},
	}}
}

func (w *loopWorkflow) matches(target workflowRef, phase loopPhase) bool {
	return w.active != nil && w.active.generation == target.generation &&
		w.active.pending == target && w.active.phase == phase
}

func (w *loopWorkflow) finish(message string) effectBatch {
	w.active = nil
	return loopStatusBatch(message)
}

func loopStatusBatch(message string) effectBatch {
	return effectBatch{effects: []effect{
		appendRecordEffect{record: room.Record{Kind: room.KindSystem, Text: message}},
		publishEventEffect{event: LoopStatus{Message: message}},
	}}
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
