package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// controlHandler receives the answer to one of our control requests: the
// response body, or the error Claude reported. It runs on the stdout reader
// goroutine.
type controlHandler func(body json.RawMessage, err error)

// sendControl registers handler under id and writes the control request.
// An error means the request was not written, and handler is never called.
// If the process exits first, handler is never called either.
func (c *Client) sendControl(id string, request map[string]any, handler controlHandler) error {
	c.control.mu.Lock()
	c.control.pending[id] = handler
	c.control.mu.Unlock()
	err := c.writeJSON(controlRequest{Type: typeControlRequest, RequestID: id, Request: request})
	if err != nil {
		c.takeControlHandler(id)
	}
	return err
}

// awaitControl sends a control request and waits for its answer, up to the
// start timeout. Used during Start, before the client is handed out.
func (c *Client) awaitControl(id string, request map[string]any) (json.RawMessage, error) {
	type answer struct {
		body json.RawMessage
		err  error
	}
	done := make(chan answer, 1)
	err := c.sendControl(id, request, func(body json.RawMessage, err error) {
		done <- answer{body, err}
	})
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(c.startTimeout)
	defer timer.Stop()
	select {
	case a := <-done:
		return a.body, a.err
	case <-c.exited:
		return nil, errors.New("process exited")
	case <-timer.C:
		return nil, fmt.Errorf("no %s response after %s", request["subtype"], c.startTimeout)
	}
}

// handleControlResponse routes an answer to the request that registered
// its ID. Answers to unregistered requests, such as interrupt, are ignored.
func (c *Client) handleControlResponse(line *controlResponseLine) {
	handler := c.takeControlHandler(line.Response.RequestID)
	if handler == nil {
		return
	}
	if line.Response.Subtype == "error" {
		handler(nil, errors.New(line.Response.Error))
		return
	}
	handler(line.Response.Response, nil)
}

func (c *Client) takeControlHandler(id string) controlHandler {
	c.control.mu.Lock()
	defer c.control.mu.Unlock()
	handler := c.control.pending[id]
	delete(c.control.pending, id)
	return handler
}
