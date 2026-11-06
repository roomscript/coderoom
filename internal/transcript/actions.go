package transcript

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/agent"
)

// ActionClient supplies the agent and model capabilities used by scenarios.
type ActionClient interface {
	agent.Agent
	ModelClient
}

// RunActions executes a complete recording or replay scenario.
func RunActions(client ActionClient, actions []Action, collector *Collector, activity *TurnActivityObserver) error {
	models := NewModelActions(client, activity)
	for _, action := range actions {
		if IsModelAction(action.Kind) {
			if err := models.Run(action, collector.Observe); err != nil {
				return fmt.Errorf("run %s: %w", action.Kind, err)
			}
			continue
		}
		if err := runAction(client, action, collector); err != nil {
			return err
		}
	}
	return nil
}

func runAction(client ActionClient, action Action, collector *Collector) error {
	anchor, err := sendAction(client, action)
	if err != nil {
		return err
	}
	collector.turnAnchor = anchor
	for {
		msg, err := client.Read()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}
		collector.Observe(msg)
		if msg.StreamID == anchor && msg.Mode == agent.ModeFlush {
			return nil
		}
	}
}

func sendAction(client ActionClient, action Action) (agent.StreamID, error) {
	switch action.Kind {
	case "", "prompt":
		anchor, err := client.Send(action.Text)
		if err != nil {
			return "", fmt.Errorf("send prompt: %w", err)
		}
		return anchor, nil
	case "notice":
		anchor, err := client.SendNotice(action.Text)
		if err != nil {
			return "", fmt.Errorf("send notice: %w", err)
		}
		return anchor, nil
	default:
		return "", fmt.Errorf("unknown action kind %q", action.Kind)
	}
}
