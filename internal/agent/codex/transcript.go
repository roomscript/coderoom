package codex

import (
	"encoding/json"
	"fmt"
	"maps"
)

// CreateReplayMatch turns a decoded outbound Codex JSON object into the expected
// replay subset, omitting unstable fields. Unrecognized messages match in full.
// It leaves its input unchanged, performs no I/O, and reports malformed fields.
// Nested raw values may be shared with the input and must be treated as read-only.
func CreateReplayMatch(message map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if _, ok := message["method"]; !ok {
		return maps.Clone(message), nil
	}
	var method string
	if err := json.Unmarshal(message["method"], &method); err != nil {
		return nil, fmt.Errorf("decode protocol method: %w", err)
	}
	keys := replayParamKeys(method)
	if keys == nil {
		return maps.Clone(message), nil
	}
	out := selectReplayFields(message, "method", "id")
	raw, err := createReplayParams(message, method, keys)
	if err != nil {
		return nil, err
	}
	if raw != nil {
		out["params"] = raw
	}
	return out, nil
}

func replayParamKeys(method string) []string {
	var keys []string
	switch method {
	case "initialize":
		keys = []string{"clientInfo", "capabilities"}
	case "thread/start":
		keys = []string{"model", "developerInstructions"}
	case "turn/start":
		keys = []string{"threadId", "input", "outputSchema", "model", "effort"}
	case "turn/interrupt":
		keys = []string{"threadId", "turnId"}
	default:
		return nil
	}
	return keys
}

func createReplayParams(message map[string]json.RawMessage, method string, keys []string) (json.RawMessage, error) {
	if _, ok := message["params"]; !ok {
		return nil, nil
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(message["params"], &params); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", method, err)
	}
	if params == nil {
		return nil, nil
	}
	selected := selectReplayFields(params, keys...)
	if method == "initialize" {
		if err := updateReplayClientInfo(params, selected); err != nil {
			return nil, err
		}
	}
	if method == "thread/start" && len(selected) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		return nil, fmt.Errorf("encode replay params: %w", err)
	}
	return raw, nil
}

func updateReplayClientInfo(params, selected map[string]json.RawMessage) error {
	var info map[string]json.RawMessage
	if raw, ok := params["clientInfo"]; ok {
		if err := json.Unmarshal(raw, &info); err != nil {
			return fmt.Errorf("decode client info: %w", err)
		}
	}
	selected["clientInfo"] = json.RawMessage("null")
	if info != nil {
		raw, err := json.Marshal(selectReplayFields(info, "name"))
		if err != nil {
			return fmt.Errorf("encode client info: %w", err)
		}
		selected["clientInfo"] = raw
	}
	if _, ok := selected["capabilities"]; !ok {
		selected["capabilities"] = json.RawMessage("null")
	}
	return nil
}

func selectReplayFields(src map[string]json.RawMessage, keys ...string) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(keys))
	for _, key := range keys {
		if value, ok := src[key]; ok {
			out[key] = value
		}
	}
	return out
}

// IsTurnActivity reports a user item that establishes an active Codex turn.
func IsTurnActivity(raw string) bool {
	var event struct {
		Method string
		Params struct{ Item struct{ Type string } }
	}
	if json.Unmarshal([]byte(raw), &event) != nil || event.Method != "item/started" || event.Params.Item.Type != "userMessage" {
		return false
	}
	return true
}
