package interpreter

import (
	"errors"
	"testing"
)

func TestEventDispatcher_deliversEventsInPublicationOrder(t *testing.T) {
	dispatcher := newEventDispatcher()
	t.Cleanup(dispatcher.Close)
	delivered := make(chan Event, 2)
	dispatcher.addObserver(submitContractObserver{events: delivered})

	dispatcher.Publish(UnknownCommand{Raw: "/first", Name: "first"})
	dispatcher.Publish(InputRejected{Raw: "/second"})
	dispatcher.Flush()

	if _, ok := (<-delivered).(UnknownCommand); !ok {
		t.Fatal("first delivered event was not UnknownCommand")
	}
	if _, ok := (<-delivered).(InputRejected); !ok {
		t.Fatal("second delivered event was not InputRejected")
	}
}

func TestEventDispatcher_slowObserverDoesNotBlockPublish(t *testing.T) {
	dispatcher := newEventDispatcher()
	entered := make(chan struct{})
	release := make(chan struct{})
	dispatcher.addObserver(blockingEventObserver{
		entered:   entered,
		release:   release,
		delivered: make(chan Event, 2),
	})

	dispatcher.Publish(UnknownCommand{Raw: "/first", Name: "first"})
	receiveSignal(t, entered, "observer delivery")
	published := make(chan struct{})
	go func() {
		dispatcher.Publish(InputRejected{Raw: "/second"})
		close(published)
	}()
	receiveSignal(t, published, "second publication")

	close(release)
	dispatcher.Close()
}

func TestClose_flushesPublishedEventsThroughObservers(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	delivered := make(chan Event, 2)
	interp, _ := newSubmitContractInterpreterWithoutCleanup(t, WithObserver(blockingEventObserver{
		entered:   entered,
		release:   release,
		delivered: delivered,
	}))
	receiveSubmitEvent[StateChanged](t, delivered)

	interp.executor.publish(UnknownCommand{Raw: "/not-defined", Name: "not-defined"})
	receiveSignal(t, entered, "first event delivery")
	interp.executor.publish(InputRejected{Raw: "/invite", Err: errors.New("invalid input")})

	closed := make(chan struct{})
	go func() {
		interp.Close()
		close(closed)
	}()
	assertNoSignal(t, closed, "Close returned before published events were delivered")
	close(release)
	receiveSignal(t, closed, "Close")

	if _, ok := (<-delivered).(UnknownCommand); !ok {
		t.Fatal("first event was not UnknownCommand")
	}
	if _, ok := (<-delivered).(InputRejected); !ok {
		t.Fatal("second event was not InputRejected")
	}
}

func TestClose_flushesAcceptedSubmissionOutcome(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	delivered := make(chan Event, 3)
	interp, sess := newSubmitContractInterpreterWithoutCleanup(t, WithObserver(blockingAcceptedObserver{
		entered:   entered,
		release:   release,
		delivered: delivered,
	}))
	receiveSubmitEvent[StateChanged](t, delivered)

	mustSubmit(t, interp.Submit("/cancel ada"))
	receiveSignal(t, entered, "input acceptance delivery")
	receiveSubmitCommand(t, sess.executed)
	closed := make(chan struct{})
	go func() {
		interp.Close()
		close(closed)
	}()
	assertNoSignal(t, closed, "Close returned before submission outcome was delivered")
	close(release)
	receiveSignal(t, closed, "Close")

	if _, ok := (<-delivered).(InputAccepted); !ok {
		t.Fatal("first event was not InputAccepted")
	}
	if _, ok := (<-delivered).(StateChanged); !ok {
		t.Fatal("second event was not StateChanged")
	}
	if _, ok := (<-delivered).(SubmissionSucceeded); !ok {
		t.Fatal("terminal event was not SubmissionSucceeded")
	}
}

type blockingEventObserver struct {
	entered   chan struct{}
	release   chan struct{}
	delivered chan Event
}

type blockingAcceptedObserver struct {
	entered   chan struct{}
	release   chan struct{}
	delivered chan Event
}

func (o blockingAcceptedObserver) OnEvent(event Event) {
	if _, transcript := event.(TranscriptChanged); transcript {
		return
	}
	if _, accepted := event.(InputAccepted); accepted {
		close(o.entered)
		<-o.release
	}
	o.delivered <- event
}

func (o blockingEventObserver) OnEvent(event Event) {
	if _, unknown := event.(UnknownCommand); unknown {
		close(o.entered)
		<-o.release
	}
	o.delivered <- event
}

func newSubmitContractInterpreterWithoutCleanup(t *testing.T, opts ...Option) (*Interpreter, *submitContractSession) {
	t.Helper()
	sess := newSubmitContractSession()
	interp := New(t.Context(), sess, t.TempDir(), opts...)
	return interp, sess
}
