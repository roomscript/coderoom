package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

type approvalNotifier func(Event)

type approvalListenerFunc func(context.Context, agent.ApprovalRequest) (agent.ApprovalDecision, error)

func (f approvalListenerFunc) Decide(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return f(ctx, req)
}

type pendingApproval struct {
	id    int64
	alias participant.Alias
	req   agent.ApprovalRequest
	resp  chan agent.ApprovalDecision
}

type approvalQueue struct {
	approvalID int64
	active     *pendingApproval
	queue      []*pendingApproval
}

type cancelOutcome int

const (
	cancelNoVisibleChange cancelOutcome = iota
	cancelShowNextApproval
	cancelClearVisibleApproval
)

func (q *approvalQueue) add(pending *pendingApproval) (publishNow bool) {
	if q.active == nil {
		q.active = pending
		return true
	}
	q.queue = append(q.queue, pending)
	return false
}

func (q *approvalQueue) resolve(id int64) (resolved *pendingApproval, next *pendingApproval, ok bool) {
	if q.active == nil {
		return nil, nil, false
	}
	if id != 0 && q.active.id != id {
		return nil, nil, false
	}

	resolved = q.active
	q.active = q.pop()
	return resolved, q.active, true
}

func (q *approvalQueue) cancel(id int64) (cleared *pendingApproval, next *pendingApproval, outcome cancelOutcome) {
	if q.active != nil && q.active.id == id {
		cleared = q.active
		q.active = q.pop()
		if q.active != nil {
			return cleared, q.active, cancelShowNextApproval
		}
		return cleared, nil, cancelClearVisibleApproval
	}

	for i, pending := range q.queue {
		if pending.id != id {
			continue
		}
		q.queue = append(q.queue[:i], q.queue[i+1:]...)
		return pending, nil, cancelNoVisibleChange
	}
	return nil, nil, cancelNoVisibleChange
}

func (q *approvalQueue) pop() *pendingApproval {
	if len(q.queue) == 0 {
		return nil
	}
	next := q.queue[0]
	q.queue = q.queue[1:]
	return next
}

type approvalHub struct {
	mu         sync.Mutex
	queue      *approvalQueue
	notify     approvalNotifier
	events     []Event
	publishing bool
}

func newApprovalHub(notify approvalNotifier) *approvalHub {
	return &approvalHub{queue: &approvalQueue{}, notify: notify}
}

func (h *approvalHub) Listener(alias participant.Alias) agent.ApprovalListener {
	return approvalListenerFunc(func(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
		if h == nil {
			return agent.ApprovalDecision{}, fmt.Errorf("approval listener for %q: hub is nil", alias.Name)
		}
		return h.request(ctx, alias, req)
	})
}

func (h *approvalHub) request(ctx context.Context, alias participant.Alias, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	if err := ctx.Err(); err != nil {
		return agent.ApprovalDecision{}, fmt.Errorf("approval canceled: %w", err)
	}
	if h.queue == nil {
		return agent.ApprovalDecision{}, errors.New("approval queue is nil")
	}
	if h.notify == nil {
		return agent.ApprovalDecision{}, errors.New("approval notifier is nil")
	}

	pending := &pendingApproval{
		alias: alias,
		req:   req,
		resp:  make(chan agent.ApprovalDecision, 1),
	}

	h.mu.Lock()
	h.queue.approvalID++
	pending.id = h.queue.approvalID
	publishNow := h.queue.add(pending)
	if publishNow {
		h.enqueue(pending)
	}
	h.mu.Unlock()
	h.publishEvents()

	select {
	case d := <-pending.resp:
		if err := ctx.Err(); err != nil {
			return agent.ApprovalDecision{}, fmt.Errorf("approval canceled: %w", err)
		}
		return d, nil
	case <-ctx.Done():
		h.cancel(pending.id)
		return agent.ApprovalDecision{}, fmt.Errorf("approval request %d canceled: %w", pending.id, ctx.Err())
	}
}

func (h *approvalHub) resolve(id int64, choice agent.ApprovalOption) bool {
	if h.queue == nil {
		return false
	}

	h.mu.Lock()
	if h.queue.active == nil || !slices.Contains(h.queue.active.req.Options, choice) {
		h.mu.Unlock()
		return false
	}

	resolved, next, ok := h.queue.resolve(id)
	if !ok {
		h.mu.Unlock()
		return false
	}

	resolved.resp <- agent.ApprovalDecision{Choice: choice}
	if next != nil {
		h.enqueue(next)
	}
	h.mu.Unlock()
	h.publishEvents()
	return true
}

func (h *approvalHub) cancel(id int64) {
	if h.queue == nil {
		return
	}

	h.mu.Lock()

	cleared, next, outcome := h.queue.cancel(id)
	switch outcome {
	case cancelShowNextApproval:
		h.enqueue(next)
	case cancelClearVisibleApproval:
		h.events = append(h.events, ApprovalCleared{Alias: aliasForApproval(cleared), ID: id})
	case cancelNoVisibleChange:
	}
	h.mu.Unlock()
	h.publishEvents()
}

func (h *approvalHub) enqueue(p *pendingApproval) {
	if h.notify == nil || p == nil {
		return
	}
	req := p.req
	h.events = append(h.events, ApprovalRequested{Alias: p.alias, ID: p.id, Req: req})
}

// Drain in causal order, allowing observers to reenter without holding any lock.
// Reentrant publishers append to this drain rather than overtaking its events.
func (h *approvalHub) publishEvents() {
	h.mu.Lock()
	if h.publishing {
		h.mu.Unlock()
		return
	}
	h.publishing = true
	for len(h.events) > 0 {
		event := h.events[0]
		h.events[0] = nil
		h.events = h.events[1:]
		h.mu.Unlock()
		h.notify(event)
		h.mu.Lock()
	}
	h.publishing = false
	h.mu.Unlock()
}

func aliasForApproval(p *pendingApproval) participant.Alias {
	if p == nil {
		return participant.Alias{}
	}
	return p.alias
}
