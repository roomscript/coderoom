package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
)

type approvalNotifier func(Event)

type approvalListenerFunc func(context.Context, agent.ApprovalRequest) (agent.ApprovalDecision, error)

func (f approvalListenerFunc) Decide(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return f(ctx, req)
}

type pendingApproval struct {
	id    int64
	alias string
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
	mu     sync.Mutex
	queue  *approvalQueue
	notify approvalNotifier
}

func newApprovalHub(notify approvalNotifier) *approvalHub {
	return &approvalHub{queue: &approvalQueue{}, notify: notify}
}

func (h *approvalHub) Listener(alias string) agent.ApprovalListener {
	return approvalListenerFunc(func(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
		if h == nil {
			return agent.ApprovalDecision{}, fmt.Errorf("approval listener for %q: hub is nil", alias)
		}
		return h.request(ctx, alias, req)
	})
}

func (h *approvalHub) request(ctx context.Context, alias string, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
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
		h.publish(pending)
	}
	h.mu.Unlock()

	select {
	case d := <-pending.resp:
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
	defer h.mu.Unlock()

	resolved, next, ok := h.queue.resolve(id)
	if !ok {
		return false
	}

	resolved.resp <- agent.ApprovalDecision{Choice: choice}
	if next != nil {
		h.publish(next)
	}
	return true
}

func (h *approvalHub) cancel(id int64) {
	if h.queue == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	cleared, next, outcome := h.queue.cancel(id)
	switch outcome {
	case cancelShowNextApproval:
		h.publish(next)
	case cancelClearVisibleApproval:
		h.notify(ApprovalCleared{Alias: aliasForApproval(cleared), ID: id})
	case cancelNoVisibleChange:
	}
}

func (h *approvalHub) publish(p *pendingApproval) {
	if h.notify == nil || p == nil {
		return
	}
	req := p.req
	h.notify(ApprovalRequested{Alias: p.alias, ID: p.id, Req: req})
}

func aliasForApproval(p *pendingApproval) string {
	if p == nil {
		return ""
	}
	return p.alias
}
