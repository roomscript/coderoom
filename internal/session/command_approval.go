package session

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/agent"
)

// ResolveApprovalCommand resolves the active approval request and advances the
// session-managed approval queue.
type ResolveApprovalCommand struct {
	ApprovalID int64
	Choice     agent.ApprovalOption
}

func (c ResolveApprovalCommand) execute(s *Session) error {
	if s.approvals == nil {
		return fmt.Errorf("no approval hub configured on session")
	}
	if c.ApprovalID == 0 {
		return fmt.Errorf("approval id is required")
	}
	if !s.approvals.resolve(c.ApprovalID, c.Choice) {
		return fmt.Errorf("approval %d not active (already resolved, canceled, or queued)", c.ApprovalID)
	}
	return nil
}
