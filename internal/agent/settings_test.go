package agent_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestSettingsLabels(t *testing.T) {
	tests := []struct {
		name       string
		settings   agent.Settings
		wantModel  string
		wantEffort string
	}{
		{"both known", agent.Settings{Backend: "claude", Model: "claude-opus-5-5", Effort: "high"}, "claude-opus-5-5", "high"},
		{"both managed", agent.Settings{Backend: "codex"}, "codex-managed", "codex-managed"},
		{"model known, effort managed", agent.Settings{Backend: "claude", Model: "claude-haiku-4-5"}, "claude-haiku-4-5", "claude-managed"},
		{"effort known, model managed", agent.Settings{Backend: "codex", Effort: "low"}, "codex-managed", "low"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.settings.ModelLabel(); got != tt.wantModel {
				t.Errorf("ModelLabel() = %q, want %q", got, tt.wantModel)
			}
			if got := tt.settings.EffortLabel(); got != tt.wantEffort {
				t.Errorf("EffortLabel() = %q, want %q", got, tt.wantEffort)
			}
		})
	}
}
