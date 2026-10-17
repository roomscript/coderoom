package std

import (
	"strconv"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/api"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

func (i *loopInvocation) publishCondition(result shell.Result) {
	output := formatLoopConditionResult(result)
	command, cwd := "/"+i.statement.Condition.Value, i.ctx.Shell.Cwd()
	record := room.NewAgentRecord(participant.Alias{Name: "shell"}, agent.Message{
		Mode: agent.ModeSingle, Content: agent.Command{Command: command, Cwd: cwd, Output: output, ExitCode: result.ExitCode},
	})
	i.ctx.Publication.Publish([]room.Record{record}, api.ShellCompleted{Raw: i.raw, Command: command, Cwd: cwd, Result: result, Output: output})
}

func (i *loopInvocation) publishStatus(message string) {
	i.ctx.Publication.Publish([]room.Record{{Kind: room.KindSystem, Text: message}}, api.LoopStatus{Message: message})
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
