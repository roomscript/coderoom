package codex

import (
	"encoding/json"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

func isTurnError(msg rpcEnvelope) bool { return msg.Method == methodError }

func turnErrorLogMessage(msg rpcEnvelope) (agent.Message, bool) {
	var p errorNotificationParams
	if err := json.Unmarshal(msg.Params, &p); err != nil || p.Error == nil {
		return agent.Message{}, false
	}
	text := formatCodexTurnErrorLog(*p.Error, p.WillRetry)
	if text == "" {
		return agent.Message{}, false
	}
	return logMessage(text), true
}

func logMessage(text string) agent.Message {
	return agent.Message{
		StreamID: logStreamID,
		Mode:     agent.ModeSingle,
		Content:  agent.Log{Text: sanitizeLogText(text)},
	}
}

func formatCodexTurnErrorLog(err codexTurnError, willRetry bool) string {
	message := strings.TrimSpace(err.Message)
	info := strings.TrimSpace(err.CodexErrorInfo)
	details := additionalDetailsText(err)
	if message == "" && info == "" && details == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("turn error")
	if info != "" {
		b.WriteString(" [")
		b.WriteString(info)
		b.WriteString("]")
	}
	b.WriteString(": ")
	b.WriteString(primaryTurnErrorText(message, details))
	if details != "" && details != message {
		b.WriteString("\n")
		b.WriteString(details)
	}
	if willRetry {
		b.WriteString("\nwill retry: true")
	}
	return b.String()
}

func additionalDetailsText(err codexTurnError) string {
	if err.AdditionalDetails == nil {
		return ""
	}
	return strings.TrimSpace(*err.AdditionalDetails)
}

func primaryTurnErrorText(message, details string) string {
	switch {
	case message != "":
		return message
	case details != "":
		return details
	default:
		return "unknown error"
	}
}
