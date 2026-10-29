package transcript

import (
	"context"
	"fmt"
)

// ProtocolObserver receives raw JSON-RPC traffic, independent of the backend client.
type ProtocolObserver interface {
	OnSend(string)
	OnReceive(string)
}

// TurnActivityObserver signals activation for interruption actions. Recording
// remains in the optional wrapped observer; this wrapper retains no transcript.
type TurnActivityObserver struct {
	observer ProtocolObserver
	active   chan struct{}
	detect   func(string) bool
}

// NewTurnActivityObserver tracks turn activation for recording and replay.
// A nil observer tracks activation without recording traffic, as in replay.
func NewTurnActivityObserver(observer ProtocolObserver, detect func(string) bool) *TurnActivityObserver {
	return &TurnActivityObserver{observer: observer, detect: detect, active: make(chan struct{}, 16)}
}

// OnSend forwards outbound traffic to the wrapped observer.
func (o *TurnActivityObserver) OnSend(raw string) {
	if o.observer != nil {
		o.observer.OnSend(raw)
	}
}

// OnReceive forwards traffic and signals activity recognized by the backend.
func (o *TurnActivityObserver) OnReceive(raw string) {
	if o.observer != nil {
		o.observer.OnReceive(raw)
	}
	if o.detect == nil || !o.detect(raw) {
		return
	}
	o.Signal()
}

// Signal marks activity independently of protocol traffic.
func (o *TurnActivityObserver) Signal() {
	select {
	case o.active <- struct{}{}:
	default:
	}
}

// Reset forgets activity from actions preceding the next selection.
func (o *TurnActivityObserver) Reset() {
	for {
		select {
		case <-o.active:
		default:
			return
		}
	}
}

// Wait waits for activation or context cancellation without consuming agent messages.
func (o *TurnActivityObserver) Wait(ctx context.Context) error {
	select {
	case <-o.active:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for turn activity: %w", ctx.Err())
	}
}
