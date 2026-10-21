package transcript

import (
	"encoding/json"
	"sync"
)

// Observer captures raw JSON-RPC traffic as replayable transcript steps.
// It satisfies protocol observer interfaces by method set.
type Observer struct {
	mu    sync.Mutex
	steps []Step
}

// NewObserver returns a fresh protocol observer that records transcript steps.
func NewObserver() *Observer {
	return &Observer{}
}

// OnSend records one outbound JSON-RPC line as a replay "recv" step.
func (o *Observer) OnSend(msg string) {
	o.append("recv", msg)
}

// OnReceive records one inbound JSON-RPC line as a replay "send" step.
func (o *Observer) OnReceive(msg string) {
	o.append("send", msg)
}

// Steps returns a stable snapshot of the recorded transcript steps.
func (o *Observer) Steps() []Step {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Step, len(o.steps))
	copy(out, o.steps)
	return out
}

func (o *Observer) append(kind string, raw string) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return
	}
	step := Step{Kind: kind}
	if kind == "recv" {
		step.Match = normalizeReplayMatch(payload)
	} else {
		step.Message = payload
	}
	o.mu.Lock()
	o.steps = append(o.steps, step)
	o.mu.Unlock()
}

func normalizeReplayMatch(payload any) any {
	msg, ok := payload.(map[string]any)
	if !ok {
		return payload
	}

	method, _ := msg["method"].(string)
	switch method {
	case "initialize":
		return normalizeInitializeMatch(msg)
	case "thread/start":
		return normalizeThreadStartMatch(msg)
	case "turn/start":
		return normalizeTurnStartMatch(msg)
	case "turn/interrupt":
		return normalizeTurnInterruptMatch(msg)
	default:
		return payload
	}
}

func normalizeInitializeMatch(msg map[string]any) map[string]any {
	out := copySelectedFields(msg, "method", "id")
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		return out
	}
	out["params"] = map[string]any{
		"clientInfo":   normalizeClientInfo(params["clientInfo"]),
		"capabilities": copyMapValue(params["capabilities"]),
	}
	return out
}

func normalizeClientInfo(raw any) map[string]any {
	clientInfo, _ := raw.(map[string]any)
	if clientInfo == nil {
		return nil
	}
	return copySelectedMapFields(clientInfo, "name")
}

func normalizeThreadStartMatch(msg map[string]any) map[string]any {
	out := copySelectedFields(msg, "method", "id")
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		return out
	}
	normParams := copySelectedMapFields(params, "model", "developerInstructions")
	if len(normParams) > 0 {
		out["params"] = normParams
	}
	return out
}

func normalizeTurnStartMatch(msg map[string]any) map[string]any {
	out := copySelectedFields(msg, "method", "id")
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		return out
	}
	out["params"] = copySelectedMapFields(params, "threadId", "input", "outputSchema", "model", "effort")
	return out
}

func normalizeTurnInterruptMatch(msg map[string]any) map[string]any {
	out := copySelectedFields(msg, "method", "id")
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		return out
	}
	out["params"] = copySelectedMapFields(params, "threadId", "turnId")
	return out
}

func copySelectedFields(src map[string]any, keys ...string) map[string]any {
	return copySelectedMapFields(src, keys...)
}

func copySelectedMapFields(src map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := src[key]; ok {
			out[key] = value
		}
	}
	return out
}

func copyMapValue(raw any) map[string]any {
	value, _ := raw.(map[string]any)
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
