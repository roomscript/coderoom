package interpreter

import (
	"errors"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	roomstate "github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

var (
	// ErrClosed reports that the interpreter can no longer accept operations.
	ErrClosed = errors.New("interpreter closed")
	// ErrStagePending rejects new input while a staged submission awaits a stage action.
	ErrStagePending = errors.New("submission blocked by pending stage")
)

// ErrorCode classifies terminal submission errors independently of their
// presentation text or concrete Go error type.
type ErrorCode string

// Stable terminal submission error codes.
const (
	ErrorInvalidInput           ErrorCode = "invalid_input"
	ErrorStagePending           ErrorCode = "stage_pending"
	ErrorReservedCommand        ErrorCode = "reserved_command"
	ErrorCommandExists          ErrorCode = "command_exists"
	ErrorExecutionFailed        ErrorCode = "execution_failed"
	ErrorParticipantUnavailable ErrorCode = "participant_unavailable"
)

// ApprovalKind identifies an approval request without exposing agent protocol
// types to front ends.
type ApprovalKind string

// Supported approval request kinds.
const (
	ApprovalCommandExecution ApprovalKind = "commandExecution"
	ApprovalFileChange       ApprovalKind = "fileChange"
	ApprovalPermissions      ApprovalKind = "permissions"
)

// ApprovalOption is one selectable approval response.
type ApprovalOption struct {
	ID    string
	Label string
}

// Approval is the active application-level approval request.
type Approval struct {
	ID      int64
	Alias   string
	Kind    ApprovalKind
	Prompt  string
	Options []ApprovalOption
}

// ApprovalChoice identifies the option selected by a front end.
type ApprovalChoice struct{ OptionID string }

// Snapshot is a detached point-in-time view of interpreter state.
type Snapshot struct {
	Room         roomstate.Snapshot
	Participants []participant.View
	Approval     *Approval
	Stage        *StagedSubmission
}

// StagePhase describes the externally observable state of a staged submission.
type StagePhase string

const (
	// StagePhasePending means the submission is waiting for its frozen readiness requirements.
	StagePhasePending StagePhase = "pending"
)

// StagedSubmission is the detached presentation state of one pending composer submission.
type StagedSubmission struct {
	Raw                string
	Routing            []string
	NotReadyAliases    []string
	Interruptible      []string
	Unavailable        []string
	InterruptRequested bool
	Phase              StagePhase
}

// Event is an application event emitted by the interpreter.
type Event interface{ interpreterEvent() }

// StateChanged reports a new immutable application snapshot.
type StateChanged struct{ Snapshot Snapshot }

// OperationFailed reports an asynchronous interpreter operation failure.
type OperationFailed struct {
	Raw       string
	Statement promptlang.ParsedStatement
	Operation string
	Err       error
}

// InputAccepted reports prompt-language input accepted for execution.
type InputAccepted struct {
	Statement promptlang.ParsedStatement
	Raw       string
	Routing   []string
}

// StagedInputDispatched reports the delivered routing for a staged input.
// Front ends use it to preserve transcript insertion at the dispatch boundary.
type StagedInputDispatched struct {
	Raw     string
	Routing []string
}

// HandoffCompleted presents the audit record for a completed handoff after
// its staged input has been presented.
type HandoffCompleted struct{ Preview string }

// StagedInputDiscarded reports a staged input abandoned by lifecycle changes.
type StagedInputDiscarded struct {
	Raw    string
	Reason string
}

// InputRejected reports input rejected before acceptance.
type InputRejected struct {
	Raw  string
	Code ErrorCode
	Err  error
}

// UnknownCommand reports valid input with no interpreter handler.
type UnknownCommand struct {
	Err       error
	Statement promptlang.ParsedStatement
	Raw       string
	Name      string
}

// RosterListed reports the participant roster requested by /who. Front ends
// decide how to format the participant values for presentation.
type RosterListed struct{ Participants []participant.View }

// HelpEntry describes one prompt-language form without prescribing how a
// front end lays it out.
type HelpEntry struct {
	Usage       string
	Description string
}

// HelpListed reports the command metadata requested by /help.
type HelpListed struct {
	Commands []HelpEntry
	Messages []HelpEntry
}

// ExitRequested asks a front end to end its interactive session.
type ExitRequested struct{}

// ShellCompleted reports the structured result of a local shell command.
type ShellCompleted struct {
	Raw       string
	Statement promptlang.ParsedStatement
	Command   string
	Cwd       string
	Result    shell.Result
	Output    string
}

// LoopStatus reports a user-visible bounded-loop lifecycle transition.
type LoopStatus struct{ Message string }

// SubmissionSucceeded reports that recognized input executed or scheduled
// successfully. Asynchronous work started by the command may still be active.
type SubmissionSucceeded struct {
	Raw       string
	Statement promptlang.ParsedStatement
}

// SubmissionFailed reports the terminal failure of recognized input.
type SubmissionFailed struct {
	Statement promptlang.ParsedStatement
	Raw       string
	Operation string
	Code      ErrorCode
	Err       error
}

func (StateChanged) interpreterEvent()          {}
func (OperationFailed) interpreterEvent()       {}
func (InputAccepted) interpreterEvent()         {}
func (StagedInputDispatched) interpreterEvent() {}
func (HandoffCompleted) interpreterEvent()      {}
func (StagedInputDiscarded) interpreterEvent()  {}
func (InputRejected) interpreterEvent()         {}
func (UnknownCommand) interpreterEvent()        {}
func (RosterListed) interpreterEvent()          {}
func (HelpListed) interpreterEvent()            {}
func (ExitRequested) interpreterEvent()         {}
func (ShellCompleted) interpreterEvent()        {}
func (LoopStatus) interpreterEvent()            {}
func (SubmissionSucceeded) interpreterEvent()   {}
func (SubmissionFailed) interpreterEvent()      {}

// Observer consumes application events. Implementations should return quickly.
type Observer interface{ OnEvent(Event) }

func approvalFromAgent(id int64, alias string, request agent.ApprovalRequest) Approval {
	options := make([]ApprovalOption, len(request.Options))
	for index, option := range request.Options {
		options[index] = ApprovalOption{ID: string(option), Label: approvalOptionLabel(option)}
	}
	return Approval{
		ID:      id,
		Alias:   alias,
		Kind:    ApprovalKind(request.Kind),
		Prompt:  request.Ask,
		Options: options,
	}
}

func approvalOptionLabel(option agent.ApprovalOption) string {
	switch option {
	case agent.OptionAccept:
		return "Accept"
	case agent.OptionAcceptForSession:
		return "Accept for session"
	case agent.OptionDecline:
		return "Decline"
	case agent.OptionCancel:
		return "Cancel"
	default:
		return string(option)
	}
}
