package transcript

// Config stores backend-neutral scenario settings used during recording.
type Config struct {
	Model        string            `yaml:"model,omitempty"`
	SystemPrompt string            `yaml:"system_prompt,omitempty"`
	Effort       string            `yaml:"effort,omitempty"`
	Options      map[string]string `yaml:"options,omitempty"`
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
