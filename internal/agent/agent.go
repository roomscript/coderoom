// Package agent defines the interface for managing an AI agent process and
// provides implementations for supported CLI tools.
package agent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrTurnInProgress is returned when a new turn is started while a prior turn
	// is still in flight.
	ErrTurnInProgress = errors.New("turn already in progress")
)

// StreamID identifies a logical message stream. Messages sharing an ID form one
// stream. The consumer uses it for grouping only — never parse or construct it
// outside the adapter.
//
// Consumer rule: compare StreamIDs for equality only. Never branch on prefixes,
// suffixes, or substrings. All semantic behavior is driven by content type and
// mode, not by the ID string.
type StreamID string

// Mode is the streaming lifecycle signal for a message.
type Mode int

// Streaming lifecycle signals for a Message.
const (
	ModeStream Mode = iota // fragment; a ModeFlush with the same content type follows
	ModeFlush              // stream is closed; carries the same content type as ModeStream
	ModeSingle             // standalone message; not part of a stream
)

// MessageContent is implemented only by types in this package.
type MessageContent interface {
	content()
}

// Message is a semantic unit of output from an agent turn.
type Message struct {
	StreamID StreamID
	Mode     Mode
	Content  MessageContent
}

// Accumulate merges next into m, returning the updated message.
// Used by consumers to build up stream state across ModeStream messages.
// Returns an error if the StreamIDs or content types are incompatible.
// When next is ModeFlush the returned message carries the same content as m
// (the flush payload is empty) with Mode set to ModeFlush.
func (m Message) Accumulate(next Message) (Message, error) {
	if m.StreamID != next.StreamID {
		return Message{}, fmt.Errorf("stream ID mismatch: %s vs %s", m.StreamID, next.StreamID)
	}
	switch c := m.Content.(type) {
	case Output:
		return accumulateOutput(m, c, next)
	case Reasoning:
		return accumulateReasoning(m, c, next)
	case Command:
		return accumulateCommand(m, c, next)
	case FileChangeSet:
		return accumulateFileChangeSet(m, c, next)
	}
	return Message{}, fmt.Errorf("incompatible content types: %T and %T", m.Content, next.Content)
}

func accumulateOutput(current Message, content Output, next Message) (Message, error) {
	nextContent, ok := next.Content.(Output)
	if !ok {
		return Message{}, fmt.Errorf("incompatible content types: %T and %T", current.Content, next.Content)
	}
	return Message{
		StreamID: current.StreamID,
		Mode:     next.Mode,
		Content:  Output{Text: content.Text + nextContent.Text},
	}, nil
}

func accumulateReasoning(current Message, content Reasoning, next Message) (Message, error) {
	nextContent, ok := next.Content.(Reasoning)
	if !ok {
		return Message{}, fmt.Errorf("incompatible content types: %T and %T", current.Content, next.Content)
	}
	return Message{
		StreamID: current.StreamID,
		Mode:     next.Mode,
		Content:  Reasoning{Text: content.Text + nextContent.Text},
	}, nil
}

func accumulateCommand(current Message, content Command, next Message) (Message, error) {
	nextContent, ok := next.Content.(Command)
	if !ok {
		return Message{}, fmt.Errorf("incompatible content types: %T and %T", current.Content, next.Content)
	}
	return Message{
		StreamID: current.StreamID,
		Mode:     next.Mode,
		Content: Command{
			Command:  content.Command,
			Cwd:      content.Cwd,
			Output:   mergeCommandOutput(content, nextContent),
			ExitCode: mergeExitCode(content.ExitCode, nextContent.ExitCode),
		},
	}, nil
}

func mergeExitCode(existing *int, incoming *int) *int {
	if incoming != nil {
		return incoming
	}
	return existing
}

func mergeCommandOutput(existing Command, incoming Command) string {
	if incoming.overrideOutput != nil {
		return *incoming.overrideOutput
	}
	return existing.Output + incoming.Output
}

