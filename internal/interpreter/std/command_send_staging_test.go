package std_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func prepareSendLifecycle(t *testing.T, delivery *sendDelivery) (*runtime.InvocationLifecycle, runtime.Invocation) {
	t.Helper()
	invocation := prepareSendInvocation(t, delivery, "@ada work")
	return runtime.NewInvocationLifecycle(invocation, runtime.Context{
		Participants: delivery, Session: delivery,
	}), invocation
}

func TestSendLifecycle_ImmediatelyReady(t *testing.T) {
	delivery := &sendDelivery{}
	lifecycle, _ := prepareSendLifecycle(t, delivery)
	defer lifecycle.Close(errors.New("cleanup"))
	queue := make(chan runtime.CommandResult, 2)
	if err := lifecycle.Start(func(result runtime.CommandResult) { queue <- result }); err != nil {
		t.Fatal(err)
	}
	waitForSendLaunch(t, delivery)
	if !slices.Equal(delivery.order, []string{"subscribe", "participants", "subscribe", "execute"}) {
		t.Fatalf("order = %v", delivery.order)
	}
	delivery.emit(sendOutcome{Delivered: []string{"ada"}})
	delivery.drain()
	result, ok := lifecycle.Complete(receiveSendCompletion(t, queue))
	if !ok {
		t.Fatal("completion rejected")
	}
	assertSendResult(t, result, sendOutcome{Delivered: []string{"ada"}}, "@ada work", 1)
	if _, ok := lifecycle.Complete(result); ok {
		t.Fatal("duplicate accepted")
	}
}

func TestSendLifecycle_WaitsForParticipantUpdates(t *testing.T) {
	for _, status := range []participant.Status{
		participant.StatusStarting, participant.StatusAttached, participant.StatusIdle,
		participant.StatusPreparing, participant.StatusWorking, participant.StatusKeepalive,
	} {
		t.Run(string(status), func(t *testing.T) {
			delivery := newSendDelivery(t, false)
			delivery.views[0].Status, delivery.views[0].StartupReady = status, false
			lifecycle, _ := prepareSendLifecycle(t, delivery)
			defer lifecycle.Close(errors.New("cleanup"))
			if err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") }); err != nil {
				t.Fatal(err)
			}
			if delivery.launches != 0 {
				t.Fatal("initial state bypassed waiting")
			}
			delivery.queue = append(delivery.queue, session.AgentReady{Alias: delivery.frozenAlias("ada")})
			delivery.drain()
			waitForSendLaunch(t, delivery)
			if delivery.launches != 1 {
				t.Fatal("ready state did not launch once")
			}
			if reads := slices.DeleteFunc(slices.Clone(delivery.order), func(step string) bool { return step != "participants" }); len(reads) != 1 {
				t.Fatal("monitor reread participant state")
			}
		})
	}
}

func TestSendLifecycle_OptionalDepartureIsSticky(t *testing.T) {
	for _, kind := range []string{"stopped", "crashed"} {
		t.Run(kind, func(t *testing.T) {
			delivery := newSendDelivery(t, true)
			var event session.Event = session.AgentStopped{Alias: delivery.frozenAlias("tim")}
			if kind == "crashed" {
				event = session.AgentCrashed{Alias: delivery.frozenAlias("tim")}
			}
			delivery.views[0].Status = participant.StatusWorking
			delivery.duringParticipants = []session.Event{event, event, session.AgentReady{Alias: delivery.frozenAlias("tim")}}
			lifecycle, invocation := prepareSendLifecycle(t, delivery)
			discard := invocation.Staging().DiscardInform
			discards := 0
			invocation.Staging().DiscardInform = func(alias participant.Alias) { discards++; discard(alias) }
			defer lifecycle.Close(errors.New("cleanup"))
			if err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") }); err != nil {
				t.Fatal(err)
			}
			delivery.queue = append(delivery.queue, session.AgentReady{Alias: delivery.frozenAlias("ada")})
			delivery.drain()
			waitForSendLaunch(t, delivery)
			assertDiscardedNoticePlan(t, delivery, []string{"ada"})
			if discards != 1 || !slices.Equal(invocation.Staging().ToInform, []participant.Alias{delivery.frozenAlias("tim")}) {
				t.Fatal("discard repeated or changed frozen staging")
			}
		})
	}
}

