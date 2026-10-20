package session

import (
	"fmt"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

// modelListingPurpose says what a requested model listing is for. Listings
// arrive in the order of the Models calls that started them, so each serves
// the purpose at the head of the participant's queue.
type modelListingPurpose int

const (
	modelListingForDisplay modelListingPurpose = iota // /model <alias>
	modelListingForChange                             // the pending ChangeModelCommand
)

// modelChange is a ChangeModelCommand in progress: it waits for its listing,
// then, once selecting is set, for ModelSelected.
type modelChange struct {
	request   ChangeModelCommand
	selecting bool // the listing arrived and SelectModel was called
}

// ListModelsCommand matches /model alias: it reads the participant's model
// catalog. The outcome arrives as ModelsListed. It does not reserve the
// participant, so it works while the participant is busy.
type ListModelsCommand struct {
	Alias participant.Alias
}

func (c ListModelsCommand) execute(s *Session, _ ExecutionID) error {
	selector, err := s.queueReadListing(c.Alias)
	if err != nil {
		return err
	}
	if err := selector.Models(); err != nil {
		s.dropLastListing(c.Alias)
		return fmt.Errorf("list models for %q: %w", c.Alias.Name, err)
	}
	return nil
}

// ChangeModelCommand matches /model alias model [effort]: it changes the
// participant's model, and effort when given, in its running session. It
// reserves the participant, validates against a fresh listing, then selects.
// The outcome arrives as ModelChangeFinished.
type ChangeModelCommand struct {
	Alias  participant.Alias
	Model  string // Name or ID
	Effort string // empty: model only
}

func (c ChangeModelCommand) execute(s *Session, _ ExecutionID) error {
	selector, err := s.beginModelChange(c)
	if err != nil {
		return err
	}
	if err := selector.Models(); err != nil {
		s.dropLastListing(c.Alias)
		s.abandonModelChange(c.Alias)
		return fmt.Errorf("list models for %q: %w", c.Alias.Name, err)
	}
	return nil
}

func (s *Session) queueReadListing(alias participant.Alias) (agent.ModelSelector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, runtime, ok := s.lookupModelRuntimeLocked(alias)
	if !ok {
		return nil, fmt.Errorf("participant %q not found", alias.Name)
	}
	if err := checkModelReadable(p); err != nil {
		return nil, err
	}
	selector, err := readModelSelector(p)
	if err != nil {
		return nil, err
	}
	runtime.pendingModelListings = append(runtime.pendingModelListings, modelListingForDisplay)
	s.agents[alias] = runtime
	return selector, nil
}

// beginModelChange admits a change under the same lock and status check that
// admit work and keepalive, and queues a listing for the change.
func (s *Session) beginModelChange(c ChangeModelCommand) (agent.ModelSelector, error) {
	var selector agent.ModelSelector
	var event Event
	var err error
	s.changeParticipants(func() {
		p, runtime, ok := s.lookupModelRuntimeLocked(c.Alias)
		if !ok {
			err = fmt.Errorf("participant %q not found", c.Alias.Name)
			return
		}
		if err = checkModelReadable(p); err != nil {
			return
		}
		if selector, err = readModelSelector(p); err != nil {
			return
		}
		if p.Status != participant.StatusIdle {
			err = fmt.Errorf("participant %q is busy", c.Alias.Name)
			return
		}
		from := p.Status
		if err = p.BeginModelChange(s.now()); err != nil {
			err = fmt.Errorf("begin model change: %w", err)
			return
		}
		runtime.change = &modelChange{request: c}
		runtime.pendingModelListings = append(runtime.pendingModelListings, modelListingForChange)
		s.agents[c.Alias] = runtime
		event = statusChangedEvent(p, from)
	})
	if err != nil {
		return nil, err
	}
	s.notify(event)
	return selector, nil
}

// lookupModelRuntimeLocked returns the participant and its runtime record.
// The caller must hold s.mu.
func (s *Session) lookupModelRuntimeLocked(alias participant.Alias) (*participant.Participant, agentRuntime, bool) {
	p, ok := s.lookupParticipantLocked(alias)
	if !ok {
		return nil, agentRuntime{}, false
	}
	runtime, ok := s.agents[alias]
	return p, runtime, ok
}

func readModelSelector(p *participant.Participant) (agent.ModelSelector, error) {
	selector, ok := p.Agent.(agent.ModelSelector)
	if !ok {
		return nil, fmt.Errorf("model selection not supported by %s", p.Settings.Backend)
	}
	return selector, nil
}

func checkModelReadable(p *participant.Participant) error {
	switch {
	case p.Status == participant.StatusCrashed:
		return fmt.Errorf("participant %q crashed", p.Alias.Name)
	case !p.StartupReady:
		return fmt.Errorf("participant %q is not ready", p.Alias.Name)
	}
	return nil
}

// dropLastListing removes the purpose queued for a Models call that did not
// start. Commands run serially, so it is the last entry.
func (s *Session) dropLastListing(alias participant.Alias) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, ok := s.agents[alias]
	if !ok || len(runtime.pendingModelListings) == 0 {
		return
	}
	runtime.pendingModelListings = runtime.pendingModelListings[:len(runtime.pendingModelListings)-1]
	s.agents[alias] = runtime
}

// abandonModelChange ends a change that never started at the backend. The
// command reports the error, so no ModelChangeFinished is published.
func (s *Session) abandonModelChange(alias participant.Alias) {
	var event Event
	s.changeParticipants(func() {
		p, runtime, ok := s.lookupModelRuntimeLocked(alias)
		if !ok {
			return
		}
		event, _ = s.finishModelChangeLocked(p, runtime, nil, nil)
	})
	if event != nil {
		s.notify(event)
	}
}

