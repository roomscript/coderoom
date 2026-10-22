package session

import (
	"errors"
	"fmt"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
)

type prepareErrorObserver struct{ logs []AgentLog }

func (o *prepareErrorObserver) OnEvent(event Event) {
	if log, ok := event.(AgentLog); ok {
		o.logs = append(o.logs, log)
	}
}

func TestPrepareError_distinguishesAvailabilityFromInvariant(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		logs  int
	}{
		{"busy", participant.ErrWorkAlreadyStarted, 0},
		{"not ready", participant.ErrNotReadyForWork, 0},
		{"missing", errParticipantNotFound, 0},
		{"invalid execution", participant.ErrInvalidExecutionID, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := &prepareErrorObserver{}
			s := New(WithObserver(observer))
			t.Cleanup(s.Shutdown)
			alias := participant.New("ada", "", participant.InitiativeManual).Alias
			err := reportParticipantPrepareError(alias, fmt.Errorf("prepare for work: %w", tt.cause), s)
			if !errors.Is(err, tt.cause) || len(observer.logs) != tt.logs {
				t.Fatalf("error=%v logs=%v", err, observer.logs)
			}
		})
	}
}
