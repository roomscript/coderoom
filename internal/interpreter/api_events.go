package interpreter

import "github.com/roomscript/coderoom/internal/interpreter/monolith"

// WithObserver installs an observer before startup. It receives initial state and
// subsequent events in publication order, and must return promptly.
func WithObserver(observer Observer) Option {
	return func(i *Interpreter) { monolith.WithObserver(observer)(i.fallback) }
}
