package session

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/policy"
)

// EnablePolicyCommand enables a room-local runtime policy.
type EnablePolicyCommand struct {
	Name policy.Name
}

func (c EnablePolicyCommand) execute(s *Session) error {
	if c.Name == policy.EchoInvites && s.hasInvited && !s.policies.Enabled(policy.EchoInvites) {
		return fmt.Errorf("enable policy %q: must be enabled before the first invitation", c.Name)
	}
	if err := s.policies.Enable(c.Name); err != nil {
		return fmt.Errorf("enable policy %q: %w", c.Name, err)
	}
	return nil
}
