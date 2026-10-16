package claude

import (
	"encoding/json"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

const (
	// noticeTurnStreamID is the anchor returned by SendNotice.
	noticeTurnStreamID = agent.StreamID("claude:notice-turn")
	// noticeRelayStreamID carries a non-compliant notice reply as reasoning.
	noticeRelayStreamID = agent.StreamID("claude:notice-relay")
)

// noticeContextPrefix is prepended to every notice, as in the Codex adapter.
// Claude has no per-turn output schema, so the instruction is prompt-only.
const noticeContextPrefix = "[CONTEXT UPDATE — respond only with {\"acknowledge\":true}]\n\n"

type noticeState int

const (
	noticeActive    noticeState = iota // no reply text yet
	noticeBuffering                    // reply starts with '{': held until the turn ends
	noticeRelaying                     // reply is not JSON: relayed as reasoning
)

// noticeFilter suppresses the acknowledgement of a notice turn. It sees the
// turn's messages before Read() does: reply text is held or relayed as
// reasoning, thinking is dropped, and everything else (tool calls, logs)
// passes through, as with Codex delivery notices. Stdout reader goroutine
// only.
type noticeFilter struct {
	state noticeState
	buf   strings.Builder
}

func (f *noticeFilter) reset() {
	f.state = noticeActive
	f.buf.Reset()
}

func (f *noticeFilter) filter(msgs []agent.Message) []agent.Message {
	var out []agent.Message
	for _, m := range msgs {
		switch content := m.Content.(type) {
		case agent.Output:
			if m.Mode == agent.ModeStream {
				out = append(out, f.replyText(content.Text)...)
			}
			// Reply flushes are dropped: no output stream was opened.
		case agent.Reasoning:
			// Thinking during a notice is not shown.
		default:
			out = append(out, m)
		}
	}
	return out
}

// replyText decides from the first non-blank text whether the reply looks
// like the acknowledgement.
func (f *noticeFilter) replyText(text string) []agent.Message {
	if f.state == noticeActive {
		trimmed := strings.TrimSpace(text)
		switch {
		case trimmed == "":
			f.buf.WriteString(text)
			return nil
		case strings.HasPrefix(trimmed, "{"):
			f.state = noticeBuffering
		default:
			f.state = noticeRelaying
			text = f.buf.String() + text
			f.buf.Reset()
		}
	}
	if f.state == noticeBuffering {
		f.buf.WriteString(text)
		return nil
	}
	return []agent.Message{relay(text)}
}

// finish closes the reply at the end of the turn: an acknowledgement
// disappears, anything else ends up as reasoning.
func (f *noticeFilter) finish() []agent.Message {
	switch f.state {
	case noticeBuffering:
		if isAcknowledgement(f.buf.String()) {
			return nil
		}
		return []agent.Message{relay(f.buf.String()), relayFlush()}
	case noticeRelaying:
		return []agent.Message{relayFlush()}
	case noticeActive:
	}
	return nil
}

// isAcknowledgement accepts any JSON object with "acknowledge": true; extra
// fields are allowed, as in the Codex adapter.
func isAcknowledgement(text string) bool {
	var r struct {
		Acknowledge bool `json:"acknowledge"`
	}
	return json.Unmarshal([]byte(text), &r) == nil && r.Acknowledge
}

func relay(text string) agent.Message {
	return agent.Message{StreamID: noticeRelayStreamID, Mode: agent.ModeStream, Content: agent.Reasoning{Text: text}}
}

func relayFlush() agent.Message {
	return agent.Message{StreamID: noticeRelayStreamID, Mode: agent.ModeFlush, Content: agent.Reasoning{}}
}
