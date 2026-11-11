package main

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/agent/claude"
	"github.com/roomscript/coderoom/internal/transcript"
)

func recordingOptions(config transcript.Config) ([]claude.Option, error) {
	if config.Effort != "" {
		return nil, fmt.Errorf("claude does not support header effort; use a model-select action")
	}
	for key, value := range config.Options {
		if key != "approval" {
			return nil, fmt.Errorf("unknown Claude option %q", key)
		}
		if value != "accept" && value != "decline" {
			return nil, fmt.Errorf("invalid Claude approval %q", value)
		}
	}
	return []claude.Option{claude.WithModel(config.Model), claude.WithSystemPrompt(config.SystemPrompt)}, nil
}

func validateActions(actions []transcript.Action) error {
	for _, action := range actions {
		if action.Kind == "model-interrupt" {
			return fmt.Errorf("claude does not support model-interrupt")
		}
		if action.Kind != "" && action.Kind != "prompt" && action.Kind != "notice" && !transcript.IsModelAction(action.Kind) {
			return fmt.Errorf("unknown action kind %q", action.Kind)
		}
	}
	return nil
}
