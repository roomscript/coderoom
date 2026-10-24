package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	"github.com/roomscript/coderoom/internal/participant"
)

type identityObserver struct{ events []Event }

func (o *identityObserver) OnEvent(event Event) { o.events = append(o.events, event) }

type identityCallbackCase struct {
	name   string
	status participant.Status
	run    func(*testing.T, *Session, participant.Alias)
}

func startupCallbackCases() []identityCallbackCase {
	return []identityCallbackCase{
		{"startup failure", participant.StatusStarting, func(_ *testing.T, s *Session, old participant.Alias) {
			handleInviteStartError(old, errors.New("start failed"), s)
		}},
		{"late attachment", participant.StatusStarting, func(t *testing.T, s *Session, old participant.Alias) {
			if _, _, ok := s.attachParticipant(old, echo.New(), startupMetadata{}); ok {
				t.Fatal("old startup attached to replacement")
			}
		}},
		{"late startup commit", participant.StatusAttached, func(t *testing.T, s *Session, old participant.Alias) {
			if _, ok := s.commitStarted(old, participant.StatusStarting); ok {
				t.Fatal("old startup committed replacement")
			}
		}},
		{"startup cleanup", participant.StatusStarting, func(_ *testing.T, s *Session, old participant.Alias) {
			stopUnreadyParticipant(old, echo.New(), s)
		}},
		{"approval", participant.StatusWorking, func(t *testing.T, s *Session, old participant.Alias) {
			_, err := s.ApprovalListener(old).Decide(context.Background(), approvalRequest("stale approval"))
			if err == nil || s.approvals.queue.active != nil {
				t.Fatal("old approval entered the Session queue")
			}
		}},
	}
}

func turnCallbackCases() []identityCallbackCase {
	return []identityCallbackCase{
		{"reader crash", participant.StatusWorking, func(_ *testing.T, s *Session, old participant.Alias) {
			s.handleAgentReadError(make(chan struct{}), old)
		}},
		{"reader departure", participant.StatusWorking, func(_ *testing.T, s *Session, old participant.Alias) {
			stop := make(chan struct{})
			close(stop)
			s.handleAgentReadError(stop, old)
		}},
		{"output", participant.StatusWorking, func(_ *testing.T, s *Session, old participant.Alias) {
			s.handleAgentMessage(old, agent.Message{Mode: agent.ModeStream, StreamID: "old-output", Content: agent.Output{Text: "old output"}})
		}},
		{"stream completion", participant.StatusWorking, func(_ *testing.T, s *Session, old participant.Alias) {
			s.handleAgentMessage(old, agent.Message{Mode: agent.ModeFlush, StreamID: "anchor", Content: agent.Output{}})
		}},
		{"keepalive completion", participant.StatusKeepalive, func(_ *testing.T, s *Session, old participant.Alias) {
			s.handleAgentMessage(old, agent.Message{Content: agent.KeepAlive{}})
		}},
		{"abort", participant.StatusWorking, func(_ *testing.T, s *Session, old participant.Alias) { s.abortWork(old) }},
		{"turn start", participant.StatusPreparing, func(_ *testing.T, s *Session, old participant.Alias) {
			s.beginParticipantWorking(old, "old-anchor")
		}},
		{"notice stream", participant.StatusWorking, func(_ *testing.T, s *Session, old participant.Alias) {
			s.trackAnchorStream(old, "old-notice")
		}},
		{"runtime creation", participant.StatusWorking, func(t *testing.T, s *Session, old participant.Alias) {
			s.CreateAgentRuntime(old)
			if ctx := s.CreateAgentContext(old); ctx.Err() == nil {
				t.Fatal("old alias acquired a live context")
			}
			if _, ok := s.agents[old]; ok {
				t.Fatal("old alias acquired runtime membership")
			}
		}},
	}
}

func TestBackgroundCallbacks_doNotMutateReplacement(t *testing.T) {
	cases := append(startupCallbackCases(), turnCallbackCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observer := &identityObserver{}
			s := New(WithObserver(observer))
			t.Cleanup(s.Shutdown)
			old, current, ctx := registerReplacementForCallback(t, s, tc.status)
			before := current.Snapshot()
			tc.run(t, s, old)
			if !reflect.DeepEqual(current.Snapshot(), before) || ctx.Err() != nil {
				t.Fatal("old callback changed replacement state or canceled its context")
			}
			if view, ok := s.ParticipantByName("ada"); !ok || view.Alias != current.Alias {
				t.Fatal("old callback removed replacement membership")
			}
			assertCallbackEventCount(t, tc.name, observer.events)
			assertOldCallbackEvents(t, observer.events, old)
		})
	}
}

func assertCallbackEventCount(t *testing.T, name string, events []Event) {
	t.Helper()
	want := 0
	switch name {
	case "startup failure":
		want = 2
	case "reader crash", "reader departure", "output", "stream completion":
		want = 1
	}
	if len(events) != want {
		t.Fatalf("callback emitted %d events, want %d", len(events), want)
	}
}

func registerReplacementForCallback(t *testing.T, s *Session, status participant.Status) (participant.Alias, *participant.Participant, context.Context) {
	t.Helper()
	original := participant.New("ada", "builder", participant.InitiativeManual)
	if err := s.addParticipant(original); err != nil {
		t.Fatal(err)
	}
	s.CreateAgentRuntime(original.Alias)
	oldCtx := s.CreateAgentContext(original.Alias)
	s.cancelAgentContext(original.Alias)
	s.changeParticipants(func() { s.removeParticipantLocked(original.Alias) })
	if oldCtx.Err() == nil {
		t.Fatal("old participant context remained live after removal")
	}
	current := participant.New("ada", "builder", participant.InitiativeManual)
	current.Agent = echo.New()
	current.Status = participant.StatusIdle
	if err := current.PrepareForWork(s.now(), 41); err != nil {
		t.Fatal(err)
	}
	if err := current.BeginWorking(s.now(), "anchor"); err != nil {
		t.Fatal(err)
	}
	current.Status = status
	current.StartupReady = status != participant.StatusStarting && status != participant.StatusAttached
	if err := s.addParticipant(current); err != nil {
		t.Fatal(err)
	}
	s.CreateAgentRuntime(current.Alias)
	return original.Alias, current, s.CreateAgentContext(current.Alias)
}

func assertOldCallbackEvents(t *testing.T, events []Event, old participant.Alias) {
	t.Helper()
	for _, event := range events {
		var alias participant.Alias
		switch event := event.(type) {
		case AgentLog:
			alias = event.Alias
		case AgentCrashed:
			alias = event.Alias
		case AgentStopped:
			alias = event.Alias
		case AgentMessage:
			alias = event.Alias
			if event.WorkCompleted || event.ExecutionID != 0 {
				t.Fatal("old completion completed replacement turn")
			}
		default:
			t.Fatalf("old callback published unexpected event: %#v", event)
		}
		if alias != old {
			t.Fatalf("old event lost originating alias: %+v, want %+v", alias, old)
		}
	}
}
