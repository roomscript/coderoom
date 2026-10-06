// Package echo provides a deterministic agent adapter for prompt-language tests.
package echo

import (
	"errors"
	"fmt"
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
)

var errStopped = errors.New("echo agent stopped")

// Client returns every prompt unchanged through the ordinary anchored-turn
// protocol. Notices complete normally without producing visible output.
type Client struct {
	mu      sync.Mutex
	started bool
	stopped bool
	nextID  uint64
	turns   chan turn
	stop    chan struct{}
	once    sync.Once
}

type turn struct {
	id     agent.StreamID
	prompt string
	notice bool
	flush  bool
}

// New constructs an echo adapter.
func New() *Client {
	return &Client{turns: make(chan turn, 1), stop: make(chan struct{})}
}

// Start marks the adapter ready.
func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return errStopped
	}
	c.started = true
	return nil
}

// Send starts an anchored turn that outputs prompt unchanged.
func (c *Client) Send(prompt string) (agent.StreamID, error) {
	return c.send(prompt, false)
}

// SendNotice starts an anchored, output-free notice turn.
func (c *Client) SendNotice(prompt string) (agent.StreamID, error) {
	return c.send(prompt, true)
}

func (c *Client) send(prompt string, notice bool) (agent.StreamID, error) {
	c.mu.Lock()
	if !c.started || c.stopped {
		c.mu.Unlock()
		return "", fmt.Errorf("echo agent is not running")
	}
	c.nextID++
	id := agent.StreamID(fmt.Sprintf("echo:turn:%d", c.nextID))
	c.mu.Unlock()

	select {
	case c.turns <- turn{id: id, prompt: prompt, notice: notice}:
		return id, nil
	case <-c.stop:
		return "", errStopped
	}
}

// Read blocks until the next output or turn-completion message.
func (c *Client) Read() (agent.Message, error) {
	select {
	case <-c.stop:
		return agent.Message{}, errStopped
	default:
	}

	select {
	case <-c.stop:
		return agent.Message{}, errStopped
	case item := <-c.turns:
		if item.flush || item.notice {
			return agent.Message{StreamID: item.id, Mode: agent.ModeFlush, Content: agent.Output{}}, nil
		}
		select {
		case c.turns <- turn{id: item.id, flush: true}:
		case <-c.stop:
			return agent.Message{}, errStopped
		}
		return agent.Message{StreamID: item.id, Mode: agent.ModeStream, Content: agent.Output{Text: item.prompt}}, nil
	}
}

// Interrupt is a no-op because echo turns complete without external work.
func (c *Client) Interrupt() error { return nil }

// Stop terminates the adapter and unblocks Read and Send.
func (c *Client) Stop() error {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	c.once.Do(func() { close(c.stop) })
	return nil
}
