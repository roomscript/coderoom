package interpreter_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestSubmissionSource_unroutedCommandsPreserveSourceAndCause(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		span promptlang.Span
	}{
		{"  /invite ada  ", promptlang.Span{Start: 10, End: 13}},
		{"  /remove ada  ", promptlang.Span{Start: 10, End: 13}},
		{"  /cancel ada  ", promptlang.Span{Start: 10, End: 13}},
		{"  /policy enable send-notices  ", promptlang.Span{Start: 17, End: 29}},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			interp, sess, events := newSubmitExample()
			defer interp.Close()
			cause := errors.New("Session execution failed")
			sess.executeErr = cause
			parsed, err := promptlang.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := interp.Submit(tt.raw); err != nil {
				t.Fatal(err)
			}
			accepted := receiveEvent[interpreter.InputAccepted](t, events)
			receiveCommand(t, sess.executed)
			receiveEvent[interpreter.StateChanged](t, events)
			failed := receiveEvent[interpreter.SubmissionFailed](t, events)
			if !reflect.DeepEqual(accepted.Statement, parsed) || !reflect.DeepEqual(failed.Statement, parsed) || failed.Raw != tt.raw {
				t.Fatalf("accepted=%#v failed=%#v", accepted, failed)
			}
			assertPublicSourceDiagnostic(t, failed.Err, tt.span, cause)
		})
	}
}

func assertPublicSourceDiagnostic(t *testing.T, err error, span promptlang.Span, cause error) {
	t.Helper()
	var diagnostic *promptlang.Diagnostic
	if !errors.As(err, &diagnostic) {
		t.Fatalf("missing diagnostic: %v", err)
	}
	if diagnostic.Code != promptlang.DiagnosticCode(interpreter.ErrorExecutionFailed) || diagnostic.Span != span || !errors.Is(err, cause) {
		t.Fatalf("diagnostic=%#v cause=%v", diagnostic, cause)
	}
}
