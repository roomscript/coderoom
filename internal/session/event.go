package session

import (
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

// Event reports an explicit runtime milestone from the session controller.
// A request does not imply delivery; delivery means adapter acceptance, not work
// completion. Individual event contracts specify which milestone they report.
// Only concrete event types in this package implement it.
type Event interface {
	sessionEvent()
}

// AgentStarting reports that the session has begun starting an agent process.
type AgentStarting struct{ Alias string }

// AgentReady reports that an agent is ready to receive messages. By the time
// this event fires the participant is in StatusIdle and IsSendable returns true.
type AgentReady struct{ Alias string }

// AgentStopped reports that an agent exited during an intentional shutdown path.
type AgentStopped struct{ Alias string }

// AgentCrashed reports that an agent exited unexpectedly.
type AgentCrashed struct{ Alias string }

// AgentLog carries a diagnostic line associated with an agent.
type AgentLog struct {
	Alias string
	Text  string
}

// AgentMessage carries a typed agent message without translation.
type AgentMessage struct {
	Alias string
	Msg   agent.Message
	// TurnCompleted is true only when this message caused the participant to
	// transition from working to idle.
	TurnCompleted bool
	// TurnID identifies the completed session turn when TurnCompleted is true.
	TurnID uint64
}

// ParticipantStatusChanged reports a session-driven participant status transition.
type ParticipantStatusChanged struct {
	Alias string
	From  participant.Status
	To    participant.Status
	Since time.Time
}

// Broadcast reports a shared-room broadcast command.
type Broadcast struct{ Text string }

// SharedSend reports a shared-room direct send to one addressed alias.
type SharedSend struct {
	Alias string
	Text  string
}

// SharedNotice reports a shared-room listener notice sent to one alias.
type SharedNotice struct {
	Alias string
	Text  string
}

// ContextHandoff reports a delivered handoff plus its audit metadata.
type ContextHandoff struct {
	FromAlias string
	ToAlias   string
	Text      string
	Preview   string

	SourceRecordIndex    int
	RequiredReadyAliases []string
	IdleAliases          []string
	NotReadyAliases      []string
	RejectionReason      string
}

// ApprovalRequested reports that a new approval prompt became active.
type ApprovalRequested struct {
	Alias string
	ID    int64
	Req   agent.ApprovalRequest
}

// ApprovalCleared reports that an active approval prompt should be dismissed.
type ApprovalCleared struct {
	Alias string
	ID    int64
}

func (AgentStarting) sessionEvent()            {}
func (AgentReady) sessionEvent()               {}
func (AgentStopped) sessionEvent()             {}
func (AgentCrashed) sessionEvent()             {}
func (AgentLog) sessionEvent()                 {}
func (AgentMessage) sessionEvent()             {}
func (ParticipantStatusChanged) sessionEvent() {}
func (Broadcast) sessionEvent()                {}
func (SharedSend) sessionEvent()               {}
func (SharedNotice) sessionEvent()             {}
func (ContextHandoff) sessionEvent()           {}
func (ApprovalRequested) sessionEvent()        {}
func (ApprovalCleared) sessionEvent()          {}

// HandoffSource describes the room-owned source record selected for a handoff.
type HandoffSource struct {
	Text        string
	RecordIndex int
}

// Observer receives session events. Implementations must be fast; a slow
// OnEvent blocks the goroutine that called notify (typically an agent reader
// goroutine or the Execute goroutine). If async processing is needed, buffer
// internally and dispatch off-goroutine.
type Observer interface {
	OnEvent(e Event)
}