func TestSendLifecycle_RequiredDepartureFails(t *testing.T) {
	for _, kind := range []string{"stopped", "crashed"} {
		t.Run(kind, func(t *testing.T) {
			delivery := newSendDelivery(t, false)
			var event session.Event = session.AgentStopped{Alias: delivery.frozenAlias("ada")}
			if kind == "crashed" {
				event = session.AgentCrashed{Alias: delivery.frozenAlias("ada")}
			}
			delivery.views[0].Status = participant.StatusWorking
			lifecycle, _ := prepareSendLifecycle(t, delivery)
			defer lifecycle.Close(errors.New("cleanup"))
			queue := make(chan runtime.CommandResult, 2)
			if err := lifecycle.Start(func(result runtime.CommandResult) { queue <- result }); err != nil {
				t.Fatal(err)
			}
			delivery.queue = append(delivery.queue, event, session.AgentReady{Alias: delivery.frozenAlias("ada")})
			delivery.drain()
			result, ok := lifecycle.Complete(receiveSendCompletion(t, queue))
			if !ok || result.Err == nil || len(result.Records) != 0 || delivery.launches != 0 {
				t.Fatalf("required departure = %#v, accepted = %v", result, ok)
			}
			if _, ok := lifecycle.Close(errors.New("shutdown")); ok {
				t.Fatal("failure settled twice")
			}
			if len(delivery.handlers) != 0 {
				t.Fatal("monitor retained a subscription")
			}
		})
	}
}

func TestSendLifecycle_InitialUnavailableParticipants(t *testing.T) {
	for _, tt := range []struct {
		name    string
		alias   string
		missing bool
		wantErr bool
	}{
		{name: "missing required", alias: "ada", missing: true, wantErr: true},
		{name: "crashed required", alias: "ada", wantErr: true},
		{name: "missing optional", alias: "tim", missing: true},
		{name: "crashed optional", alias: "tim"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			delivery := &sendDelivery{notices: true}
			lifecycle, _ := prepareSendLifecycle(t, delivery)
			defer lifecycle.Close(errors.New("cleanup"))
			if tt.missing {
				delivery.views = slices.DeleteFunc(delivery.views, func(view participant.View) bool { return view.Alias.Name == tt.alias })
			} else {
				for index := range delivery.views {
					if delivery.views[index].Alias.Name == tt.alias {
						delivery.views[index].Status = participant.StatusCrashed
					}
				}
			}
			err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") })
			if (err != nil) != tt.wantErr {
				t.Fatalf("start = %v", err)
			}
			if !tt.wantErr {
				waitForSendLaunch(t, delivery)
				assertDiscardedNoticePlan(t, delivery, []string{"ada"})
			}
		})
	}
}

func TestSendLifecycle_CloseWhileWaitingPreventsLaunch(t *testing.T) {
	delivery := newSendDelivery(t, false)
	delivery.views[0].Status = participant.StatusWorking
	lifecycle, _ := prepareSendLifecycle(t, delivery)
	if err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") }); err != nil {
		t.Fatal(err)
	}
	if _, ok := lifecycle.Complete(runtime.CommandResult{}); ok {
		t.Fatal("completion accepted before launch")
	}
	if err := lifecycle.Start(func(runtime.CommandResult) {}); !errors.Is(err, runtime.ErrInvocationNotPrepared) {
		t.Fatalf("repeated start = %v", err)
	}
	reason := errors.New("discarded")
	result, ok := lifecycle.Close(reason)
	if !ok || !errors.Is(result.Err, reason) {
		t.Fatal("discard did not settle once")
	}
	delivery.queue = append(delivery.queue, session.AgentReady{Alias: delivery.frozenAlias("ada")})
	delivery.drain()
	if delivery.launches != 0 || len(delivery.handlers) != 0 {
		t.Fatal("closed monitor launched or retained its subscription")
	}
}

func TestSendLifecycle_DelayedLaunchFailure(t *testing.T) {
	delivery := newSendDelivery(t, false)
	delivery.views[0].Status = participant.StatusWorking
	lifecycle, _ := prepareSendLifecycle(t, delivery)
	defer lifecycle.Close(errors.New("cleanup"))
	queue := make(chan runtime.CommandResult, 2)
	if err := lifecycle.Start(func(result runtime.CommandResult) { queue <- result }); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("admission failed")
	delivery.err = failure
	delivery.queue = append(delivery.queue, session.AgentReady{Alias: delivery.frozenAlias("ada")})
	delivery.drain()
	result, ok := lifecycle.Complete(receiveSendCompletion(t, queue))
	if !ok || !errors.Is(result.Err, failure) || len(result.Records) != 0 {
		t.Fatalf("launch failure = %#v, accepted = %v", result, ok)
	}
	if _, ok := lifecycle.Close(errors.New("shutdown")); ok {
		t.Fatal("failure repeated")
	}
}

func TestSendLifecycle_PreservesPartialDelivery(t *testing.T) {
	for _, wait := range []bool{false, true} {
		name := "immediate"
		if wait {
			name = "retained"
		}
		t.Run(name, func(t *testing.T) {
			delivery := newSendDelivery(t, true)
			if wait {
				delivery.views[1].Status = participant.StatusWorking
			}
			lifecycle, _ := prepareSendLifecycle(t, delivery)
			defer lifecycle.Close(errors.New("cleanup"))
			queue := make(chan runtime.CommandResult, 2)
			if err := lifecycle.Start(func(result runtime.CommandResult) { queue <- result }); err != nil {
				t.Fatal(err)
			}
			if wait {
				delivery.queue = append(delivery.queue, session.AgentReady{Alias: delivery.frozenAlias("tim")})
				delivery.drain()
			}
			waitForSendLaunch(t, delivery)
			outcome := sendOutcome{Delivered: []string{"ada"}, Failed: []string{"tim"}, Err: errors.New("notice failed")}
			delivery.emit(outcome)
			delivery.drain()
			result, ok := lifecycle.Complete(receiveSendCompletion(t, queue))
			if !ok {
				t.Fatal("partial delivery rejected")
			}
			assertSendResult(t, result, outcome, "@ada work", 1)
		})
	}
}

