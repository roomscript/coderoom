package codex

import "encoding/json"

// rpcRequest is a minimal JSON-RPC request envelope (newline-delimited JSON).
// Codex does not require the jsonrpc version field in practice.
type rpcRequest[T any] struct {
	Method string `json:"method"`
	ID     int    `json:"id"`
	Params T      `json:"params,omitempty"`
}

type initializeParams struct {
	ClientInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
	Capabilities struct {
		ExperimentalAPI bool `json:"experimentalApi"`
	} `json:"capabilities"`
}

type threadStartParams struct {
	Cwd                   string  `json:"cwd"`
	Model                 *string `json:"model,omitempty"`
	DeveloperInstructions *string `json:"developerInstructions,omitempty"`
}

type threadStartResult struct {
	Model  string `json:"model"`
	Effort string `json:"reasoningEffort"`
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnStartParams struct {
	Model        string          `json:"model,omitempty"`
	Effort       string          `json:"effort,omitempty"`
	ThreadID     string          `json:"threadId"`
	Input        []turnInput     `json:"input"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

type turnInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnStartedParams struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type notificationParams struct {
	Delta  string `json:"delta"`
	ItemID string `json:"itemId"`
	TurnID string `json:"turnId"`
}

type turnCompletedParams struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string          `json:"id"`
		Status string          `json:"status"`
		Error  *codexTurnError `json:"error"`
		Items  []itemKind      `json:"items"`
	} `json:"turn"`
}

type errorNotificationParams struct {
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	WillRetry bool            `json:"willRetry"`
	Error     *codexTurnError `json:"error"`
}

type codexTurnError struct {
	Message           string  `json:"message"`
	CodexErrorInfo    string  `json:"codexErrorInfo"`
	AdditionalDetails *string `json:"additionalDetails"`
}

// rpcEnvelope is the minimal wire envelope used for decoding unknown messages.
// Params/Result are left as RawMessage so they can be routed by Method first.
type rpcEnvelope struct {
	Method string          `json:"method,omitempty"`
	ID     *int            `json:"id,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type rpcResponse[T any] struct {
	ID     int `json:"id"`
	Result T   `json:"result"`
}

type commandExecutionRequestApprovalParams struct {
	Command string  `json:"command"`
	Cwd     *string `json:"cwd"`
}

type fileChangeRequestApprovalParams struct {
	GrantRoot *string `json:"grantRoot"`
	Reason    *string `json:"reason"`
}

type permissionsRequestApprovalParams struct {
	Cwd         string          `json:"cwd"`
	Permissions json.RawMessage `json:"permissions"`
	Reason      *string         `json:"reason"`
}

type requestPermissionProfile struct {
	FileSystem json.RawMessage `json:"fileSystem"`
	Network    json.RawMessage `json:"network"`
}

// itemLifecycleParams is the shared shape for item/started and item/completed.
type itemLifecycleParams struct {
	TurnID string          `json:"turnId"`
	Item   json.RawMessage `json:"item"`
}

type itemKind struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// commandExecutionItem is the subset of CommandExecutionThreadItem fields used
// by coderoom. Adapter-irrelevant fields (processId, source, commandActions)
// are omitted.
type commandExecutionItem struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	Command          string `json:"command"`
	Cwd              string `json:"cwd"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`
}

type fileChangeItem struct {
	Type    string             `json:"type"`
	ID      string             `json:"id"`
	Status  string             `json:"status"`
	Changes []fileUpdateChange `json:"changes"`
}

type fileChangePatchUpdatedParams struct {
	ItemID  string             `json:"itemId"`
	TurnID  string             `json:"turnId"`
	Changes []fileUpdateChange `json:"changes"`
}

type fileUpdateChange struct {
	Path string               `json:"path"`
	Diff string               `json:"diff"`
	Kind fileUpdateChangeKind `json:"kind"`
}

type fileUpdateChangeKind struct {
	Type     string  `json:"type"`
	MovePath *string `json:"move_path"`
}

type commandDecisionResult struct {
	Decision string `json:"decision"`
}

type permissionsGrantResult struct {
	Permissions json.RawMessage `json:"permissions"`
}