// handleModelsListed serves the purpose at the head of the listing queue.
func (s *Session) handleModelsListed(alias participant.Alias, listed agent.ModelsListed) {
	var events []Event
	var selector agent.ModelSelector
	var opt agent.ModelOption
	var effort string
	s.changeParticipants(func() {
		p, runtime, ok := s.lookupModelRuntimeLocked(alias)
		if !ok || len(runtime.pendingModelListings) == 0 {
			return
		}
		purpose := runtime.pendingModelListings[0]
		runtime.pendingModelListings = runtime.pendingModelListings[1:]
		s.agents[alias] = runtime
		if purpose == modelListingForDisplay {
			events = append(events, newModelsListed(p, listed))
			return
		}
		if runtime.change == nil {
			return
		}
		var err error
		if opt, err = validateModelChange(runtime.change.request, listed); err != nil {
			events = s.appendFinishedLocked(events, p, runtime, nil, err)
			return
		}
		runtime.change.selecting = true
		effort = runtime.change.request.Effort
		selector, _ = p.Agent.(agent.ModelSelector)
	})
	s.notifyAll(events)
	if selector == nil {
		return
	}
	if err := selector.SelectModel(opt, effort); err != nil {
		s.finishModelChange(alias, nil, fmt.Errorf("select model: %w", err))
	}
}

// handleModelSelected applies the backend outcome and ends the change.
func (s *Session) handleModelSelected(alias participant.Alias, selected agent.ModelSelected) {
	s.finishModelChange(alias, &selected.Settings, selected.Err)
}

// finishModelChange ends the pending change and publishes its outcome.
// Settings are replaced only when settings is non-nil.
func (s *Session) finishModelChange(alias participant.Alias, settings *agent.Settings, err error) {
	var events []Event
	s.changeParticipants(func() {
		p, runtime, ok := s.lookupModelRuntimeLocked(alias)
		if !ok || runtime.change == nil {
			return
		}
		if settings != nil && !runtime.change.selecting {
			return
		}
		events = s.appendFinishedLocked(events, p, runtime, settings, err)
	})
	s.notifyAll(events)
}

func (s *Session) appendFinishedLocked(events []Event, p *participant.Participant, runtime agentRuntime, settings *agent.Settings, err error) []Event {
	status, finished := s.finishModelChangeLocked(p, runtime, settings, err)
	if status == nil {
		return events
	}
	return append(events, status, finished)
}

// finishModelChangeLocked clears the pending change, applies settings when
// given, and returns the participant to idle. The caller notifies the
// returned events after unlocking. The caller must hold s.mu.
func (s *Session) finishModelChangeLocked(p *participant.Participant, runtime agentRuntime, settings *agent.Settings, err error) (Event, Event) {
	runtime.change = nil
	s.agents[p.Alias] = runtime
	if settings != nil {
		p.Settings.Model = settings.Model
		p.Settings.Effort = settings.Effort
	}
	from := p.Status
	if finishErr := p.FinishModelChange(s.now()); finishErr != nil {
		return nil, nil
	}
	return statusChangedEvent(p, from), ModelChangeFinished{Alias: p.Alias, Settings: p.Settings, Err: err}
}

func newModelsListed(p *participant.Participant, listed agent.ModelsListed) ModelsListed {
	return ModelsListed{
		Alias:    p.Alias,
		Settings: p.Settings,
		Status:   p.Status,
		Models:   cloneModelOptions(listed.Models),
		Err:      listed.Err,
	}
}

// validateModelChange finds the requested model in a fresh listing and checks
// its effort. A Name match wins over an ID match, since aliases can share an
// ID.
func validateModelChange(request ChangeModelCommand, listed agent.ModelsListed) (agent.ModelOption, error) {
	if listed.Err != nil {
		return agent.ModelOption{}, fmt.Errorf("list models: %w", listed.Err)
	}
	opt, ok := findModelOption(listed.Models, request.Model)
	if !ok {
		return agent.ModelOption{}, fmt.Errorf("unknown model %q (valid: %s)", request.Model, modelNames(listed.Models))
	}
	if !opt.SupportsEffort(request.Effort) {
		return agent.ModelOption{}, unsupportedEffortError(opt, request.Effort)
	}
	return opt, nil
}

func findModelOption(models []agent.ModelOption, name string) (agent.ModelOption, bool) {
	for _, opt := range models {
		if opt.Name == name {
			return opt, true
		}
	}
	for _, opt := range models {
		if opt.ID == name {
			return opt, true
		}
	}
	return agent.ModelOption{}, false
}

func modelNames(models []agent.ModelOption) string {
	if len(models) == 0 {
		return "none"
	}
	names := make([]string, len(models))
	for i, opt := range models {
		names[i] = opt.Name
	}
	return strings.Join(names, ", ")
}

func unsupportedEffortError(opt agent.ModelOption, effort string) error {
	if len(opt.Efforts) == 0 {
		return fmt.Errorf("model %s supports no effort, got %q", opt.Name, effort)
	}
	return fmt.Errorf("model %s does not support effort %q (valid: %s)", opt.Name, effort, strings.Join(opt.Efforts, ", "))
}

func cloneModelOptions(models []agent.ModelOption) []agent.ModelOption {
	out := make([]agent.ModelOption, len(models))
	for i, opt := range models {
		opt.Efforts = append([]string(nil), opt.Efforts...)
		out[i] = opt
	}
	return out
}

func statusChangedEvent(p *participant.Participant, from participant.Status) Event {
	return ParticipantStatusChanged{Alias: p.Alias, From: from, To: p.Status, Since: p.Since, ActiveExecutionID: p.ActiveExecutionID}
}

func (s *Session) notifyAll(events []Event) {
	for _, e := range events {
		s.notify(e)
	}
}
