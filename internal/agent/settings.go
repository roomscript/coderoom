package agent

// Settings are the model and effort a backend reports for one session,
// together with the backend's name. An empty Model or Effort has no concrete
// value: unknown, not reported, uncertain after a failed change, or not
// applicable. See docs/design/participant-models.md.
type Settings struct {
	Backend string // the agent's Name(), such as "claude"
	Model   string
	Effort  string
}

// ModelLabel returns Model, or Backend + "-managed" when Model is empty.
func (s Settings) ModelLabel() string { return s.label(s.Model) }

// EffortLabel returns Effort, or Backend + "-managed" when Effort is empty.
func (s Settings) EffortLabel() string { return s.label(s.Effort) }

func (s Settings) label(value string) string {
	if value != "" {
		return value
	}
	return s.Backend + "-managed"
}
