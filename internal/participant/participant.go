// Package participant defines the session-level entity: a named collaborator
// with a role, initiative level, and capabilities, backed by an agent process.
package participant

import (
	"errors"
	"fmt"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

var (
	// ErrWorkAlreadyStarted is returned when a participant is asked to start a
	// new turn while one is already in progress or being prepared.
	ErrWorkAlreadyStarted = errors.New("participant already working")
	// ErrStreamsStillOpen is returned when a participant is asked to become idle
	// before all tracked streams for the current turn have been closed.
	ErrStreamsStillOpen = errors.New("participant has open streams")
	// ErrNotWorking is returned when an operation requires the participant to be working.
	ErrNotWorking = errors.New("participant is not working")
	// ErrNotPreparing is returned when an operation requires the participant to be in preparing state.
	ErrNotPreparing = errors.New("participant is not in preparing state")
	// ErrStreamNotTracked is returned when a stream ID is not found in the participant's open streams.
	ErrStreamNotTracked = errors.New("participant stream is not tracked")
	// ErrNotReadyForWork is returned when a participant cannot accept work due to its current state.
	ErrNotReadyForWork = errors.New("participant not ready for work")
	// ErrNotStarting is returned when an operation requires the participant to be in starting state.
	ErrNotStarting = errors.New("participant is not starting")
	// ErrNotAttached is returned when an operation requires the participant to be in attached state.
	ErrNotAttached = errors.New("participant is not attached")
	// ErrNotIdle is returned when an operation requires the participant to be idle.
	ErrNotIdle = errors.New("participant is not idle")
	// ErrNotActive is returned when an operation requires the participant to be in Working or Preparing state.
	ErrNotActive = errors.New("participant is not active")
	// ErrNotKeepalive is returned when an operation requires the participant to be in keepalive state.
	ErrNotKeepalive = errors.New("participant is not in keepalive state")
	// ErrInvalidTurnID is returned when a working turn has no session identity.
	ErrInvalidTurnID = errors.New("participant turn ID must be non-zero")
)

// Initiative controls how autonomously a participant acts.
type Initiative string

// Supported initiative levels.
const (
	InitiativeManual     Initiative = "manual"
	InitiativeActive     Initiative = "active"
	InitiativeAutonomous Initiative = "autonomous"
)

// Status is the runtime state of a participant's agent process.
type Status string

// Supported status values.
const (
	StatusIdle      Status = "idle"
	StatusStarting  Status = "starting"
	StatusAttached  Status = "attached"  // agent process running, AgentReady not yet dispatched
	StatusPreparing Status = "preparing" // committed to a Send; anchor being established
	StatusKeepalive Status = "keepalive"
	StatusWorking   Status = "working"
	StatusCrashed   Status = "crashed"
)

// View is the observable state of a participant. It contains no live runtime
// capabilities and is safe to expose in application snapshots.
type View struct {
	Alias      string
	Role       string
	Initiative Initiative
	Status     Status
	Color      string // hex colour code, e.g. "#4ade80"; empty means default terminal colour
	Since      time.Time
	// StartupReady is set immediately before AgentReady; Idle alone does not
	// imply startup has completed.
	StartupReady bool
	// TurnID remains stable after completion to identify delayed turn events.
	TurnID uint64
}

// Participant is a named collaborator in a session.
type Participant struct {
	View

	Agent agent.Agent
	// OpenStreams tracks the participant's active turn streams. Session is the
	// sole mutator; snapshots copy the map so observers/UI can inspect it safely.
	OpenStreams map[agent.StreamID]struct{}
	// anchor is the stream whose close signals turn completion. Set by
	// BeginWorking; cleared on BecomeIdle or AbortWork.
	anchor agent.StreamID
}

// New creates a participant with the supplied identity and initiative. The
// caller advances it into its initial lifecycle state before registration.
func New(alias, role string, initiative Initiative) *Participant {
	return &Participant{View: View{
		Alias:      alias,
		Role:       role,
		Initiative: initiative,
	}}
}

// Snapshot returns a detached runtime copy for session operations.
func (p *Participant) Snapshot() Participant {
	cp := *p
	cp.OpenStreams = cloneOpenStreams(p.OpenStreams)
	return cp
}

// IsSendable requires completed startup and a lifecycle state that permits
// delivery. The session additionally checks that an agent handle is bound.
func (v View) IsSendable() bool {
	return v.StartupReady && (v.Status == StatusIdle || v.HasActiveTurn())
}

// IsRoutable preserves shared-room selection: idle and working participants
// are included; preparing, maintenance, startup, and crashed participants are not.
func (v View) IsRoutable() bool { return v.Status == StatusIdle || v.Status == StatusWorking }

// IsReadyForWork reports full startup readiness for a new exclusive turn.
func (v View) IsReadyForWork() bool { return v.StartupReady && v.Status == StatusIdle }

// HasActiveTurn reports committed work, including anchor preparation.
func (v View) HasActiveTurn() bool { return v.Status == StatusPreparing || v.Status == StatusWorking }

// IsCancellable reports the existing lifecycle eligibility for cancellation.
// The session must additionally check that the agent handle is bound.
func (v View) IsCancellable() bool { return v.IsSendable() }

// IsRemovable includes failed startup: crashed participants can be evicted
// even when they never reached StartupReady.
func (v View) IsRemovable() bool {
	if v.Status == StatusCrashed {
		return true
	}
	return v.StartupReady && (v.Status == StatusIdle || v.HasActiveTurn() || v.Status == StatusKeepalive)
}

// IsSendable adds the runtime handle guard to the shared view predicate.
func (p *Participant) IsSendable() bool { return p.Agent != nil && p.View.IsSendable() }

// IsCancellable adds the runtime handle guard to the shared view predicate.
func (p *Participant) IsCancellable() bool { return p.Agent != nil && p.View.IsCancellable() }

// BeginStartup transitions the participant into startup state.
func (p *Participant) BeginStartup(now time.Time) {
	p.resetOpenStreams()
	p.anchor = ""
	p.StartupReady = false
	p.Status = StatusStarting
	p.Since = now
}

// SessionReady marks the participant as fully started. It must be called while
// the participant is StatusIdle, immediately before AgentReady is dispatched,
// so that IsRemovable returns true by the time observers process the event.
func (p *Participant) SessionReady() error {
	if p.Status != StatusIdle {
		return ErrNotIdle
	}
	p.StartupReady = true
	return nil
}

// AttachAgent transitions the participant from Starting to Attached and binds
// the running agent process. The Attached state signals that the process is
// live but the startup sequence (commitStarted + AgentReady) has not yet
// completed; IsSendable returns false for Attached.
func (p *Participant) AttachAgent(a agent.Agent, now time.Time) error {
	if p.Status != StatusStarting {
		return ErrNotStarting
	}
	p.Agent = a
	p.Status = StatusAttached
	p.Since = now
	return nil
}

// CommitIdle transitions the participant from Attached to Idle. Called before
// AgentReady is dispatched so that IsSendable returns true when the event
// fires and observers can immediately send to the participant.
func (p *Participant) CommitIdle(now time.Time) error {
	if p.Status != StatusAttached {
		return ErrNotAttached
	}
	p.Status = StatusIdle
	p.Since = now
	return nil
}

// BecomeIdle transitions the participant from Working to Idle after a turn ends.
// The anchor must already be closed (removed from OpenStreams by CloseStream)
// before this is called.
func (p *Participant) BecomeIdle(now time.Time) error {
	if p.Status != StatusWorking {
		return ErrNotWorking
	}
	if p.anchor != "" {
		if _, open := p.OpenStreams[p.anchor]; open {
			return ErrStreamsStillOpen
		}
	} else if len(p.OpenStreams) > 0 {
		return ErrStreamsStillOpen
	}
	p.resetOpenStreams()
	p.anchor = ""
	p.Status = StatusIdle
	p.Since = now
	return nil
}

// Crash transitions the participant into crashed state.
func (p *Participant) Crash(now time.Time) {
	p.resetOpenStreams()
	p.anchor = ""
	p.Status = StatusCrashed
	p.Since = now
}

// PrepareForWork transitions the participant from Idle into Preparing.
// Resets OpenStreams and clears the anchor. Blocks concurrent sends.
func (p *Participant) PrepareForWork(now time.Time) error {
	switch p.Status {
	case StatusStarting, StatusAttached, StatusCrashed:
		return ErrNotReadyForWork
	case StatusWorking, StatusPreparing, StatusKeepalive:
		return ErrWorkAlreadyStarted
	case StatusIdle:
		// Ready to prepare.
	}
	p.resetOpenStreams()
	p.anchor = ""
	p.Status = StatusPreparing
	p.Since = now
	return nil
}

// BeginKeepalive transitions the participant from Idle into Keepalive.
func (p *Participant) BeginKeepalive(now time.Time) error {
	if p.Status != StatusIdle {
		return ErrNotIdle
	}
	p.Status = StatusKeepalive
	p.Since = now
	return nil
}

// FinishKeepalive transitions the participant from Keepalive back to Idle.
func (p *Participant) FinishKeepalive(now time.Time) error {
	if p.Status != StatusKeepalive {
		return ErrNotKeepalive
	}
	p.Status = StatusIdle
	p.Since = now
	return nil
}

// BeginWorking transitions the participant from Preparing into Working and
// records the turn-lifecycle anchor. The anchor stream is added to OpenStreams
// so it is tracked from the moment Working begins — before the adapter can
// emit any messages — closing the race between Send returning and the first
// agent message arriving.
func (p *Participant) BeginWorking(now time.Time, anchor agent.StreamID, turnID uint64) error {
	if p.Status != StatusPreparing {
		return ErrNotPreparing
	}
	if turnID == 0 {
		return ErrInvalidTurnID
	}
	p.anchor = anchor
	if anchor != "" {
		if p.OpenStreams == nil {
			p.OpenStreams = make(map[agent.StreamID]struct{})
		}
		p.OpenStreams[anchor] = struct{}{}
	}
	p.TurnID = turnID
	p.Status = StatusWorking
	p.Since = now
	return nil
}

// AbortWork rolls back a Preparing or Working state to Idle. Used when Send
// fails after PrepareForWork, or on error rollback from Working state.
func (p *Participant) AbortWork(now time.Time) error {
	if !p.HasActiveTurn() {
		return ErrNotWorking
	}
	p.resetOpenStreams()
	p.anchor = ""
	p.Status = StatusIdle
	p.Since = now
	return nil
}

// resetOpenStreams clears all active stream tracking for the participant.
func (p *Participant) resetOpenStreams() {
	p.OpenStreams = make(map[agent.StreamID]struct{})
}

// TrackStream records an active stream for the participant.
// Accepted from both StatusPreparing and StatusWorking so that messages
// arriving during the race window (Preparing) are tracked without error.
func (p *Participant) TrackStream(streamID agent.StreamID) error {
	if !p.HasActiveTurn() {
		return ErrNotActive
	}
	if p.OpenStreams == nil {
		p.OpenStreams = make(map[agent.StreamID]struct{})
	}
	p.OpenStreams[streamID] = struct{}{}
	return nil
}

// CloseStream removes a tracked stream.
// shouldIdle is true when the closed stream is the anchor and the participant
// is Working — this is the signal to call BecomeIdle.
// CloseStream is accepted from StatusWorking and StatusPreparing; it never
// returns shouldIdle=true from Preparing so premature idle is impossible.
func (p *Participant) CloseStream(streamID agent.StreamID) (shouldIdle bool, err error) {
	if !p.HasActiveTurn() {
		return false, ErrNotWorking
	}
	if _, ok := p.OpenStreams[streamID]; !ok {
		return false, fmt.Errorf("%w: %s", ErrStreamNotTracked, streamID)
	}
	delete(p.OpenStreams, streamID)
	if p.Status != StatusWorking {
		return false, nil
	}
	return streamID == p.anchor, nil
}

func cloneOpenStreams(in map[agent.StreamID]struct{}) map[agent.StreamID]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[agent.StreamID]struct{}, len(in))
	for streamID := range in {
		out[streamID] = struct{}{}
	}
	return out
}
