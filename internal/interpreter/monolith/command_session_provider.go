package monolith

import (
	"sync"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// commandSessionProvider owns an invocation's execution scopes and ordered
// publication boundary. Each child scope has independent admission and events.
// The interpreter drives publication on its lane; workers may queue completions.
type commandSessionProvider struct {
	executor       *interpreterExecutor
	invocation     *ownedCommand // temporary SubmitCommand compatibility ownership
	mu             sync.Mutex
	closed         bool
	executions     map[*commandSession]struct{}
	dispatching    bool
	completionWork func()
	publication    instructionSequence
	recordCount    int
}

var _ runtime.SessionExecutionProvider = (*commandSessionProvider)(nil)

func (s *commandSessionProvider) CreateExecution() runtime.SessionExecution {
	return s.createExecution()
}

func (s *commandSessionProvider) createExecution() *commandSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution := &commandSession{provider: s, closed: s.closed}
	if !s.closed {
		if s.executions == nil {
			s.executions = make(map[*commandSession]struct{})
		}
		s.executions[execution] = struct{}{}
	}
	return execution
}

func (s *commandSessionProvider) ParticipantByName(name string) (participant.View, bool) {
	return s.executor.session.ParticipantByName(name)
}

func (s *commandSessionProvider) CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan {
	return s.executor.session.CreateParticipantSendPlan(alias)
}

// Delegate lifecycle subscriptions to the atomic Session boundary.
func (s *commandSessionProvider) SubscribeParticipants(handle func(session.Event)) (session.ParticipantSnapshot, func()) {
	source, ok := s.executor.session.(interface {
		SubscribeParticipants(func(session.Event)) (session.ParticipantSnapshot, func())
	})
	if !ok {
		panic("session controller does not support atomic participant subscriptions")
	}
	return source.SubscribeParticipants(handle)
}

func (s *commandSessionProvider) close() {
	s.mu.Lock()
	s.closed = true
	executions := make([]*commandSession, 0, len(s.executions))
	for execution := range s.executions {
		executions = append(executions, execution)
	}
	s.executions = nil
	s.mu.Unlock()
	for _, execution := range executions {
		execution.close()
	}
}

func (s *commandSessionProvider) complete(completion invocationCompletion) {
	s.queueCompletionWork(func() {
		sequence := s.executor.settleInvocation(completion)
		s.publishCompletion(sequence, len(completion.completion.Records))
	})
}

func (s *commandSessionProvider) queueCompletionWork(work func()) {
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

func (s *commandSessionProvider) publishCompletion(sequence instructionSequence, recordCount int) {
	s.mu.Lock()
	if s.dispatching {
		s.publication, s.recordCount = sequence, recordCount
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.executor.runner.Run(sequence)
}

// Publish caller-owned completion work, input records, causal events, then the
// terminal outcome. Neither this adapter nor the runner owns the caller's workflow.
func (s *commandSessionProvider) publishDelivery(e *interpreterExecutor, events []session.Event) {
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

func (s *commandSessionProvider) release() {
	if s.invocation != nil {
		s.invocation.close()
	}
	s.close()
}
