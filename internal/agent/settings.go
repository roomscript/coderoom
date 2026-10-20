package agent

import "slices"

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

// ModelOption is one catalog entry.
type ModelOption struct {
	Name    string   // what the user types, such as "opus"
	ID      string   // full model ID; equal to Name when Name is not an alias
	Efforts []string // supported efforts, backend-specific names; empty: none
}

// SupportsEffort reports whether effort is one of the entry's efforts. The
// empty effort, meaning model only, is always supported.
func (o ModelOption) SupportsEffort(effort string) bool {
	return effort == "" || slices.Contains(o.Efforts, effort)
}

// ModelSelector is an optional capability for agents whose model and effort
// can be inspected and changed within the running session. Models and
// SelectModel start work and return; their outcomes arrive on Read as
// ModelsListed and ModelSelected. Neither puts turn messages on Read.
type ModelSelector interface {
	// Settings returns the current settings, as last established. Valid
	// after Start; before that, only Backend is set. No I/O.
	Settings() Settings

	// Models starts a catalog read. An error means nothing started.
	// Otherwise one ModelsListed arrives on Read, unless the process exits
	// first. Accepted in any status after Start. Listings arrive in the order
	// of the calls that started them.
	Models() error

	// SelectModel starts a change. An error means nothing started: effort
	// not supported by opt, a turn or another change in progress, or a write
	// failure. Otherwise one ModelSelected arrives on Read, unless the
	// process exits first. While a change runs, Send, SendNotice and
	// KeepAlive return ErrTurnInProgress. An empty effort selects the model
	// only.
	SelectModel(opt ModelOption, effort string) error
}

// ModelsListed reports the outcome of ModelSelector.Models (ModeSingle).
// Err is non-nil when the backend could not list its models.
type ModelsListed struct {
	Models []ModelOption
	Err    error
}

// ModelSelected reports the outcome of ModelSelector.SelectModel
// (ModeSingle). Settings is always set; fields the backend may not have
// applied are empty. Err is non-nil when the change did not fully apply.
type ModelSelected struct {
	Settings Settings
	Err      error
}

func (ModelsListed) content()  {}
func (ModelSelected) content() {}
