package runtime

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/roomscript/coderoom/internal/session"
)

type deliveryHost struct {
	participantHost
	command      session.Command
	executeErr   error
	handle       func(ExecutionEvent)
	order        []string
	unsubscribes atomic.Int32
}

func (h *deliveryHost) Subscribe(handle func(ExecutionEvent)) func() {
	h.handle = handle
	h.order = append(h.order, "subscribe")
	return func() { h.unsubscribes.Add(1) }
}

func (h *deliveryHost) Execute(command session.Command) error {
	h.command = command
	h.order = append(h.order, "execute")
	return h.executeErr
}

func TestDelivery_TerminalEvents(t *testing.T) {
	failure := errors.New("missing outcome")
	for _, test := range []struct {
		name  string
		event ExecutionEvent
		kind  session.RoutingKind
		err   error
	}{
		{"actual outcome", ExecutionCompleted{ExecutionID: 41, Result: session.RoutingResult{Kind: "probe"}}, "probe", nil},
		{"delivery failure", ExecutionCompleted{ExecutionID: 42, Result: session.RoutingResult{Kind: "probe", Err: failure}, Err: failure}, "probe", nil},
		{"missing routing", ExecutionCompleted{ExecutionID: 43}, "", ErrInvalidDeliveryOutcome},
		{"failed without routing", ExecutionCompleted{ExecutionID: 44, Err: failure}, "", ErrInvalidDeliveryOutcome},
		{"execution failure", ExecutionFailed{Err: failure}, "", failure},
		{"invalid failure", ExecutionFailed{}, "", ErrInvalidDeliveryOutcome},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := &deliveryHost{}
			var delivery Delivery
			updates := 0
			delivery.UpdatePrepared(func() { updates++ })
			count := 0
			var outcome ExecutionCompleted
			var failure error
			if err := delivery.Go(host, session.CancelCommand{}, func(result ExecutionCompleted, err error) { count++; outcome, failure = result, err }); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(host.order, []string{"subscribe", "execute"}) {
				t.Fatalf("launch order = %v", host.order)
			}
			delivery.UpdatePrepared(func() { updates++ })
			host.handle(test.event)
			host.handle(test.event)
			delivery.Close()
			host.handle(test.event)
			delivery.UpdatePrepared(func() { updates++ })
			assertDeliveryTerminal(t, count, updates, outcome, failure, test.kind, test.err)
			expected, _ := test.event.(ExecutionCompleted)
			if outcome.ExecutionID != expected.ExecutionID || !errors.Is(outcome.Err, expected.Err) || !errors.Is(outcome.Result.Err, expected.Result.Err) {
				t.Fatalf("outcome=%#v, want %#v", outcome, expected)
			}
			if host.unsubscribes.Load() != 1 {
				t.Fatal("subscription not released once")
			}
			if err := delivery.Go(host, session.CancelCommand{}, func(ExecutionCompleted, error) {}); !errors.Is(err, ErrInvocationNotPrepared) {
				t.Fatalf("relaunch = %v", err)
			}
		})
	}
}

func assertDeliveryTerminal(t *testing.T, count, updates int, outcome ExecutionCompleted, failure error, kind session.RoutingKind, want error) {
	t.Helper()
	if count != 1 || updates != 1 || outcome.Result.Kind != kind || !errors.Is(failure, want) {
		t.Fatalf("terminal result count=%d, updates=%d, outcome=%#v, error=%v", count, updates, outcome, failure)
	}
}

func TestDelivery_CloseAndFailedLaunch(t *testing.T) {
	for _, beforeLaunch := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed admission", true: "closed before launch"}[beforeLaunch], func(t *testing.T) {
			failure := errors.New("admission failed")
			host := &deliveryHost{executeErr: failure}
			var delivery Delivery
			if beforeLaunch {
				delivery.Close()
				failure = ErrInvocationNotPrepared
			}
			err := delivery.Go(host, session.CancelCommand{}, func(ExecutionCompleted, error) { t.Error("closed delivery completed") })
			if !errors.Is(err, failure) {
				t.Fatalf("Go = %v", err)
			}
			delivery.Close()
			if beforeLaunch {
				if host.handle != nil {
					t.Fatal("closed delivery subscribed")
				}
			} else {
				host.handle(ExecutionFailed{Err: errors.New("late failure")})
				if host.unsubscribes.Load() != 1 {
					t.Fatal("failed launch retained subscription")
				}
			}
		})
	}
}

func TestDelivery_ConcurrentEventsAndClose(t *testing.T) {
	var delivery Delivery
	host := &deliveryHost{}
	var completions atomic.Int32
	if err := delivery.Go(host, session.CancelCommand{}, func(ExecutionCompleted, error) { completions.Add(1) }); err != nil {
		t.Fatal(err)
	}
	var work sync.WaitGroup
	for range 20 {
		work.Go(func() { host.handle(ExecutionCompleted{Result: session.RoutingResult{Kind: "probe"}}) })
	}
	work.Go(delivery.Close)
	work.Wait()
	if completions.Load() > 1 || host.unsubscribes.Load() != 1 {
		t.Fatal("racing events or close settled/released more than once")
	}
}
