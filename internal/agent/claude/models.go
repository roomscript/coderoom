package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/agent"
)

// settingsRequestID is the get_settings request Start sends. Later reads use
// numbered IDs.
const settingsRequestID = "coderoom-settings"

// Settings returns the model and effort last read from Claude. See
// docs/design/pkg-agent-claude-models.md.
func (c *Client) Settings() agent.Settings {
	c.models.mu.Lock()
	defer c.models.mu.Unlock()
	return c.models.settings
}

// Models lists the catalog read at startup. The listing is queued at once,
// with no request to Claude.
func (c *Client) Models() error {
	if c.queue == nil {
		return errors.New("claude: client not started")
	}
	c.models.mu.Lock()
	models := make([]agent.ModelOption, len(c.models.catalog))
	for i, opt := range c.models.catalog {
		opt.Efforts = slices.Clone(opt.Efforts)
		models[i] = opt
	}
	c.models.mu.Unlock()
	listed := agent.ModelsListed{Models: models}
	c.queue.push(agent.Message{StreamID: modelsStreamID, Mode: agent.ModeSingle, Content: listed})
	return nil
}

// SelectModel starts the exchange that changes the model, and the effort
// when given: set_model, apply_flag_settings, then a get_settings readback
// that always runs. The exchange holds the prompt gate until its last
// answer arrives. opt.ID is sent, not opt.Name: aliases such as default
// and opus can share an ID, and Claude picks the first catalog entry that
// matches.
func (c *Client) SelectModel(opt agent.ModelOption, effort string) error {
	if !opt.SupportsEffort(effort) {
		return fmt.Errorf("claude: model %s does not support effort %q", opt.Name, effort)
	}
	if err := c.admitModelChange(); err != nil {
		return err
	}
	request := map[string]any{"subtype": controlSetModel, "model": opt.ID}
	err := c.sendControl(c.nextRequestID("set-model"), request, func(_ json.RawMessage, err error) {
		if err != nil {
			c.readBackSettings(fmt.Errorf("claude set_model: %w", err))
			return
		}
		c.applyEffort(effort)
	})
	if err != nil {
		c.endModelChange()
		return err
	}
	return nil
}

func (c *Client) applyEffort(effort string) {
	if effort == "" {
		c.readBackSettings(nil)
		return
	}
	request := map[string]any{
		"subtype":  controlApplyFlagSettings,
		"settings": map[string]any{"effortLevel": effort},
	}
	err := c.sendControl(c.nextRequestID("effort"), request, func(_ json.RawMessage, err error) {
		if err != nil {
			err = fmt.Errorf("claude apply_flag_settings: %w", err)
		}
		c.readBackSettings(err)
	})
	if err != nil {
		c.readBackSettings(err)
	}
}

// readBackSettings ends the exchange with the settings Claude reports.
// changeErr is the failure of an earlier step, if any.
func (c *Client) readBackSettings(changeErr error) {
	request := map[string]any{"subtype": controlGetSettings}
	err := c.sendControl(c.nextRequestID("settings"), request, func(body json.RawMessage, err error) {
		settings, readErr := c.parseSettings(body, err)
		c.finishModelChange(settings, errors.Join(changeErr, readErr))
	})
	if err != nil {
		c.finishModelChange(agent.Settings{Backend: c.Name()}, errors.Join(changeErr, err))
	}
}

// finishModelChange records the settings and reopens the gate before
// queueing ModelSelected, so a Send issued in reaction to it is accepted.
func (c *Client) finishModelChange(settings agent.Settings, err error) {
	c.models.mu.Lock()
	c.models.settings = settings
	c.models.mu.Unlock()
	c.endModelChange()
	c.queue.push(agent.Message{StreamID: modelsStreamID, Mode: agent.ModeSingle,
		Content: agent.ModelSelected{Settings: settings, Err: err}})
}

// admitModelChange closes the prompt gate for a model change. Like a
// prompt, a change starts only when no turn runs and no prompt waits.
func (c *Client) admitModelChange() error {
	if c.queue == nil {
		return errors.New("claude: client not started")
	}
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.owner != ownerNone || c.turn.pending != nil || c.turn.changingModel {
		return agent.ErrTurnInProgress
	}
	c.turn.changingModel = true
	return nil
}

func (c *Client) endModelChange() {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	c.turn.changingModel = false
}

// readStartupModels records the catalog from the initialize answer and
// reads the current settings. Neither failure fails Start: the settings
// stay unknown, and a catalog that cannot be decoded lists no models.
func (c *Client) readStartupModels(initBody json.RawMessage) {
	catalog, err := parseCatalog(initBody)
	if err != nil {
		c.queue.push(logMessage("claude: model catalog: " + err.Error()))
	}
	body, err := c.awaitControl(settingsRequestID, map[string]any{"subtype": controlGetSettings})
	settings, err := c.parseSettings(body, err)
	if err != nil {
		c.queue.push(logMessage("claude: read settings: " + err.Error()))
	}
	c.models.mu.Lock()
	defer c.models.mu.Unlock()
	c.models.catalog = catalog
	c.models.settings = settings
}

func parseCatalog(body json.RawMessage) ([]agent.ModelOption, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var resp initializeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode initialize: %w", err)
	}
	catalog := make([]agent.ModelOption, 0, len(resp.Models))
	for _, m := range resp.Models {
		opt := agent.ModelOption{Name: m.Value, ID: m.ResolvedModel}
		if opt.ID == "" {
			opt.ID = m.Value
		}
		if m.SupportsEffort {
			opt.Efforts = m.SupportedEffortLevels
		}
		catalog = append(catalog, opt)
	}
	return catalog, nil
}

// parseSettings returns the applied settings from a get_settings answer.
// On any failure both fields are unknown.
func (c *Client) parseSettings(body json.RawMessage, err error) (agent.Settings, error) {
	settings := agent.Settings{Backend: c.Name()}
	if err != nil {
		return settings, fmt.Errorf("claude get_settings: %w", err)
	}
	var resp settingsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return settings, fmt.Errorf("claude get_settings: %w", err)
	}
	settings.Model = resp.Applied.Model
	settings.Effort = resp.Applied.Effort
	return settings, nil
}
