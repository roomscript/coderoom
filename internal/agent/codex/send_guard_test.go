package codex

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestSend_whenTurnActive_returnsErrTurnInProgress(t *testing.T) {
	var stdin bytes.Buffer
	c := newWithIO(t, nopWriteCloser{&stdin}, bytes.NewBuffer(nil), nil)
	c.turn.mu.Lock()
	c.turn.threadID = "t1"
	c.turn.state = turnState{kind: turnInflightUnknownID}
	c.turn.mu.Unlock()

	if _, err := c.Send("hi"); !errors.Is(err, agent.ErrTurnInProgress) {
		t.Fatalf("expected ErrTurnInProgress, got %v", err)
	}
}

func TestSend_afterTurnCompleted_allowsNextTurn(t *testing.T) {
	stdin := &strings.Builder{}
	stdoutR, stdoutW := io.Pipe()
	t.Cleanup(func() { _ = stdoutR.Close() })

	c := newWithIO(t, nopWriteCloser{stdin}, stdoutR, nil)
	c.turn.mu.Lock()
	c.turn.threadID = "t1"
	c.turn.state = turnState{kind: turnIdle}
	c.turn.mu.Unlock()

	if _, err := c.Send("first"); err != nil {
		t.Fatalf("Send(first): %v", err)
	}

	// Complete the turn; this should clear the in-flight guard.
	_, _ = io.WriteString(stdoutW, "{\"method\":\"turn/completed\",\"params\":{}}\n")

	deadline := time.NewTimer(250 * time.Millisecond)
	defer deadline.Stop()
	for {
		c.turn.mu.Lock()
		state := c.turn.state
		c.turn.mu.Unlock()
		if state.kind == turnIdle {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for turn to become idle")
		default:
			time.Sleep(1 * time.Millisecond)
		}
	}

	if _, err := c.Send("second"); err != nil {
		t.Fatalf("Send(second): %v", err)
	}
}
