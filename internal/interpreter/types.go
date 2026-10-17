package interpreter

import "github.com/roomscript/coderoom/internal/interpreter/api"

// CommandDetails is detached command output for transcript presentation.
type CommandDetails = api.CommandDetails

// FileChange is one file change presented in a transcript.
type FileChange = api.FileChange

// FileChangeDetails is detached file-change output for transcript presentation.
type FileChangeDetails = api.FileChangeDetails

// TranscriptChanged carries canonical record changes in interpreter processing
// order. Snapshots are for state inspection; front ends render this event stream
// rather than echoing semantic events or reconciling snapshot records.
type TranscriptChanged = api.TranscriptChanged

// ErrorCode classifies terminal submission errors independently of their
// presentation text or concrete Go error type.
type ErrorCode = api.ErrorCode

// ApprovalKind identifies an approval request without exposing agent protocol
// types to front ends.
type ApprovalKind = api.ApprovalKind

// ApprovalOption is one selectable approval response.
type ApprovalOption = api.ApprovalOption

// Approval is the active application-level approval request.
type Approval = api.Approval

// ApprovalChoice identifies the option selected by a front end.
type ApprovalChoice = api.ApprovalChoice

// Snapshot is a detached point-in-time view of interpreter state.
type Snapshot = api.Snapshot

// StagePhase describes the externally observable state of a staged submission.
type StagePhase = api.StagePhase

// StagedSubmission is the detached presentation state of one pending composer submission.
type StagedSubmission = api.StagedSubmission

// Event is an application event emitted by the interpreter.
type Event = api.Event

// StateChanged reports a new immutable application snapshot.
type StateChanged = api.StateChanged

// OperationFailed reports an asynchronous interpreter operation failure.
type OperationFailed = api.OperationFailed

// InputAccepted reports prompt-language input accepted for execution.
type InputAccepted = api.InputAccepted

// StagedInputDispatched reports the delivered routing for a staged input.
// Front ends use it to preserve transcript insertion at the dispatch boundary.
type StagedInputDispatched = api.StagedInputDispatched

// StagedInputDiscarded reports a staged input abandoned by lifecycle changes.
type StagedInputDiscarded = api.StagedInputDiscarded

// InputRejected reports input rejected before acceptance.
type InputRejected = api.InputRejected

// UnknownCommand reports valid input with no interpreter handler.
type UnknownCommand = api.UnknownCommand

// ParticipantsListed is retained for source compatibility.
//
// Deprecated: /who now returns its notice through canonical transcript records.
type ParticipantsListed = api.ParticipantsListed //nolint:staticcheck // Retain the deprecated public type for source compatibility.

// HelpEntry describes one prompt-language form without prescribing how a
// front end lays it out.
type HelpEntry = api.HelpEntry

// HelpListed reports the command metadata requested by /help.
type HelpListed = api.HelpListed

// ShellCompleted reports the structured result of a local shell command.
type ShellCompleted = api.ShellCompleted

// LoopStatus reports a user-visible bounded-loop lifecycle transition.
type LoopStatus = api.LoopStatus

// SubmissionSucceeded reports that recognized input executed or scheduled
// successfully. Asynchronous work started by the command may still be active.
type SubmissionSucceeded = api.SubmissionSucceeded

// SubmissionFailed reports the terminal failure of recognized input.
type SubmissionFailed = api.SubmissionFailed

// Observer consumes application events. Implementations should return quickly.
type Observer = api.Observer

var (
	// ErrClosed reports that the interpreter can no longer accept operations.
	ErrClosed = api.ErrClosed
	// ErrStagePending rejects new input while a staged submission awaits a stage action.
	ErrStagePending = api.ErrStagePending
)

// Shared interpreter constants.
const (
	ErrorInvalidInput           = api.ErrorInvalidInput
	ErrorStagePending           = api.ErrorStagePending
	ErrorReservedCommand        = api.ErrorReservedCommand
	ErrorCommandExists          = api.ErrorCommandExists
	ErrorExecutionFailed        = api.ErrorExecutionFailed
	ErrorParticipantUnavailable = api.ErrorParticipantUnavailable
	ApprovalCommandExecution    = api.ApprovalCommandExecution
	ApprovalFileChange          = api.ApprovalFileChange
	ApprovalPermissions         = api.ApprovalPermissions
	StagePhasePending           = api.StagePhasePending
)
