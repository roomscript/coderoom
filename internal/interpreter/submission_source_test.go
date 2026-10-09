package interpreter

import (
	"errors"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

func TestSubmissionSource_nativeExecutionRetainsStatementAndErrorIdentity(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	cause := errors.New("execution failed")
	sess.executeErr = cause
	raw := "  /cancel ada  "
	mustSubmit(t, interp.Submit(raw))
	accepted := receiveSubmitEvent[InputAccepted](t, events)
	if !reflect.DeepEqual(accepted.Statement, parseSourceStatement(t, raw)) {
		t.Fatal("acceptance lost parsed statement")
	}
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[StateChanged](t, events)
	failed := receiveSubmitEvent[SubmissionFailed](t, events)
	if failed.Raw != raw || !reflect.DeepEqual(failed.Statement, accepted.Statement) {
		t.Fatalf("accepted = %#v, failed = %#v", accepted, failed)
	}
	assertSourceDiagnostic(t, failed.Err, promptlang.DiagnosticCode(ErrorExecutionFailed), promptlang.Span{Start: 10, End: 13})
	if !errors.Is(failed.Err, cause) {
		t.Fatal("diagnostic lost session error identity")
	}
}

func TestSubmissionSource_validationPointsAtDefinitionOrCondition(t *testing.T) {
	tests := []struct {
		raw  string
		code promptlang.DiagnosticCode
		span promptlang.Span
	}{
		{"  /def help /shell true  ", promptlang.DiagnosticReservedCommand, promptlang.Span{Start: 7, End: 11}},
		{"/loop @ada fix /until /missing /max 3", promptlang.DiagnosticUndefinedCommand, promptlang.Span{Start: 23, End: 30}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			model := newInterpreterModel()
			t.Cleanup(model.Close)
			statement := parseSourceStatement(t, tt.raw)
			sequence := model.PrepareRequest(tt.raw, statement)
			failed := sourceEvent[SubmissionFailed](t, sequence)
			if !reflect.DeepEqual(failed.Statement, statement) {
				t.Fatal("validation lost parsed statement")
			}
			assertSourceDiagnostic(t, failed.Err, tt.code, tt.span)
		})
	}
}

func TestSubmissionSource_undefinedCommandRetainsRegistryDiagnostic(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	raw := "  /missing  "
	mustSubmit(t, interp.Submit(raw))
	unknown := receiveSubmitEvent[UnknownCommand](t, events)
	statement, ok := unknown.Statement.Value.(promptlang.CommandInvocation)
	if !ok || unknown.Raw != raw || statement.Name.Value != "missing" {
		t.Fatalf("unknown = %#v", unknown)
	}
	assertSourceDiagnostic(t, unknown.Err, promptlang.DiagnosticUndefinedCommand, promptlang.Span{Start: 3, End: 10})
	var cause promptlang.UndefinedCommandError
	if !errors.As(unknown.Err, &cause) || cause.Name != "missing" {
		t.Fatal("diagnostic lost undefined-command identity")
	}
	assertNoSubmitExecution(t, sess.executed)
}

func TestSubmissionSource_stageFailureRetainsSourceAfterQueuedSuccess(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	raw := "  @ada hello  "
	statement := parseSourceStatement(t, raw)
	sequence := model.PrepareRequest(raw, statement)
	planning := sequence[0].(prepareSendInstruction)
	sequence = model.ApplyPreparation(sendPlanResult{
		target: planning.target, targets: []string{"ada"},
		participants: []participantState{{alias: "ada", status: participant.StatusWorking}},
	})
	queued := sourceEvent[SubmissionSucceeded](t, sequence)
	if !reflect.DeepEqual(queued.Statement, statement) {
		t.Fatal("queued success lost parsed statement")
	}
	sequence, _ = model.ApplySessionEvent(session.ParticipantStatusChanged{Alias: "ada", To: participant.StatusIdle})
	dispatch := sequence[0].(executeSessionInstruction)
	sequence = model.ApplyOutcome(sessionOutcome{target: dispatch.target, err: errors.New("delivery failed")})
	failed := sourceEvent[OperationFailed](t, sequence)
	if failed.Raw != raw || model.workflows.stage.pending() {
		t.Fatalf("failure = %#v, stage pending = %v", failed, model.workflows.stage.pending())
	}
	assertSourceDiagnostic(t, failed.Err, promptlang.DiagnosticCode(ErrorExecutionFailed), promptlang.Span{Start: 3, End: 6})
}

func TestSubmissionSource_deferredHandoffFailureRetainsInput(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	raw := "  /handoff ada turing  "
	statement := parseSourceStatement(t, raw)
	sequence := model.PrepareRequest(raw, statement)
	planning := sequence[0].(readParticipantStateInstruction)
	sequence = model.ApplyPreparation(participantStateResult{
		target: planning.target, readinessRequirements: []participantState{
			{alias: "ada", status: participant.StatusKeepalive},
			{alias: "turing", status: participant.StatusIdle},
		},
	})
	sourceEvent[SubmissionSucceeded](t, sequence)
	sequence, _ = model.ApplySessionEvent(session.ParticipantStatusChanged{
		Alias: "ada", From: participant.StatusKeepalive, To: participant.StatusIdle,
	})
	read := sequence[0].(readHandoffSourceInstruction)
	sequence = model.ApplyPreparation(handoffSourceResult{target: read.target, ok: false})
	failed := sourceEvent[OperationFailed](t, sequence)
	if failed.Raw != raw || !reflect.DeepEqual(failed.Statement, statement) || model.workflows.stage.pending() {
		t.Fatalf("failure = %#v, stage pending = %v", failed, model.workflows.stage.pending())
	}
	assertSourceDiagnostic(t, failed.Err, promptlang.DiagnosticCode(ErrorExecutionFailed), statement.Span)
	if !errors.Is(failed.Err, errNoHandoffSource) {
		t.Fatal("diagnostic lost handoff error identity")
	}
}

