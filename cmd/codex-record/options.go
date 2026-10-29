package main

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/agent/codex"
	"github.com/roomscript/coderoom/internal/transcript"
)

func recordingOptions(config transcript.Config) ([]codex.Option, error) {
	allowed := map[string][]string{
		"ask_for_approval":  {string(codex.AskDefault), string(codex.AskUntrusted), string(codex.AskOnFailure), string(codex.AskOnRequest), string(codex.AskNever)},
		"sandbox":           {string(codex.SandboxDefault), string(codex.SandboxReadOnly), string(codex.SandboxWorkspaceWrite), string(codex.SandboxDangerFull)},
		"reasoning_summary": {string(codex.ReasoningSummaryDefault), string(codex.ReasoningSummaryAuto), string(codex.ReasoningSummaryConcise), string(codex.ReasoningSummaryDetailed), string(codex.ReasoningSummaryNone)},
	}
	for key, value := range config.Options {
		values, ok := allowed[key]
		if !ok {
			return nil, fmt.Errorf("unknown Codex option %q", key)
		}
		if !slices.Contains(values, value) {
			return nil, fmt.Errorf("invalid Codex option %s=%q", key, value)
		}
	}
	if !slices.Contains([]codex.ReasoningEffort{codex.ReasoningDefault, codex.ReasoningMinimal, codex.ReasoningLow, codex.ReasoningMedium, codex.ReasoningHigh, codex.ReasoningXHigh}, codex.ReasoningEffort(config.Effort)) {
		return nil, fmt.Errorf("invalid Codex effort %q", config.Effort)
	}
	return []codex.Option{
		codex.WithModel(config.Model),
		codex.WithSystemPrompt(config.SystemPrompt),
		codex.WithReasoningEffort(codex.ReasoningEffort(config.Effort)),
		codex.WithAskForApprovalPolicy(codex.AskForApprovalPolicy(config.Options["ask_for_approval"])),
		codex.WithSandboxMode(codex.SandboxMode(config.Options["sandbox"])),
		codex.WithReasoningSummary(codex.ReasoningSummary(config.Options["reasoning_summary"])),
	}, nil
}
