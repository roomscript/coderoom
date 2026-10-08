package room

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/agent"
)

// Kind identifies the source and semantic type of a room record.
type Kind int

// Record kind values.
const (
	KindUserInput Kind = iota
	KindAgentOutput
	KindSystem
	KindLog
	KindReasoning
	KindCommand
	KindFileChange
)

// Record is the canonical chat-visible unit owned by the room package.
type Record struct {
	Kind  Kind
	Alias string
	// Routing contains only recipients that accepted delivery.
	Routing       []string
	FailedRouting []string
	UnsentRouting []string
	Text          string
	Msg           *agent.Message
	HandoffSource bool
}

// NewAgentRecord constructs a canonical record backed by an agent message.
func NewAgentRecord(alias string, msg agent.Message) Record {
	msgCopy := msg
	return Record{
		Kind:  kindForAgentMessage(msg),
		Alias: alias,
		Msg:   &msgCopy,
		Text:  bodyFromAgentMessage(msg),
	}
}

// Accumulate merges next into the record's backing agent message.
func (r Record) Accumulate(next agent.Message) (Record, error) {
	if r.Msg == nil {
		return Record{}, fmt.Errorf("record has no message")
	}
	accumulated, err := r.Msg.Accumulate(next)
	if err != nil {
		return Record{}, fmt.Errorf("accumulate message: %w", err)
	}
	accumulatedCopy := accumulated
	r.Msg = &accumulatedCopy
	r.Text = bodyFromAgentMessage(accumulated)
	return r, nil
}

func bodyFromAgentMessage(msg agent.Message) string {
	switch c := msg.Content.(type) {
	case agent.Output:
		return c.Text
	case agent.Reasoning:
		return c.Text
	case agent.Command:
		return c.Output
	case agent.FileChangeSet:
		return formatFileChangeBody(c.Changes)
	}
	return ""
}

func kindForAgentMessage(msg agent.Message) Kind {
	switch msg.Content.(type) {
	case agent.Reasoning:
		return KindReasoning
	case agent.Command:
		return KindCommand
	case agent.FileChangeSet:
		return KindFileChange
	default:
		return KindAgentOutput
	}
}
