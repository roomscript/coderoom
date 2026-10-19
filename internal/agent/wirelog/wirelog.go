// Package wirelog writes an agent's raw protocol lines to a log, for
// --agent-log.
package wirelog

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Observer writes every raw protocol line to w with a timestamp, agent
// alias, and direction arrow. It satisfies each backend's ProtocolObserver.
// It is safe for concurrent use so multiple agents can share a single writer
// (e.g. one file).
type Observer struct {
	mu    sync.Mutex
	w     io.Writer
	alias string
}

// New returns an Observer that tags each line with alias and writes to w.
func New(w io.Writer, alias string) *Observer {
	return &Observer{w: w, alias: alias}
}

// OnSend records an outbound line.
func (o *Observer) OnSend(msg string) {
	o.write("→", msg)
}

// OnReceive records an inbound line.
func (o *Observer) OnReceive(msg string) {
	o.write("←", msg)
}

func (o *Observer) write(dir, msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.w, "%s %s %s %s\n",
		time.Now().UTC().Format(time.RFC3339Nano), o.alias, dir, msg)
}