type failedLaunchInvocation struct {
	runtime.Invocation
	err    error
	closes int
}

func (i *failedLaunchInvocation) Go(complete func(runtime.CommandResult)) error {
	complete(runtime.CommandResult{})
	return i.err
}
func (i *failedLaunchInvocation) Close() { i.closes++; i.Invocation.Close() }

func TestSendLifecycle_FailedDelayedLaunchRejectsEarlierCallback(t *testing.T) {
	delivery := newSendDelivery(t, false)
	delivery.views[0].Status = participant.StatusWorking
	invocation := prepareSendInvocation(t, delivery, "@ada work")
	failure := errors.New("launch failed")
	wrapped := &failedLaunchInvocation{Invocation: invocation, err: failure}
	lifecycle := runtime.NewInvocationLifecycle(wrapped, runtime.Context{Participants: delivery, Session: delivery})
	defer lifecycle.Close(errors.New("cleanup"))
	queue := make(chan runtime.CommandResult, 2)
	if err := lifecycle.Start(func(result runtime.CommandResult) { queue <- result }); err != nil {
		t.Fatal(err)
	}
	delivery.queue = append(delivery.queue, session.AgentReady{Alias: delivery.frozenAlias("ada")})
	delivery.drain()
	earlier := receiveSendCompletion(t, queue)
	receiveSendCompletion(t, queue) // Failure is recorded before consuming either result.
	result, ok := lifecycle.Complete(earlier)
	if !ok || !errors.Is(result.Err, failure) || wrapped.closes != 1 {
		t.Fatalf("earlier callback superseded failure: %#v", result)
	}
	if _, ok := lifecycle.Complete(runtime.CommandResult{}); ok {
		t.Fatal("failure repeated")
	}
}

func TestSendLifecycle_InitialListDoesNotOverwriteCollectedChanges(t *testing.T) {
	for _, tt := range []struct {
		name    string
		event   func(*sendDelivery) session.Event
		wantErr bool
	}{
		{name: "busy status", event: func(delivery *sendDelivery) session.Event {
			return session.ParticipantStatusChanged{Alias: delivery.frozenAlias("ada"), From: participant.StatusIdle, To: participant.StatusWorking}
		}},
		{name: "startup wait", event: func(delivery *sendDelivery) session.Event {
			return session.ParticipantStatusChanged{Alias: delivery.frozenAlias("ada"), From: participant.StatusAttached, To: participant.StatusIdle}
		}},
		{name: "departure", event: func(delivery *sendDelivery) session.Event {
			return session.AgentStopped{Alias: delivery.frozenAlias("ada")}
		}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			delivery := newSendDelivery(t, false)
			delivery.duringParticipants = []session.Event{tt.event(delivery)}
			lifecycle, _ := prepareSendLifecycle(t, delivery)
			defer lifecycle.Close(errors.New("cleanup"))
			err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") })
			if (err != nil) != tt.wantErr || delivery.launches != 0 {
				t.Fatalf("update overwritten: %v", err)
			}
			if !tt.wantErr {
				delivery.queue = append(delivery.queue, session.AgentReady{Alias: delivery.frozenAlias("ada")})
				delivery.drain()
				waitForSendLaunch(t, delivery)
			}
		})
	}
}

// All three facts are collected while initialization is reading the list. The
// monitor must consider the departure before using the replacement's ready state.
func TestSendLifecycle_QueuedDeparturePrecedesEvaluation(t *testing.T) {
	for _, alias := range []string{"ada", "tim"} {
		t.Run(alias, func(t *testing.T) {
			delivery := newSendDelivery(t, true)
			frozen := delivery.frozenAlias(alias)
			delivery.duringParticipants = []session.Event{
				session.ParticipantStatusChanged{Alias: frozen, From: participant.StatusWorking, To: participant.StatusIdle},
				session.AgentStopped{Alias: frozen}, session.AgentReady{Alias: newTestAlias(alias)},
			}
			lifecycle, _ := prepareSendLifecycle(t, delivery)
			defer lifecycle.Close(errors.New("cleanup"))
			err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") })
			if alias == "ada" {
				if err == nil || delivery.launches != 0 {
					t.Fatal("required departure was bypassed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			waitForSendLaunch(t, delivery)
			assertDiscardedNoticePlan(t, delivery, []string{"ada"})
		})
	}
}
