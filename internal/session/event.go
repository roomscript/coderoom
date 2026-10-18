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
type AgentStarting struct{ Alias participant.Alias }

// AgentReady reports that an agent is ready to receive messages. By the time
// this event fires the participant is in StatusIdle and IsSendable returns true.
type AgentReady struct{ Alias participant.Alias }

// AgentStopped reports that an agent exited during an intentional shutdown path.
type AgentStopped struct{ Alias participant.Alias }

// AgentCrashed reports that an agent exited unexpectedly.
type AgentCrashed struct{ Alias participant.Alias }

// ParticipantConfigChanged reports that files a participant's config was
// loaded from changed after startup. The session keeps using the loaded
// config. Sources are repo-relative paths.
type ParticipantConfigChanged struct {
	Alias   participant.Alias
	Sources []string
}

// AgentLog carries a diagnostic line associated with an agent.
type AgentLog struct {
	Alias participant.Alias
	Text  string
}

// AgentMessage carries a typed agent message without translation.
type AgentMessage struct {
	Alias participant.Alias
	Msg   agent.Message
	// WorkCompleted is true only when this message caused the participant to
	// transition from working to idle.
	WorkCompleted bool
	// ExecutionID identifies the command execution whose work completed.
	ExecutionID ExecutionID
}

// ParticipantStatusChanged reports a session-driven participant status transition.
type ParticipantStatusChanged struct {
	Alias             participant.Alias
	From              participant.Status
	To                participant.Status
	Since             time.Time
	ActiveExecutionID ExecutionID
}

// ParticipantWorkCompleted is an ordered lifecycle fact. SubscribeParticipants
// supplies it before the corresponding idle fact. The terminal AgentMessage
// separately carries the same ExecutionID and payload for canonical projection.
type ParticipantWorkCompleted struct {
	Alias       participant.Alias
	ExecutionID ExecutionID
}

func (ParticipantWorkCompleted) sessionEvent() {}

// RoutingCompleted is emitted exactly once for each routing command, after its
// delivery attempts (or rejection), before Execute returns. It carries no message
// body, including for outside-room sends. It does not report agent work completion.
// Commands execute serially; consumers associate the outcome with that execution
// burst rather than a persistent send ID. Lifecycle/agent events may interleave.
type RoutingCompleted struct {
	ExecutionID ExecutionID
	Result      RoutingResult
}

// HandoffDelivered reports a delivered handoff plus its audit metadata.
type HandoffDelivered struct {
	FromAlias participant.Alias
	ToAlias   participant.Alias
	Text      string
	Preview   string

	SourceRecordIndex    int
	RequiredReadyAliases []participant.Alias
	IdleAliases          []participant.Alias
	NotReadyAliases      []participant.Alias
}

// ApprovalRequested reports that a new approval prompt became active.
type ApprovalRequested struct {
	Alias participant.Alias
	ID    int64
	Req   agent.ApprovalRequest
}

// ApprovalCleared reports that an active approval prompt should be dismissed.
type ApprovalCleared struct {
	Alias participant.Alias
	ID    int64
}

func (AgentStarting) sessionEvent()            {}
func (ParticipantConfigChanged) sessionEvent() {}
func (AgentReady) sessionEvent()               {}
func (AgentStopped) sessionEvent()             {}
func (AgentCrashed) sessionEvent()             {}
func (AgentLog) sessionEvent()                 {}
func (AgentMessage) sessionEvent()             {}
func (ParticipantStatusChanged) sessionEvent() {}
func (RoutingCompleted) sessionEvent()         {}
func (HandoffDelivered) sessionEvent()         {}
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
