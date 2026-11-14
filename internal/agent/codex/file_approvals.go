package codex

import (
	"encoding/json"

	"github.com/roomscript/coderoom/internal/agent"
)

type fileApprovalIdentity struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
}

func (id fileApprovalIdentity) valid() bool {
	return id.ThreadID != "" && id.TurnID != "" && id.ItemID != ""
}

// Only the stdout reader accesses this cache. Queued approvals own a copy, so
// subsequent notifications and cleanup cannot change a pending prompt.
func (c *Client) trackFileApprovalEvidence(msg rpcEnvelope) {
	switch msg.Method {
	case methodTurnCompleted, methodTurnFailed:
		c.approvals.files = nil
	case methodItemStarted, methodItemCompleted:
		var p struct {
			fileApprovalIdentity
			Item fileChangeItem `json:"item"`
		}
		if json.Unmarshal(msg.Params, &p) != nil {
			return
		}
		p.ItemID = p.Item.ID
		if !p.valid() {
			return
		}
		delete(c.approvals.files, p.fileApprovalIdentity)
		if msg.Method == methodItemStarted && p.Item.Type == "fileChange" {
			if c.approvals.files == nil {
				c.approvals.files = make(map[fileApprovalIdentity][]agent.FileChange)
			}
			c.approvals.files[p.fileApprovalIdentity] = fileChangesFromWire(p.Item.Changes)
		}
	}
}
