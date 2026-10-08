package interpreter

// WithObserver installs an application observer before startup. The observer
// receives an initial StateChanged followed by subsequent events in publication
// order. It must return promptly; front ends should queue events until ready.
func WithObserver(observer Observer) Option {
	return func(i *Interpreter) { i.executor.dispatcher.addObserver(observer) }
}

// publish is the outgoing interpreter-event boundary, not session-event input.
func (e *interpreterExecutor) publish(event Event) {
	e.dispatcher.Publish(event)
}
