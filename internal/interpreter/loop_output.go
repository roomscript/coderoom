package interpreter

import (
	"strconv"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

func shellObservationSequence(outcome shellOutcome) instructionSequence {
	output := formatLoopConditionResult(outcome.result)
	return instructionSequence{
		appendRecordInstruction{record: room.NewAgentRecord(shellRecordAlias, agent.Message{
			Mode: agent.ModeSingle,
			Content: agent.Command{
				Command: outcome.request.command, Cwd: outcome.cwd,
				Output: output, ExitCode: outcome.result.ExitCode,
			},
		})},
		publishEventInstruction{event: ShellCompleted{
			Command: outcome.request.command, Cwd: outcome.cwd,
			Result: outcome.result, Output: output,
		}},
	}
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
		statement.Prompt.Value, "",
		"The outcome condition is failing. Continue working on the task using the evidence below.", "",
		"Condition command: /" + statement.Condition.Value,
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