func accumulateFileChangeSet(current Message, content FileChangeSet, next Message) (Message, error) {
	nextContent, ok := next.Content.(FileChangeSet)
	if !ok {
		return Message{}, fmt.Errorf("incompatible content types: %T and %T", current.Content, next.Content)
	}
	return Message{
		StreamID: current.StreamID,
		Mode:     next.Mode,
		Content: FileChangeSet{
			Status:  mergeToolStatus(content.Status, nextContent.Status),
			Changes: appendFileChanges(content.Changes, nextContent.Changes),
		},
	}, nil
}

func mergeToolStatus(existing ToolStatus, incoming ToolStatus) ToolStatus {
	if incoming != "" {
		return incoming
	}
	return existing
}

func appendFileChanges(existing []FileChange, incoming []FileChange) []FileChange {
	if len(incoming) == 0 {
		return existing
	}
	return append(existing, incoming...)
}

// Content types. Value receivers are deliberate: all payload fields are
// reference types in Go, so copying a struct header is always cheap. Value
// receivers avoid nil pointer concerns and keep type switch cases free of *.

// Output carries agent-produced text during a turn.
type Output struct{ Text string }

// Reasoning carries an extended-thinking fragment from the agent.
type Reasoning struct{ Text string }

// Log carries a diagnostic or process-level log line.
type Log struct{ Text string }

// KeepAlive marks completion of a backend keepalive operation.
type KeepAlive struct{}

// ToolStatus is a lifecycle marker for tool-like output items (commands, file
// changes) as reported by an adapter.
type ToolStatus string

// ToolStatus values reported by adapters.
const (
	ToolStatusInProgress ToolStatus = "inProgress"
	ToolStatusCompleted  ToolStatus = "completed"
	ToolStatusFailed     ToolStatus = "failed"
	ToolStatusDeclined   ToolStatus = "declined"
)

// Command carries a shell command execution item from the agent.
// On ModeStream: Output holds a stdout+stderr delta fragment; Command and Cwd
// are populated only on the first fragment (item/started).
// ExitCode is populated on the last stream message.
type Command struct {
	Command  string
	Cwd      string
	Output   string
	ExitCode *int
	// overrideOutput, when non-nil, replaces any accumulated Output on the
	// next Accumulate call rather than being appended. It is never set on
	// accumulated results — only on messages produced by adapters that receive
	// a canonical complete output (e.g. aggregatedOutput from item/completed).
	overrideOutput *string
}

// CommandWithOverrideOutput returns a Command whose Output, when accumulated,
// replaces any previously accumulated delta output rather than appending to it.
// output == "" is treated as absent (returns a zero-value Command).
func CommandWithOverrideOutput(output string) Command {
	if output == "" {
		return Command{}
	}
	return Command{Output: output, overrideOutput: &output}
}

// FileChange describes a single file-level change with an optional diff.
type FileChange struct {
	Path       string
	Diff       string
	ChangeKind string // "add" | "delete" | "update"
}

// FileChangeSet carries a patch/diff item from the agent.
// On ModeStream: Changes holds an incremental patch set; Status is populated
// only when known (typically item/completed).
type FileChangeSet struct {
	Status  ToolStatus
	Changes []FileChange
}

func (Output) content()        {}
func (Reasoning) content()     {}
func (Log) content()           {}
func (KeepAlive) content()     {}
func (Command) content()       {}
func (FileChangeSet) content() {}

