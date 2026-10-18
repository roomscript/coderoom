package monolith

// User requests enter the serialized interpreter path through these methods.

// ResolveApproval queues a structured response to the active approval.
func (i *Interpreter) ResolveApproval(id int64, choice ApprovalChoice) error {
	return i.executor.resolveApproval(id, choice)
}
