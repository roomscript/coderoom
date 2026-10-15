package monolith

import (
	"errors"
	"sync"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// commandSession admits native sends onto the existing execution lane. Routing
// subscriptions are invocation-scoped: only this adapter's dispatch can deliver
// a routing outcome, including when legacy work targets the same primary.
type commandSession struct {
	executor       *interpreterExecutor
	invocation     *ownedCommand
	mu             sync.Mutex
	handle         func(session.Event)
	admitted       bool
	closed         bool
	completionWork func()
	publication    instructionSequence
	recordCount    int
	dispatching    bool
}

func (s *commandSession) ParticipantByName(name string) (participant.View, bool) {
	return s.executor.session.ParticipantByName(name)
}

func (s *commandSession) CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan {
	return s.executor.session.CreateParticipantSendPlan(alias)
}

func (s *commandSession) Subscribe(handle func(session.Event)) func() {
	s.mu.Lock()
	if !s.closed {
		s.handle = handle
		s.executor.commandFeed.add(s)
	}
	s.mu.Unlock()
	return s.close
}

// Delegate only to the atomic Session boundary;
// controllers without that boundary cannot be used for lifecycle staging.
func (s *commandSession) SubscribeParticipants(handle func(session.Event)) ([]participant.View, func()) {
	source, ok := s.executor.session.(interface {
		SubscribeParticipants(func(session.Event)) ([]participant.View, func())
	})
	if !ok {
		panic("session controller does not support atomic participant subscriptions")
	}
	return source.SubscribeParticipants(handle)
}

func (s *commandSession) Execute(command session.Command) error {
	send, ok := command.(session.SendToParticipantCommand)
	if !ok {
		return errors.New("command adapter supports addressed sends only")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.admitted {
		return errors.New("send adapter is closed or already admitted")
	}
	e := s.executor
	e.enqueueMu.Lock()
	defer e.enqueueMu.Unlock()
	if e.closed {
		return ErrClosed
	}
	primary := send.Plan.PrimaryParticipant()
	if e.nativeSends == nil {
		e.nativeSends = make(map[participant.Alias]*commandSession)
	}
	if e.nativeSends[primary] != nil {
		return errors.New("primary already has an admitted send")
	}
	e.nativeSends[primary] = s
	e.operations.Push(nativeSendOperation{session: s, command: send})
	s.admitted = true
	return nil
}

func (s *commandSession) close() {
	s.mu.Lock()
	s.closed = true
	s.handle = nil
	s.mu.Unlock()
	s.executor.commandFeed.remove(s)
	s.executor.enqueueMu.Lock()
	for alias, owner := range s.executor.nativeSends {
		if owner == s {
			delete(s.executor.nativeSends, alias)
		}
	}
	s.executor.enqueueMu.Unlock()
}

func (s *commandSession) complete(completion invocationCompletion) {
	s.queueCompletionWork(func() {
		sequence := s.executor.settleInvocation(completion)
		s.publishCompletion(sequence, len(completion.completion.Records))
	})
}

func (s *commandSession) queueCompletionWork(work func()) {
	s.mu.Lock()
	if s.dispatching {
		if s.completionWork == nil {
			s.completionWork = work
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.executor.enqueueCompletion(scheduledOperation{work: work})
}

func (s *commandSession) publishCompletion(sequence instructionSequence, recordCount int) {
	s.mu.Lock()
	if s.dispatching {
		s.publication, s.recordCount = sequence, recordCount
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.executor.runner.Run(sequence)
}

type nativeSendOperation struct {
	session *commandSession
	command session.SendToParticipantCommand
}

func (op nativeSendOperation) apply(e *interpreterExecutor) {
	s := op.session
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.dispatching = true
	handle := s.handle
	s.mu.Unlock()
	err := e.executeCommand(op.command)
	events := e.takeSessionEvents()
	routing := routingResultFromEvents(events)
	if routing.Kind == "" {
		// A broken controller must not leave accepted work waiting forever.
		routing = session.RoutingResult{Kind: session.RoutingParticipantSend,
			Recipients: []session.RecipientResult{{Alias: op.command.Plan.PrimaryParticipant(), Role: session.RecipientPrimary}},
			Err:        errors.Join(errMissingRoutingOutcome, err)}
	}
	if handle != nil {
		handle(session.RoutingCompleted{Result: routing})
	}
	s.publishDelivery(e, events)
}

// Publish caller-owned completion work, input records, causal events, then the
// terminal outcome. Neither this adapter nor the runner owns the caller's workflow.
func (s *commandSession) publishDelivery(e *interpreterExecutor, events []session.Event) {
	s.mu.Lock()
	work := s.completionWork
	s.completionWork = nil
	s.mu.Unlock()
	if work != nil {
		work() // The owning interpreter settles its result before publication.
	}
	s.mu.Lock()
	publication, count := s.publication, s.recordCount
	s.publication = nil
	s.recordCount = 0
	s.dispatching = false
	s.mu.Unlock()
	// Input records precede the events caused by delivery, as in legacy dispatch.
	items := []runnerItem{}
	var settlement instructionSequence

	if publication != nil {
		items = append(items, instructionItems(publication[:count])...)
		settlement = append(settlement, publication[count:]...)
	}
	items = append(items, sessionEventsItem{events: events})
	items = append(items, instructionItems(settlement)...)
	e.runner.runItems(items)
}

// commandEventFeed shares the interpreter's existing observer. Routing results
// are delivered separately by the dispatch owner, never by primary alias alone.
type commandEventFeed struct {
	mu          sync.Mutex
	subscribers map[*commandSession]struct{}
}

func (f *commandEventFeed) add(s *commandSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subscribers == nil {
		f.subscribers = make(map[*commandSession]struct{})
	}
	f.subscribers[s] = struct{}{}
}
func (f *commandEventFeed) remove(s *commandSession) {
	f.mu.Lock()
	delete(f.subscribers, s)
	f.mu.Unlock()
}
func (f *commandEventFeed) deliver(event session.Event) {
	if _, routing := event.(session.RoutingCompleted); routing {
		return
	}
	f.mu.Lock()
	subscribers := make([]*commandSession, 0, len(f.subscribers))
	for s := range f.subscribers {
		subscribers = append(subscribers, s)
	}
	f.mu.Unlock()
	for _, s := range subscribers {
		s.mu.Lock()
		handle := s.handle
		s.mu.Unlock()
		if handle != nil {
			handle(event)
		}
	}
}

func (s *commandSession) release() {
	if s.invocation != nil {
		s.invocation.close()
	}
	s.close()
}