func TestSubmissionSource_asyncShellRetainsInvocation(t *testing.T) {
	cause := errors.New("shell failed")
	runner := &fakeShellRunner{result: shell.Result{Status: shell.StatusFailure, Err: cause}}
	interp, events := newShellTestInterpreter(t, runner)
	raw := "  /shell false  "
	mustSubmit(t, interp.Submit(raw))
	accepted := receiveSubmitEvent[InputAccepted](t, events)
	started := receiveSubmitEvent[SubmissionSucceeded](t, events)
	completed := receiveSubmitEvent[ShellCompleted](t, events)
	if completed.Raw != raw || !reflect.DeepEqual(completed.Statement, accepted.Statement) || !reflect.DeepEqual(started.Statement, accepted.Statement) {
		t.Fatalf("started = %#v, completed = %#v", started, completed)
	}
	assertSourceDiagnostic(t, completed.Result.Err, promptlang.DiagnosticCode(ErrorExecutionFailed), promptlang.Span{Start: 9, End: 14})
	if !errors.Is(completed.Result.Err, cause) {
		t.Fatal("diagnostic lost shell error identity")
	}
}

func TestSubmissionSource_staleLoopShellReportsItsOwnSource(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	oldRaw := "/loop @ada old /until /tests /max 1"
	oldStatement := parseSourceStatement(t, oldRaw)
	newStatement := parseSourceStatement(t, "/loop @ben new /until /checks /max 2").Value.(promptlang.Loop)
	model.workflows.loop.active = &loopState{generation: 2, statement: newStatement, phase: loopEvaluating}
	sequence := model.ApplyOutcome(shellOutcome{
		target:  workflowRef{kind: workflowLoop, generation: 1},
		request: shellRequest{raw: oldRaw, statement: oldStatement, command: "/tests"},
		result:  shell.Result{Status: shell.StatusFailure, Err: errors.New("old shell failed")},
	})
	completed := sourceEvent[ShellCompleted](t, sequence)
	if completed.Raw != oldRaw || !reflect.DeepEqual(completed.Statement, oldStatement) {
		t.Fatalf("stale completion = %#v", completed)
	}
	if model.workflows.loop.active.generation != 2 {
		t.Fatal("stale completion advanced replacement loop")
	}
	assertSourceDiagnostic(t, completed.Result.Err, promptlang.DiagnosticCode(ErrorExecutionFailed), oldStatement.Value.(promptlang.Loop).Condition.Span)
}

func TestSubmissionSource_rejectedLoopPreservesActiveSource(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	definition := parseSourceStatement(t, "/def tests /shell true").Value.(promptlang.CommandDefinition)
	if err := model.commands.Define(definition); err != nil {
		t.Fatal(err)
	}
	raw := "/loop @ada old /until /tests /max 1"
	statement := parseSourceStatement(t, raw)
	model.PrepareRequest(raw, statement)
	replacementRaw := "  /loop @ben new /until /tests /max 2  "
	replacement := parseSourceStatement(t, replacementRaw)
	failed := sourceEvent[SubmissionFailed](t, model.PrepareRequest(replacementRaw, replacement))
	if !errors.Is(failed.Err, errLoopAlreadyActive) || !reflect.DeepEqual(failed.Statement, replacement) {
		t.Fatalf("rejection = %#v", failed)
	}
	assertSourceDiagnostic(t, failed.Err, promptlang.DiagnosticCode(ErrorExecutionFailed), replacement.Span)
	request := model.workflows.loop.active.conditionRequest()
	if request.raw != raw || !reflect.DeepEqual(request.statement, statement) {
		t.Fatalf("active condition lost original source: %#v", request)
	}
}

func parseSourceStatement(t *testing.T, raw string) promptlang.ParsedStatement {
	t.Helper()
	statement, err := promptlang.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return statement
}

func sourceEvent[T Event](t *testing.T, sequence instructionSequence) T {
	t.Helper()
	for _, item := range sequence {
		if published, ok := item.(publishEventInstruction); ok {
			if event, ok := published.event.(T); ok {
				return event
			}
		}
	}
	var zero T
	t.Fatalf("no %T in %#v", zero, sequence)
	return zero
}

func assertSourceDiagnostic(t *testing.T, err error, code promptlang.DiagnosticCode, span promptlang.Span) {
	t.Helper()
	var diagnostic *promptlang.Diagnostic
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Span != span {
		t.Fatalf("diagnostic = %#v, error = %v; want %s at %#v", diagnostic, err, code, span)
	}
}
