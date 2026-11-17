package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestApprovalSettlementCancellationRace(t *testing.T) {
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan Event, 4)
		hub := newApprovalHub(func(e Event) { events <- e })
		done := make(chan error, 1)
		go func() {
			decision, err := hub.request(ctx, participant.Alias{}, approvalRequest("race"))
			if err == nil && decision.Choice != agent.OptionAccept {
				t.Error("accepted decision lost")
			}
			done <- err
		}()
		wait, stop := context.WithTimeout(context.Background(), time.Second)
		request := mustApprovalEvent(wait, t, events)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); cancel() }()
		accepted := hub.resolve(request.ID, agent.OptionAccept)
		wg.Wait()
		settled := mustApprovalClearedEvent(wait, t, events)
		select {
		case err := <-done:
			if (err == nil) != accepted || (settled.Choice == agent.OptionAccept) != accepted {
				t.Fatalf("accepted=%v, settled=%#v, err=%v", accepted, settled, err)
			}
		case <-wait.Done():
			t.Fatal("listener did not settle")
		}
		if hub.resolve(request.ID, agent.OptionAccept) || len(events) != 0 {
			t.Fatal("duplicate settlement")
		}
		stop()
	}
}

func TestApprovalAcceptedBeforeCancellationRemainsCommitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hub *approvalHub
	hub = newApprovalHub(func(event Event) {
		if request, ok := event.(ApprovalRequested); ok {
			hub.resolve(request.ID, agent.OptionAccept)
			cancel()
		}
	})
	decision, err := hub.request(ctx, participant.Alias{}, approvalRequest("approve"))
	if err != nil || decision.Choice != agent.OptionAccept {
		t.Fatalf("committed decision = %#v, %v", decision, err)
	}
}

func TestShutdownWithdrawsManualApprovalWithoutPolicy(t *testing.T) {
	events := make(chan Event, 4)
	s := New(WithObserver(approvalAuditObserver(func(e Event) { events <- e })))
	done := make(chan error, 1)
	go func() {
		_, err := s.decideApproval(context.Background(), participant.Alias{}, approvalRequest("shutdown"), s.approvals.Listener(participant.Alias{}))
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request := mustApprovalEvent(ctx, t, events)
	s.Shutdown()
	settled := mustApprovalClearedEvent(ctx, t, events)
	if settled.ID != request.ID || settled.Choice != "" {
		t.Fatalf("shutdown settlement = %#v", settled)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("shutdown accepted approval")
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not unblock approval")
	}
}

func TestBackendExitWithdrawsManualApproval(t *testing.T) {
	events := make(chan Event, 8)
	s := New(WithObserver(approvalAuditObserver(func(e Event) {
		switch e.(type) {
		case ApprovalRequested, ApprovalCleared:
			events <- e
		}
	})))
	t.Cleanup(s.Shutdown)
	p := participant.New("bill", "builder", participant.InitiativeManual)
	if err := s.addParticipant(p); err != nil {
		t.Fatal(err)
	}
	s.CreateAgentRuntime(p.Alias)
	ctx := s.CreateAgentContext(p.Alias)
	done := make(chan error, 1)
	go func() { _, err := s.ApprovalListener(p.Alias).Decide(ctx, approvalRequest("exit")); done <- err }()
	wait, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request := mustApprovalEvent(wait, t, events)
	s.handleAgentReadError(make(chan struct{}), p.Alias)
	settled := mustApprovalClearedEvent(wait, t, events)
	if settled.ID != request.ID || settled.Choice != "" || ctx.Err() == nil {
		t.Fatalf("exit settlement = %#v", settled)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("exit accepted approval")
		}
	case <-wait.Done():
		t.Fatal("exit left approval blocked")
	}
}

func TestApprovalPromotionSkipsCanceledQueuedRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := approvalQueue{}
	q.add(&pendingApproval{id: 1, ctx: context.Background()})
	q.add(&pendingApproval{id: 2, ctx: ctx})
	q.add(&pendingApproval{id: 3, ctx: context.Background()})
	_, next, ok := q.resolve(1)
	if !ok || next == nil || next.id != 3 {
		t.Fatalf("promoted canceled request: %#v", next)
	}
}
