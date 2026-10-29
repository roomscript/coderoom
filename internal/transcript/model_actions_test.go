package transcript

import (
	"errors"
	"io"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// This fake implements only the capability needed by model actions, without a backend client.
type modelActionClient struct {
	settings    agent.Settings
	messages    []agent.Message
	activity    *TurnActivityObserver
	selections  int
	interrupted bool
}

func (c *modelActionClient) Settings() agent.Settings { return c.settings }
func (c *modelActionClient) Models() error {
	c.messages = append(c.messages, agent.Message{Mode: agent.ModeSingle, Content: agent.ModelsListed{
		Models: []agent.ModelOption{{Name: "test-model", ID: "test-model", Efforts: []string{"low"}}},
	}})
	return nil
}
func (c *modelActionClient) SelectModel(option agent.ModelOption, effort string) error {
	c.selections++
	c.settings.Model = option.ID
	if effort != "" {
		c.settings.Effort = effort
	}
	c.messages = append(c.messages, agent.Message{Mode: agent.ModeSingle, Content: agent.ModelSelected{Settings: c.settings}})
	if c.activity != nil {
		c.activity.Signal()
	}
	return nil
}
func (c *modelActionClient) Interrupt() error {
	c.interrupted = true
	for i, msg := range c.messages {
		if result, ok := msg.Content.(agent.ModelSelected); ok {
			result.Err = errors.New("interrupted")
			c.messages[i].Content = result
		}
	}
	return nil
}
func (c *modelActionClient) Read() (agent.Message, error) {
	if len(c.messages) == 0 {
		return agent.Message{}, io.EOF
	}
	msg := c.messages[0]
	c.messages = c.messages[1:]
	return msg, nil
}

func TestModelActionsUseAgentCapability(t *testing.T) {
	c := &modelActionClient{settings: agent.Settings{Backend: "fake"}}
	runner := NewModelActions(c, NewTurnActivityObserver(nil, nil))
	actions := []Action{
		{Kind: "model-list", Text: "test-model"},
		{Kind: "model-select", Text: "test-model low"},
		{Kind: "model-select", Text: "test-model"},
		{Kind: "model-settings", Text: "test-model low"},
	}
	var messages []agent.Message
	for _, action := range actions {
		if err := runner.Run(action, func(msg agent.Message) { messages = append(messages, msg) }); err != nil {
			t.Fatal(err)
		}
	}
	if c.selections != 2 || len(messages) != 3 {
		t.Fatalf("selections=%d messages=%d", c.selections, len(messages))
	}
}

func TestModelActionsInterrupt(t *testing.T) {
	c := &modelActionClient{
		settings: agent.Settings{Backend: "fake"},
		activity: NewTurnActivityObserver(nil, nil),
	}
	runner := NewModelActions(c, c.activity)
	ignore := func(agent.Message) {}
	if err := runner.Run(Action{Kind: "model-list", Text: "test-model"}, ignore); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(Action{Kind: "model-interrupt", Text: "test-model low"}, ignore); err != nil {
		t.Fatal(err)
	}
	if !c.interrupted || c.selections != 1 {
		t.Fatalf("interrupted=%t selections=%d", c.interrupted, c.selections)
	}
}

func TestModelSelectionContract(t *testing.T) {
	tests := []struct {
		name    string
		failure bool
		result  agent.Settings
		current agent.Settings
		effort  string
		wantErr bool
	}{
		{"success", false, agent.Settings{Model: "new", Effort: "low"}, agent.Settings{Model: "new", Effort: "low"}, "low", false},
		{"model only", false, agent.Settings{Model: "new", Effort: "high"}, agent.Settings{Model: "new", Effort: "high"}, "", false},
		{"wrong model", false, agent.Settings{Model: "old"}, agent.Settings{Model: "old"}, "", true},
		{"wrong effort", false, agent.Settings{Model: "new", Effort: "high"}, agent.Settings{Model: "new", Effort: "high"}, "low", true},
		{"failure retains settings", true, agent.Settings{Model: "old"}, agent.Settings{Model: "old"}, "low", false},
		{"failure unknown settings", true, agent.Settings{}, agent.Settings{}, "low", false},
		{"failure inconsistent outcome", true, agent.Settings{Model: "new"}, agent.Settings{Model: "old"}, "low", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewModelActions(&modelActionClient{settings: tt.current}, nil)
			result := agent.ModelSelected{Settings: tt.result}
			if tt.failure {
				result.Err = errors.New("selection failed")
			}
			err := runner.checkSelection(result, agent.ModelOption{ID: "new"}, tt.effort, tt.failure)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkSelection error=%v, want error=%t", err, tt.wantErr)
			}
		})
	}
}
