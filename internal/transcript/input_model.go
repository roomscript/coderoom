package transcript

import "github.com/roomscript/coderoom/internal/agent/codex"

// Config stores scenario-level Codex settings used during transcript recording.
type Config struct {
	Model                 string                     `yaml:"model,omitempty"`
	DeveloperInstructions string                     `yaml:"developer_instructions,omitempty"`
	AskForApproval        codex.AskForApprovalPolicy `yaml:"ask_for_approval,omitempty"`
	Sandbox               codex.SandboxMode          `yaml:"sandbox,omitempty"`
	ReasoningEffort       codex.ReasoningEffort      `yaml:"reasoning_effort,omitempty"`
	ReasoningSummary      codex.ReasoningSummary     `yaml:"reasoning_summary,omitempty"`
}

// Input is the authoring-side transcript scenario definition.
type Input struct {
	Config  Config
	Actions []Action
}

// Action is one normalized scenario action used to drive recording and replay.
type Action struct {
	Kind string `yaml:"kind"`
	Text string `yaml:"text"`
}
