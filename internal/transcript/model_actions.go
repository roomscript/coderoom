package transcript

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

// IsModelAction identifies model operations in conversation action files.
func IsModelAction(kind string) bool {
	switch kind {
	case "model-list", "model-select", "model-select-failed", "model-interrupt", "model-settings":
		return true
	default:
		return false
	}
}

// ModelClient is the agent capability used by transcript model actions.
type ModelClient interface {
	agent.ModelSelector
	Read() (agent.Message, error)
	Interrupt() error
}

// ModelActions drives model operations for recording and replay.
// Catalog and activity belong to this scenario, not to participant state.
type ModelActions struct {
	client   ModelClient
	activity *TurnActivityObserver
	catalog  []agent.ModelOption
}

// NewModelActions shares one runner across the actions of a conversation.
func NewModelActions(client ModelClient, activity *TurnActivityObserver) *ModelActions {
	return &ModelActions{client: client, activity: activity}
}

// Run executes one model action and checks its outcome before returning.
func (r *ModelActions) Run(action Action, observe func(agent.Message)) error {
	switch action.Kind {
	case "model-list":
		return r.list(action.Text, observe)
	case "model-settings":
		return r.checkSettings(action.Text)
	case "model-select", "model-select-failed", "model-interrupt":
		return r.selectModel(action, observe)
	default:
		return fmt.Errorf("unknown model action %q", action.Kind)
	}
}

func (r *ModelActions) list(required string, observe func(agent.Message)) error {
	if err := r.client.Models(); err != nil {
		return fmt.Errorf("start catalog read: %w", err)
	}
	msg, err := r.readOutcome(true, observe)
	if err != nil {
		return err
	}
	listing := msg.Content.(agent.ModelsListed)
	if listing.Err != nil {
		return listing.Err
	}
	if len(listing.Models) == 0 {
		return errors.New("model catalog is empty")
	}
	r.catalog = listing.Models
	for _, model := range strings.Fields(required) {
		if _, ok := r.find(model); !ok {
			return fmt.Errorf("catalog has no model %q", model)
		}
	}
	return nil
}

func (r *ModelActions) selectModel(action Action, observe func(agent.Message)) error {
	option, effort, err := r.selectionOption(action)
	if err != nil {
		return err
	}
	r.activity.Reset()
	if err := r.client.SelectModel(option, effort); err != nil {
		return fmt.Errorf("start model selection: %w", err)
	}
	if action.Kind == "model-interrupt" {
		if err := r.interrupt(); err != nil {
			return err
		}
	}
	msg, err := r.readOutcome(false, observe)
	if err != nil {
		return err
	}
	return r.checkSelection(msg.Content.(agent.ModelSelected), option, effort, action.Kind != "model-select")
}

func (r *ModelActions) checkSelection(result agent.ModelSelected, option agent.ModelOption, effort string, wantErr bool) error {
	if (result.Err != nil) != wantErr {
		return fmt.Errorf("model selection error=%v, want failure=%t", result.Err, wantErr)
	}
	if !wantErr && (result.Settings.Model != option.ID || (effort != "" && result.Settings.Effort != effort)) {
		return fmt.Errorf("model selection settings=%+v, requested %s/%s", result.Settings, option.ID, effort)
	}
	if result.Settings != r.client.Settings() {
		return errors.New("model outcome and current settings differ")
	}
	return nil
}

func (r *ModelActions) interrupt() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.activity.Wait(ctx); err != nil {
		return fmt.Errorf("wait for model activation: %w", err)
	}
	if err := r.client.Interrupt(); err != nil {
		return fmt.Errorf("interrupt model selection: %w", err)
	}
	return nil
}

func (r *ModelActions) checkSettings(text string) error {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return errors.New("model-settings needs model and effort")
	}
	settings := r.client.Settings()
	if settings.ModelLabel() != fields[0] || settings.EffortLabel() != fields[1] {
		return fmt.Errorf("settings=%+v, want %s", settings, text)
	}
	return nil
}

func (r *ModelActions) find(model string) (agent.ModelOption, bool) {
	for _, option := range r.catalog {
		if option.ID == model || option.Name == model {
			return option, true
		}
	}
	return agent.ModelOption{}, false
}

func (r *ModelActions) readOutcome(listing bool, observe func(agent.Message)) (agent.Message, error) {
	for {
		msg, err := r.client.Read()
		if err != nil {
			return agent.Message{}, fmt.Errorf("read model outcome: %w", err)
		}
		observe(msg)
		switch msg.Content.(type) {
		case agent.Log:
			continue
		case agent.ModelsListed:
			if listing && msg.Mode == agent.ModeSingle {
				return msg, nil
			}
		case agent.ModelSelected:
			if !listing && msg.Mode == agent.ModeSingle {
				return msg, nil
			}
		}
		return agent.Message{}, fmt.Errorf("unexpected model action message: %+v", msg)
	}
}

func (r *ModelActions) selectionOption(action Action) (agent.ModelOption, string, error) {
	fields := strings.Fields(action.Text)
	if len(fields) < 1 || len(fields) > 2 {
		return agent.ModelOption{}, "", errors.New("model action needs a model and optional effort")
	}
	effort := ""
	if len(fields) == 2 {
		effort = fields[1]
	}
	option, ok := r.find(fields[0])
	if action.Kind == "model-select-failed" {
		// Deliberately bypass catalog admission to cover a backend failure.
		option = agent.ModelOption{Name: fields[0], ID: fields[0], Efforts: []string{effort}}
	} else if !ok {
		return agent.ModelOption{}, "", fmt.Errorf("model %q was not listed", fields[0])
	}

	return option, effort, nil
}
