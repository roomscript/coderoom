package claude

import "github.com/roomscript/coderoom/internal/agent"

// IsStderrLogStream reports whether id carries process stderr lines.
func IsStderrLogStream(id agent.StreamID) bool {
	return id == stderrStreamID
}

// BindReplayIDs pairs a recorded user prompt UUID with the replay client's UUID.
// Other values remain matched as recorded. It leaves its inputs unchanged.
func BindReplayIDs(expected, actual map[string]any) (map[string]string, error) {
	if expected["type"] != "user" {
		return nil, nil
	}
	recorded, ok := expected["uuid"].(string)
	if !ok {
		return nil, nil
	}
	replayed, ok := actual["uuid"].(string)
	if !ok {
		return nil, nil
	}
	return map[string]string{recorded: replayed}, nil
}
