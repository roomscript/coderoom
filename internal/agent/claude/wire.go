package claude

import "encoding/json"

// Wire types for the stream-json protocol. Only the fields the adapter uses
// are declared. Lines are decoded in two steps: envelope first, then the
// type-specific struct, because field shapes differ by type (for example,
// "message" is an object on user/assistant lines and a string on
// system/permission_denied).

// Message types on stdout.
const (
	typeStreamEvent     = "stream_event"
	typeAssistant       = "assistant"
	typeUser            = "user"
	typeSystem          = "system"
	typeResult          = "result"
	typeControlRequest  = "control_request"
	typeControlResponse = "control_response"
	// typeControlCancel withdraws a control request Claude sent, for example
	// a pending permission request when the turn is interrupted.
	typeControlCancel = "control_cancel_request"

	subtypeInit = "init"
)

// Stream event types inside stream_event lines.
const (
	eventMessageStart      = "message_start"
	eventContentBlockStart = "content_block_start"
	eventContentBlockDelta = "content_block_delta"
	eventContentBlockStop  = "content_block_stop"

	blockText     = "text"
	blockThinking = "thinking"
	blockToolUse  = "tool_use"

	deltaText     = "text_delta"
	deltaThinking = "thinking_delta"
)

// Control request subtypes.
const (
	controlInitialize        = "initialize"
	controlInterrupt         = "interrupt"
	controlCanUseTool        = "can_use_tool"
	controlGetSettings       = "get_settings"
	controlSetModel          = "set_model"
	controlApplyFlagSettings = "apply_flag_settings"
)

type envelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

type streamEventLine struct {
	Event streamEvent `json:"event"`
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID string `json:"id"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
}

type assistantLine struct {
	Message struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// userLine is either a replayed prompt (string content) or tool results
// (array content).
type userLine struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"tool_use_result"`
	IsReplay      bool            `json:"isReplay"`
	UUID          string          `json:"uuid"` // on a replay, the uuid we sent
}

type toolResultBlock struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type resultLine struct {
	Subtype        string   `json:"subtype"`
	IsError        bool     `json:"is_error"`
	Result         string   `json:"result"`
	Errors         []string `json:"errors"`
	TerminalReason string   `json:"terminal_reason"`
}

type controlRequestLine struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// canUseTool is the request body of a can_use_tool control request.
type canUseTool struct {
	Subtype               string            `json:"subtype"`
	ToolName              string            `json:"tool_name"`
	Input                 json.RawMessage   `json:"input"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions"`
	ToolUseID             string            `json:"tool_use_id"`
}

type controlCancelLine struct {
	RequestID string `json:"request_id"`
}

type controlResponseLine struct {
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Error     string          `json:"error"`
		Response  json.RawMessage `json:"response"`
	} `json:"response"`
}

// initializeResponse is the body of the initialize answer: the model
// catalog.
type initializeResponse struct {
	Models []struct {
		Value                 string   `json:"value"`
		ResolvedModel         string   `json:"resolvedModel"`
		SupportsEffort        bool     `json:"supportsEffort"`
		SupportedEffortLevels []string `json:"supportedEffortLevels"`
	} `json:"models"`
}

// settingsResponse is the body of the get_settings answer. Only applied
// follows runtime changes; effort is null for a model without effort.
type settingsResponse struct {
	Applied struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
	} `json:"applied"`
}

// Tool inputs and results the decoder maps.

type bashInput struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type fileInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
}

type bashResult struct {
	Stdout                   string `json:"stdout"`
	Stderr                   string `json:"stderr"`
	ReturnCodeInterpretation string `json:"returnCodeInterpretation"`
}

type fileResult struct {
	Type            string `json:"type"`
	FilePath        string `json:"filePath"`
	Content         string `json:"content"`
	StructuredPatch []hunk `json:"structuredPatch"`
}

// notebookResult is the NotebookEdit tool result. Its fields are snake_case,
// unlike Write and Edit.
type notebookResult struct {
	NotebookPath string `json:"notebook_path"`
	CellID       string `json:"cell_id"`
	EditMode     string `json:"edit_mode"` // replace, insert or delete
	OldSource    string `json:"old_source"`
	NewSource    string `json:"new_source"`
}

type hunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// Outgoing messages on stdin.

type userMessage struct {
	Type            string          `json:"type"`
	UUID            string          `json:"uuid"` // echoed in the replay
	Message         userMessageBody `json:"message"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	SessionID       string          `json:"session_id"`
}

type userMessageBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func newUserMessage(text, id string) userMessage {
	return userMessage{
		Type:      typeUser,
		UUID:      id,
		Message:   userMessageBody{Role: "user", Content: text},
		SessionID: "default",
	}
}

type controlRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   any    `json:"request"`
}

type controlResponse struct {
	Type     string              `json:"type"`
	Response controlResponseBody `json:"response"`
}

type controlResponseBody struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Response  any    `json:"response,omitempty"`
	Error     string `json:"error,omitempty"`
}

func newControlSuccess(requestID string, response any) controlResponse {
	return controlResponse{Type: typeControlResponse, Response: controlResponseBody{
		Subtype: "success", RequestID: requestID, Response: response,
	}}
}

func newControlError(requestID, msg string) controlResponse {
	return controlResponse{Type: typeControlResponse, Response: controlResponseBody{
		Subtype: "error", RequestID: requestID, Error: msg,
	}}
}

// permissionAllow is the can_use_tool response that allows a tool call.
type permissionAllow struct {
	Behavior           string            `json:"behavior"`
	UpdatedInput       json.RawMessage   `json:"updatedInput"`
	UpdatedPermissions []json.RawMessage `json:"updatedPermissions,omitempty"`
}

// permissionDeny is the can_use_tool response that refuses a tool call.
type permissionDeny struct {
	Behavior  string `json:"behavior"`
	Message   string `json:"message"`
	Interrupt bool   `json:"interrupt,omitempty"`
}