// Agent manages the lifecycle of and communication with an AI agent process.
type Agent interface {
	// Name returns the backend's canonical name, as used in configuration and
	// shown to users.
	Name() string
	// Start launches the process and completes any required handshake.
	Start() error
	// Send writes a prompt to the agent and returns immediately.
	// Events arrive via Read(). The returned StreamID is the turn anchor: the
	// stream the adapter will flush when the turn is fully complete. The session
	// tracks it so idle is only triggered after the adapter signals turn-end.
	// Adapters that do not support anchoring return ("", nil); the session
	// degrades gracefully to per-stream-close idle detection.
	Send(prompt string) (StreamID, error)
	// SendNotice delivers context to the agent without expecting a substantive
	// response. Implementations are responsible for instructing the model to
	// acknowledge with a minimal signal and suppressing the acknowledgment from
	// Read(). Non-compliant responses surface as reasoning rather than output.
	// The returned StreamID is the notice-turn anchor (same contract as Send).
	SendNotice(prompt string) (StreamID, error)
	// Read blocks until the next meaningful message arrives from the agent.
	// Returns an error if the process has exited or the turn has failed.
	Read() (Message, error)
	// Interrupt requests the agent to stop its current in-flight work.
	// Best-effort: implementations may use protocol-level cancellation or send
	// an OS interrupt signal (e.g. SIGINT) to the underlying process.
	// Interrupt must not terminate the agent process unless there is no safe
	// alternative.
	Interrupt() error
	// Stop kills the process and blocks until it is fully reaped.
	// May be called from a different goroutine to interrupt a blocked Read.
	Stop() error
}

// Keepaliver is an optional capability for agents that need periodic idle-time
// maintenance to keep their session context warm.
type Keepaliver interface {
	KeepAliveSchedule() time.Duration
	KeepAlive() error
}

// Resumable is an optional capability for agents whose backend CLI can resume
// the session. ResumeID returns the ID that CLI accepts with --resume. It is
// valid after Start and does no I/O.
type Resumable interface {
	ResumeID() string
}

// ExternalDependencies is implemented by agents that rely on tools outside
// coderoom.
type ExternalDependencies interface {
	ExternalDependencies() []Dependency
}

// Dependency is one check. Checks run in order; later checks may assume
// earlier ones passed.
type Dependency struct {
	Description string                          // "npx is installed"
	Verify      func(ctx context.Context) error // nil means satisfied
}

// SendAndWait sends a prompt and blocks until the turn is complete,
// returning the accumulated output text.
//
// A turn may emit multiple item-scoped Output streams. If Send returns a
// non-empty anchor StreamID, SendAndWait treats a ModeFlush for that stream
// as the authoritative turn-end signal. Otherwise it falls back to the
// heuristic: all observed output streams have flushed.
func SendAndWait(a Agent, prompt string) (string, error) {
	anchorID, err := a.Send(prompt)
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	var sb []byte
	open := make(map[StreamID]struct{})
	seenOutput := false
	for {
		msg, err := a.Read()
		if err != nil {
			return "", fmt.Errorf("read: %w", err)
		}
		c, ok := msg.Content.(Output)
		if !ok {
			continue
		}
		switch msg.Mode {
		case ModeStream:
			handleOutputStream(msg, c, open, &seenOutput, &sb)
		case ModeFlush:
			if handleOutputFlush(msg, anchorID, open, seenOutput) {
				return string(sb), nil
			}
		case ModeSingle:
			handleOutputSingle(c, &seenOutput, &sb)
		}
	}
}

func handleOutputStream(msg Message, c Output, open map[StreamID]struct{}, seenOutput *bool, sb *[]byte) {
	*seenOutput = true
	open[msg.StreamID] = struct{}{}
	*sb = append(*sb, c.Text...)
}

func handleOutputFlush(msg Message, anchorID StreamID, open map[StreamID]struct{}, seenOutput bool) bool {
	if anchorID != "" && msg.StreamID == anchorID {
		return true
	}
	delete(open, msg.StreamID)
	// Heuristic fallback only when no anchor was provided.
	// With an anchor, the caller must wait for the anchor flush — returning
	// early on a content stream close would miss output from later items in
	// the same turn.
	return anchorID == "" && seenOutput && len(open) == 0
}

func handleOutputSingle(c Output, seenOutput *bool, sb *[]byte) {
	*seenOutput = true
	*sb = append(*sb, c.Text...)
}
